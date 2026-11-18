# Nextcloud MCP en Go: diseño

Fecha: 2026-11-18. Estado: borrador, pendiente de aprobación.

## Intención

Que el asistente de voz (emo Xiaozhi → xiaozhi-server-go → DeepSeek) haga por voz el trabajo de "secretaria" que hoy hace el bot de Telegram (`nextcloud-ollama-telegram/skills/secretaria.md`):

- calendario y eventos
- tareas VTODO por proyecto
- notas
- archivos y bitácoras diarias en Nextcloud

El usuario pidió que el servidor MCP esté **escrito en Go**, por liviano y para que viva junto al servidor Go.

## Objetivos

1. Un binario Go (`nextcloud-mcp`) que habla MCP por stdio. Porta las 24 operaciones de `nextcloud-ollama-telegram/src/nextcloud/operations.ts`, con la misma semántica:
   - files: list, read, write, delete, mkdir, search
   - notes: list, read, create, update, delete
   - calendar: list, list_events, search, create_event, delete_event
   - tasks: list_calendars, create_calendar, list, create_list, create, update, complete, delete
2. Un tool nuevo `bitacora_append` (solo agrega), con la convención de la secretaria:
   - ruta `Bitacora/{contexto}/YYYY/YYYY-MM-DD.md`, donde `contexto` es texto libre y normalizado (minúsculas, sin acentos, guiones), igual que en Telegram: `trabajo`, `personal`, `mesa-libre`… Así las entradas de voz caen en las mismas carpetas que las del bot.
   - entradas `## HH:MM — título`
   - crea las carpetas y el archivo si no existen
   - el log automático a la bitácora tras `tasks_complete` que describe secretaria.md se pide en el prompt, no lo hace el MCP solo (mi decisión)
   - además, `notes_append` y `files_append`, libres, para que "agrega X a la nota" no obligue a reescribir contenido truncado (mi decisión, a raíz de la revisión)
3. Confirmación obligatoria para borrar y editar ("borrar y editar siempre con confirmación, lo demás libre"), implementada en el propio MCP (enfoque A, abajo).
4. Respuestas compactas para voz: resultados cortos, sin XML ni iCal crudo.
5. Empaquetado en la imagen existente de xiaozhi y desplegado por Coolify igual que hoy.

## No-objetivos (fase posterior)

- Recordatorios proactivos y briefing matutino: el dispositivo no recibe pushes del servidor sin más trabajo.
- Transporte SSE/HTTP: el cliente MCP del fork solo soporta stdio.
- Whitelist por skill del proyecto TS: la reemplazan los tokens de confirmación.
- Capabilities/caché de apps instaladas: si una app falta, el error HTTP se devuelve legible y basta.

## Anti-objetivos (fracaso aunque "funcione")

- Que se borre o sobrescriba algo sin que el usuario lo haya confirmado de viva voz.
- Romper o ralentizar notablemente el pipeline de voz que ya funciona. Un fallo del MCP no debe tumbar la conexión, solo quedar sin tools.
- Filtrar credenciales: en logs, en el repo, o hacia cualquier servicio que no sea Nextcloud.
- Respuestas eternas leídas en voz alta, como un listado de 200 archivos.

## Restricciones

- Siguen vigentes las reglas de seguridad del proyecto:
  - solo DeepSeek como servicio externo
  - secretos solo en Coolify o en `server/.env` (git-ignored)
  - escaneo de secretos antes de cada push
  - no tocar Traefik, Frigate, Nextcloud ni Coolify
- Nextcloud se accede con una **app password nueva, dedicada a xiaozhi** (decidido por el usuario): revocable sin afectar al bot de Telegram. El usuario la crea y la carga en Coolify. Hay que verificar en el despliegue que `NEXTCLOUD_URL` sea alcanzable desde el contenedor de xiaozhi, sin tocar Traefik. Las variables, como en el proyecto TS, son `NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD` y `TZ`, definidas en Coolify.
- Integración con el fork:
  - `src/core/mcp/manager.go` lee `.mcp_server_settings.json` desde el cwd (`/app`).
  - El cliente es `NewStdioMCPClient(command, env, args...)` de `github.com/mark3labs/mcp-go v0.29.0`.
  - Registra los tools como `mcp_<nombre>` y toma el primer `TextContent` del resultado.
  - El manager se crea **por conexión** de dispositivo, así que hay un subproceso MCP por conexión.
- Los nombres de tool deben cumplir `^[a-zA-Z0-9_-]+$` (function calling estilo OpenAI). Se usa guion bajo: `tasks_create`, no `tasks.create`.
- El servidor se escribe con el paquete `server` de mark3labs/mcp-go, la misma librería que ya usa el fork. Solo stdlib más esa librería, sin CGO.

## Enfoques considerados

- **A. Token de confirmación en dos pasos dentro del MCP (elegido por el usuario), más una guardia mínima en el fork (elegida por el usuario tras la revisión).** Los tools destructivos no ejecutan nada. Guardan la acción pendiente y devuelven un código más la instrucción de preguntar al usuario. Un tool `confirm(codigo)` la ejecuta. Es la única barrera que no depende de que el LLM se porte bien, y no toca el fork.
- **B. Confirmación en el loop de agente de xiaozhi:** interceptar tool calls marcados y pedir el "sí" en el turno siguiente. Es más robusto en teoría, pero invasivo en el fork y lo acopla a este MCP.
- **C. Solo instrucciones en el prompt:** frágil, porque el LLM puede ejecutar sin preguntar. Descartado como barrera única; se usa como complemento de A.

## Diseño

### Clasificación de tools

| Libre (ejecuta directo) | Requiere confirmación |
|---|---|
| files_list, files_read, files_search, files_mkdir | files_delete |
| files_write **solo si el archivo no existe** | files_write sobre archivo existente (sobrescribe) |
| notes_list, notes_read, notes_create | notes_update, notes_delete |
| calendar_list, calendar_list_events, calendar_search, calendar_create_event | calendar_delete_event |
| tasks_list_calendars, tasks_create_calendar, tasks_list, tasks_create_list, tasks_create | tasks_update, tasks_delete |
| **tasks_complete** (excepción acordada) | |
| **bitacora_append** (excepción acordada: nunca sobrescribe) | |
| notes_append, files_append (mi decisión: agregar no es editar, mismo criterio que la bitácora) | |

`files_write` hace un PROPFIND/HEAD previo. Si el archivo existe, pasa al flujo de confirmación.

### Flujo de confirmación

1. El LLM llama a un tool destructivo, p. ej. `files_delete(path)`. El MCP valida los parámetros y, cuando aplica, comprueba que el objetivo existe. **No ejecuta.**
2. Guarda `{código, acción, params, resumen, emitido_en}` en memoria y responde:
   ```
   PENDIENTE: pregunta al usuario "¿Borro el archivo X?". Solo si responde que sí en su próximo mensaje, llama confirm con codigo=4821.
   ```
3. `confirm(codigo)` ejecuta la acción guardada y la elimina del mapa. Se rechaza si:
   - el código no existe o ya se usó;
   - pasaron más de **120 s** desde que se emitió (expirado);
   - (el piso de 5 s del borrador se reemplaza por la guardia del fork, abajo)
4. Hay un tool `cancel(codigo)` opcional para descartar. Si no se llama, el código expira solo.

Detalles:

- Códigos de 4 dígitos aleatorios (`crypto/rand`), únicos entre los pendientes vivos.
- Estado solo en memoria, en el subproceso, que es por conexión. Si se corta la conexión, los pendientes se pierden, y es lo correcto.
- El resumen de la acción usa nombres legibles: título de la tarea o evento, nombre del archivo. Para eso, en tasks_update/delete y calendar_delete_event el MCP lee primero el recurso.

### Guardia anti auto-confirmación en el fork (decidido por el usuario)

La revisión mostró que, en el fork, `handleFunctionResult` vuelve a llamar al LLM apenas responde un tool, sin esperar al usuario. Por eso un piso de tiempo no frena una auto-confirmación.

Se agrega un parche chico al fork, en el lugar donde despacha tool calls MCP:

- La conexión guarda el índice del último mensaje de usuario (o un contador de turnos de usuario) en el momento en que un tool devuelve un resultado que empieza con `PENDIENTE:`.
- Si después llega una llamada a `mcp_confirm` y no hubo un mensaje de usuario nuevo desde ese PENDIENTE, el fork no la reenvía al MCP. Responde al LLM: "Rechazado: debes esperar la respuesta del usuario".
- Es genérico: aplica a cualquier tool llamado `confirm` de un MCP, no está acoplado a Nextcloud.
- Lleva tests unitarios en el fork.

El token con expiración de 120 s y uso único sigue en el MCP como segunda barrera.

### Prompt

En `server/xiaozhi/config.template.yaml` se agrega un bloque breve con:

- Cuándo usar cada tool.
- Las convenciones de la secretaria: ids de calendario de tareas (joyeria, casa, mascotas, mesa-libre, bot-opticas, bot-ofertas, scraper-bancos, bot-secretaria, otros, tareas), prioridades 1/5/9, rutas de bitácora.
- La regla: "ante PENDIENTE, pregunta y espera; nunca llames confirm sin un sí explícito del usuario en ese mismo mensaje".

**Ojo:** ese archivo tiene un cambio sin commitear del usuario (el nombre "weita"). Se edita encima, sin pisarlo.

### Respuestas para voz

- Texto plano o JSON mínimo con solo los campos útiles.
- Listados con límite por defecto de 10 y `has_more`.
- Contenido de archivos y notas truncado a unos **2000 caracteres**, con un aviso de truncado. Para no perder contenido, `notes_update` y `files_write` con sobrescritura se rechazan si el contenido actual pasa de ese límite: el LLM no pudo haberlo visto entero. En ese caso, el mensaje sugiere usar `*_append` (mi decisión). El proyecto TS usa 6000, pero aquí todo se lee en voz alta.
- Fechas en hora local (`TZ`).
- Errores en una línea en español, p. ej. "Nextcloud respondió 404: no existe /x.md". Nunca stack traces ni la URL con credenciales.

### Cliente Nextcloud (port de client.ts e ical.ts)

- `net/http` con basic auth y timeout de 15 s. Un reintento tras 2 s ante 423 Locked.
- Rutas:
  - WebDAV: `/remote.php/dav/files/{user}{path}`
  - CalDAV: `/remote.php/dav/calendars/{user}/{cal}/`
  - Notes: `/apps/notes/api/v1/notes` con `OCS-APIRequest: true`
- iCal a mano, como en ical.ts:
  - `toIcalUtc` (local → UTC; solo fecha ⇒ T00:00:00)
  - `toIcalDate`, `extractIcalField` (con unfolding y params)
  - `setVtodoField`, `makeVtodo`
  - VEVENT con all_day (VALUE=DATE más el día siguiente) y rrule
  - subtareas con `RELATED-TO;RELTYPE=PARENT`
- PROPFIND y REPORT parseados con `encoding/xml`, saltando la entrada raíz como hace el TS.
- UIDs: `<unixnano>@xiaozhi-nextcloud`.
- `calendar_delete_event` y las tareas aceptan el `filename` que devuelven los listados, además del uid. Así se pueden borrar eventos creados fuera del bot. Es una pequeña desviación del TS (mi decisión).

### Empaquetado

- Módulo Go nuevo en `server/nextcloud-mcp/`, independiente de `xiaozhi-server-go`.
- `server/Dockerfile`:
  - etapa builder nueva con `CGO_ENABLED=0 go build -o /out/nextcloud-mcp`
  - `COPY` del binario a `/app/nextcloud-mcp`
- `/app/.mcp_server_settings.json`, versionado en `server/xiaozhi/` y copiado en la imagen:
  ```json
  {"mcpServers": {"nextcloud": {"command": "/app/nextcloud-mcp", "args": [], "env": {}}}}
  ```
- Credenciales: el subproceso las hereda del entorno del contenedor. Creo que mcp-go v0.29 hace `cmd.Env = append(os.Environ(), env...)`; **el builder debe verificarlo**.
  - Si no hereda, `entrypoint.sh` renderiza el JSON con `envsubst` y `chmod 600`, igual que hoy con `.config.yaml`.
  - Si faltan `NEXTCLOUD_*`, el binario MCP termina con un error en stderr, y xiaozhi arranca sin tools de Nextcloud, con la voz intacta (mi decisión, por el anti-objetivo). El entrypoint no aborta.
  - `docker-compose.yml` pasa `NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD` y `TZ` al servicio xiaozhi.
- El binario nunca loguea a stdout, que es el canal MCP. Logs a stderr, sin credenciales.

### Pruebas

- Fixtures: XML/JSON escritos a mano según la documentación de WebDAV, CalDAV y Notes, y con la forma que esperan los parsers del TS. La validación real contra Nextcloud es el E2E por voz que hace el usuario (mi decisión).
- Unitarias:
  - funciones iCal y parsers XML, con fixtures de respuestas reales de Nextcloud (anonimizadas)
  - el store de confirmaciones: expira, piso de 5 s, uso único
- Integración contra un `httptest.Server` que simula WebDAV, CalDAV y Notes para cada tool.
- E2E manual por voz: crear tarea, listar, completar, borrar con confirmación, append a bitácora.
- Verificar que DeepSeek, vía LiteLLM, hace tool calling bien con el alias actual.

## Riesgos conocidos

- **Tool calling de DeepSeek por LiteLLM:** si el alias `deepseek-chat` apunta a un modelo de razonamiento, puede fallar o ser lento con tools. Se valida al inicio de la implementación. Si falla, se cambia el modelo del alias por otro de DeepSeek sin razonamiento: se sigue sin usar proveedores fuera de DeepSeek (mi decisión).
- **Latencia:** cada tool call agrega un viaje LLM más uno a Nextcloud. Hay que vigilar cuántas iteraciones permite el fork.
- **24+ tools en el prompt:** cuesta tokens y precisión. Si DeepSeek se confunde, se pueden fusionar tools afines.

## Lo que queda al builder

- Estructura interna de paquetes, nombres y librería de testing.
- Verificar la herencia de env en mcp-go stdio y elegir entre herencia y envsubst.
- Formato exacto de las respuestas compactas, dentro de los límites de arriba.
- Cómo el resumen legible obtiene el título antes de pedir confirmación.
