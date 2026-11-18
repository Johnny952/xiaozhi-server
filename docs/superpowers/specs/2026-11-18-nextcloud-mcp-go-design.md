# Nextcloud MCP en Go: diseño

Fecha: 2026-11-18. Estado: borrador v2 (herramientas de dominio, solo-agregar), pendiente de aprobación.

## Intención

Que el asistente de voz (emo Xiaozhi → xiaozhi-server-go → DeepSeek) haga por voz el trabajo de "secretaria" que hoy hace el bot de Telegram (`nextcloud-ollama-telegram/skills/secretaria.md`):

- agenda (eventos de calendario)
- tareas VTODO por proyecto
- notas (app Notes)
- bitácoras diarias y documentos de estado de proyecto en archivos

El usuario pidió:

- que el servidor MCP esté **escrito en Go**, por liviano y para que viva junto al servidor Go;
- que **no sea un CRUD crudo** (listar/crear/editar/borrar archivos), sino herramientas de más alto nivel:
  - escribir es **siempre agregar algo nuevo**;
  - corregir es **una entrada nueva que dice qué reemplaza**;
  - leer la bitácora es que **el MCP traiga los archivos y los junte**.

El porqué: la skill de secretaria tiene muchas reglas de procedimiento que el LLM tiene que acordarse de cumplir:

- leer antes de escribir
- crear la carpeta
- poner el encabezado
- revisar duplicados
- anotar en la bitácora al completar una tarea
- no inventar rutas

En el código se cumplen siempre. El prompt queda más corto y hay menos viajes LLM ↔ tool, o sea menos latencia por voz.

## Objetivos

1. Un binario Go (`nextcloud-mcp`) que habla MCP por stdio y expone **herramientas de dominio** (tabla abajo), unas 19, en vez de las 24 operaciones crudas del proyecto TS.
2. **Archivos, notas y bitácoras son solo-agregar** desde la voz. No existe ninguna herramienta que borre o reescriba un archivo o una nota. Eso se hace en la web de Nextcloud.
3. Las reglas de procedimiento de secretaria.md viven en el código:
   - las rutas, que el LLM nunca elige
   - el encabezado de archivo
   - la creación de carpetas
   - la detección de duplicados
   - el auto-registro en la bitácora al completar una tarea
   - el filtro del briefing
4. Confirmación obligatoria solo para lo que cambia o borra estado existente: **cambiar o borrar tareas y cancelar eventos**. Se implementa con un token en el MCP más una guardia en el fork (enfoque A, abajo).
5. Respuestas compactas para voz.
6. Compatibilidad con el bot de Telegram: mismas carpetas, mismo formato de bitácora y mismos calendarios. Ambos conviven.
7. Empaquetado en la imagen existente de xiaozhi, desplegado por Coolify como hoy.

## No-objetivos (fase posterior)

- Recordatorios proactivos y briefing a las 9:00 empujado al dispositivo: el dispositivo no recibe pushes. `resumen_del_dia` cubre el caso "¿qué tengo hoy?" a pedido.
- Dictar documentos nuevos en rutas libres (`Documentos/`, `Contactos/`): por voz solo se pueden leer (`archivo_leer`). Decidido con el usuario.
- Borrar o reescribir archivos, notas o entradas de bitácora por voz.
- Transporte SSE/HTTP: el cliente MCP del fork solo soporta stdio.
- Crear o borrar calendarios de eventos; borrar proyectos (calendarios de tareas).

## Anti-objetivos (fracaso aunque "funcione")

- Que se borre o modifique una tarea o evento sin que el usuario lo haya confirmado de viva voz.
- Que se pierda contenido de archivos o notas. Por diseño, nada se sobrescribe y las correcciones conservan el original.
- Pisar lo que el bot de Telegram escribió al mismo tiempo.
- Romper o ralentizar notablemente el pipeline de voz. Un fallo del MCP deja la conexión sin tools, no la tumba.
- Filtrar credenciales: en logs, en el repo, o hacia cualquier servicio que no sea Nextcloud.
- Respuestas eternas leídas en voz alta.

## Restricciones

- Siguen vigentes las reglas de seguridad del proyecto:
  - solo DeepSeek como servicio externo
  - secretos solo en Coolify o en `server/.env` (git-ignored)
  - escaneo de secretos antes de cada push
  - no tocar Traefik, Frigate, Nextcloud ni Coolify
- Nextcloud se accede con una **app password nueva dedicada a xiaozhi** (decidido por el usuario): revocable sin afectar al bot. El usuario la crea y la carga en Coolify.
  - Variables: `NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD`, `TZ`.
  - En el despliegue hay que verificar que `NEXTCLOUD_URL` sea alcanzable desde el contenedor de xiaozhi, sin tocar Traefik.
- Integración con el fork:
  - `src/core/mcp/manager.go` lee `.mcp_server_settings.json` desde el cwd (`/app`).
  - El cliente es `NewStdioMCPClient(command, env, args...)` de `github.com/mark3labs/mcp-go v0.29.0`.
  - Registra los tools como `mcp_<nombre>` y toma el primer `TextContent` del resultado.
  - El manager se crea **por conexión**, así que hay un subproceso MCP por conexión.
- Los nombres de tool cumplen `^[a-zA-Z0-9_-]+$`.
- El servidor usa el paquete `server` de mark3labs/mcp-go, la misma librería del fork. Solo stdlib más esa librería, `CGO_ENABLED=0`.
- La lógica de bajo nivel se porta del proyecto TS (`nextcloud-ollama-telegram/src/nextcloud/{client,operations,ical}.ts`). Es la referencia de cómo hablar con WebDAV, CalDAV y Notes.

## Enfoques considerados

**Forma de las herramientas**
- **Herramientas de dominio, solo-agregar (elegido por el usuario).** Menos herramientas, reglas en código, casi nada destructivo.
- CRUD crudo con confirmación (borrador v1): más flexible, pero deja las reglas en manos del LLM, tiene 24+ tools y muchas confirmaciones. Descartado por el usuario.

**Confirmación de lo destructivo**
- **A. Token de dos pasos en el MCP más una guardia en el fork (elegido por el usuario).** La herramienta destructiva no ejecuta: devuelve un código y la instrucción de preguntar. `confirmar(codigo)` ejecuta. La guardia del fork impide que el LLM se confirme a sí mismo.
- B. Confirmación en el loop de agente del fork: invasivo y acoplado. Descartado.
- C. Solo prompt: frágil. Descartado como barrera única; queda como complemento.

## Diseño

### Herramientas

Todas en español, con guion bajo. "Libre" ejecuta directo; "Confirma" pasa por el flujo de confirmación.

| Herramienta | Parámetros | Qué hace | Modo |
|---|---|---|---|
| `bitacora_anotar` | contexto, titulo, texto, hora? | Agrega una entrada a `Bitacora/{ctx}/YYYY/YYYY-MM-DD.md` (ver formato) | Libre |
| `bitacora_corregir` | contexto, fecha, entrada, texto | Agrega una entrada de corrección **en el archivo del día de la entrada original** | Libre |
| `bitacora_leer` | contexto?, desde?, hasta?, texto? | Junta los archivos del rango y aplica correcciones (ver lectura) | Libre |
| `resumen_del_dia` | — | Eventos de hoy, más tareas vencidas, que vencen hoy o de prioridad alta (el filtro B del briefing de secretaria.md) | Libre |
| `agenda` | desde?, hasta? | Eventos del rango, por defecto hoy, de todos los calendarios de eventos | Libre |
| `evento_crear` | calendario, titulo, inicio, fin?, todo_el_dia?, lugar?, descripcion?, repetir?, forzar? | Crea un evento; sin `fin` dura 1 h y la respuesta lo dice. `calendario` es obligatorio: si el usuario no lo dijo, el LLM le pregunta (decidido por el usuario). Nombre inexistente ⇒ "No encontré…" más la lista de calendarios de eventos | Libre |
| `evento_cancelar` | evento, fecha? | Busca el evento por título (y fecha) y lo borra | **Confirma** |
| `tareas_pendientes` | proyecto?, texto? | Tareas no completadas de un proyecto o de todos | Libre |
| `tarea_crear` | proyecto, titulo, vence?, prioridad?, descripcion?, dentro_de?, crear_proyecto?, forzar? | Crea una tarea; `dentro_de` = título de la tarea padre (lista) | Libre |
| `tarea_completar` | proyecto, tarea | Marca como hecha y anota en la bitácora del proyecto | Libre (excepción acordada) |
| `tarea_anotar` | proyecto, tarea, texto | Agrega texto con fecha al final de la DESCRIPTION | Libre |
| `tarea_cambiar` | proyecto, tarea, nuevo_titulo?, vence?, prioridad? | Cambia campos de una tarea existente | **Confirma** |
| `tarea_borrar` | proyecto, tarea | Borra una tarea | **Confirma** |
| `nota_guardar` | titulo, texto | Si existe una nota con ese título, le agrega un bloque con fecha; si no, la crea | Libre |
| `notas_buscar` | texto | Busca por título y contenido; con una sola coincidencia devuelve su contenido | Libre |
| `estado_proyecto_anotar` | proyecto, texto, seccion? | Agrega un bloque con fecha a `Proyectos/{p}/estado.md` | Libre |
| `estado_proyecto_leer` | proyecto | Lee ese archivo | Libre |
| `archivo_leer` | ruta_o_nombre | Lee un archivo; si no es una ruta, lo busca por nombre (solo lectura) | Libre |
| `confirmar` | codigo | Ejecuta una acción pendiente | — |

Sobre la tabla:

- **Proyecto:** es el calendario de tareas. Se normaliza la entrada (minúsculas, sin acentos, guiones) y se compara con el id y el displayname de los calendarios existentes.
  - Si no existe, `tarea_crear` no lo crea solo: responde "No existe el proyecto X. Proyectos: …". Con `crear_proyecto=true` lo crea con MKCALENDAR VTODO y luego crea la tarea. Es mi decisión, para evitar que un error de transcripción cree calendarios basura.
- **Identificación por título** (`tarea`, `evento`, `entrada`):
  - coincidencia normalizada, primero exacta y luego "contiene"
  - con 0 coincidencias: "No encontré…" más hasta 5 candidatos parecidos
  - con >1: lista las coincidencias (título, fecha) y no hace nada
  - también acepta uid o filename
- **Duplicados:** los revisa el código, no el LLM.
  - `tarea_crear`: si hay una tarea **no completada** con el mismo título normalizado en el proyecto, no crea y avisa. Si la que coincide está completada, crea normal (repetición legítima).
  - `evento_crear`: un evento con el mismo título el mismo día bloquea. Un solapamiento de horario crea igual y lo menciona en la respuesta.
  - `forzar=true` salta el bloqueo, para cuando el usuario dijo "sí, créala igual". Es mi decisión.
- **Prioridad:** alta=1, media=5, baja=9, por defecto 5. Se aceptan las palabras alta/media/baja o el número.
  - Al leer, "alta" es PRIORITY 1–4 (semántica de Nextcloud Tasks). PRIORITY 0 = sin definir, **no** cuenta como alta. secretaria.md dice "0 o 1", pero 0 es indefinida en RFC 5545 y metería en el resumen toda tarea sin prioridad. Es mi decisión.
- **Calendarios de tareas = proyectos:** cuenta todo calendario que soporte VTODO, también los mixtos como "Personal" (igual que `list_calendars` del TS, que no filtra). Es mi decisión.
- **Formato de hora al escribir eventos:** `DTSTART;TZID=<TZ>` con VTIMEZONE, como Nextcloud web; nunca hora flotante. Al leer se aceptan flotante, UTC y TZID. Es mi decisión.
- **Eventos recurrentes:** `agenda` y `resumen_del_dia` los expanden pidiendo `<C:expand>` en el REPORT de CalDAV (lo hace el servidor; sin librería RRULE en Go). Es mi decisión.
- **Varias llamadas a la vez:** el fork solo ejecuta `toolCall[0]` de cada respuesta. No se arregla en esta fase. El prompt pide una herramienta por turno, y "agrega leche, pan y huevos" se hace en varias vueltas. Es mi decisión.
- **Fechas:** ISO 8601 en hora local (`TZ`). Solo fecha ⇒ tarea con vencimiento ese día.

### Bitácora

**Formato** (idéntico a secretaria.md, para convivir con Telegram):

```markdown
# Bitácora {contexto} — DD de mes de YYYY

## HH:MM — Título

Texto.

---
```

- El contexto se normaliza (minúsculas, sin acentos, guiones). Sin contexto no se escribe; el prompt dice que hay que preguntar.
- `hora?` sirve para hechos pasados; por defecto, la hora actual.
- Crea las carpetas `Bitacora/{ctx}/` y `Bitacora/{ctx}/YYYY/` con MKCOL si faltan (405 = ya existe), y el archivo con su encabezado.

**Corrección** (`bitacora_corregir`). Se agrega al final del archivo del día de la entrada original:

```markdown
## HH:MM — Corrección: Título original

> Corrige: HH:MM — Título original

Texto corregido.

---
```

- `entrada` identifica la original por hora (`10:30`), por título, o por ambos, con las reglas de identificación de arriba.
- Si no la encuentra, no escribe nada.

**Lectura** (`bitacora_leer`):

- Sin contexto: todos los contextos (PROPFIND Depth 1 de `Bitacora/`).
- Rango por defecto: hoy. Máximo 31 días.
- El MCP hace GET de cada `YYYY-MM-DD.md` del rango (un 404 se ignora) en paralelo, con un tope de concurrencia de 4. Parsea las entradas y las ordena cronológicamente.
- **Correcciones:** la entrada original se muestra con el texto de la corrección y la marca "(corregida HH:MM)". La entrada de corrección no se lista aparte. Si hay varias correcciones, gana la última.
- **Salida:**
  - un solo día: texto completo de las entradas
  - varios días: solo `fecha · HH:MM — título [contexto]`, agrupado por día, con la sugerencia "pide el detalle de un día"
  - `texto`: filtra entradas que lo contengan (normalizado), con rango por defecto de los últimos 30 días en ese caso
  - siempre con el límite de tamaño de voz (abajo)
- Al parsear, si un archivo no sigue el formato (editado a mano), se devuelve como un bloque de texto sin estructurar, no falla.

### Tarea completada → bitácora

`tarea_completar` hace dos cosas:

1. Pone STATUS:COMPLETED, PERCENT-COMPLETE:100 y COMPLETED con el timestamp.
2. Agrega a la bitácora del contexto igual al id del proyecto (p. ej. `mesa-libre`) la entrada `## HH:MM — Completada: {título}`, con el nombre del proyecto como texto.

Si el paso 2 falla, responde "Tarea completada; no pude anotarla en la bitácora: {error}". No deshace el paso 1.

### Notas y estado de proyecto (solo-agregar)

- **`nota_guardar`:** busca una nota con el título normalizado exacto.
  - Si existe, PUT con el contenido actual más `\n\n---\n**DD/MM/YYYY HH:MM**\n\n{texto}`, y responde "Agregado a la nota existente 'X'".
  - Si no, crea la nota con ese título y texto.
- **`estado_proyecto_anotar`:** mismo patrón sobre `Proyectos/{p}/estado.md`, con el bloque `## DD/MM/YYYY — {seccion o "Actualización"}`. Si el archivo no existe, crea la carpeta y el archivo con el encabezado `# Estado: {proyecto}`.

### Escrituras concurrentes (lectura-modificación-escritura)

WebDAV no tiene "agregar al final", así que todo agregado es GET → modificar → PUT:

- Archivos: GET devuelve el `ETag`, y el PUT lleva `If-Match: {etag}`. Un archivo nuevo usa `If-None-Match: *`.
- Ante 412: hasta 2 reintentos del ciclo completo; luego un error legible.
- Notes: la API devuelve `etag` por nota. Se usa `If-Match` si la API lo respeta; si no, se relee justo antes del PUT. Queda al builder verificarlo.
- Se mantiene el reintento ante 423 Locked tras 2 s del cliente TS.

### Flujo de confirmación

Aplica a `evento_cancelar`, `tarea_cambiar` y `tarea_borrar`.

1. La herramienta valida, identifica el recurso (leyéndolo) y **no ejecuta**.
2. Guarda `{codigo, accion, params, resumen, emitido_en}` en memoria y responde:
   ```
   PENDIENTE: pregunta al usuario "¿Borro la tarea 'Comprar alambre' de joyería?". Solo si responde que sí en su próximo mensaje, llama confirmar con codigo=4821.
   ```
   - El resumen usa nombres legibles.
   - Para eventos recurrentes avisa "(se borra la serie completa)".
   - `tarea_cambiar` enumera cada campo como `antes → después`.
3. `confirmar(codigo)` ejecuta y borra la entrada. Se rechaza si el código no existe o ya se usó, o si tiene más de **120 s**.
4. Sin herramienta de cancelar: el código expira solo.

Detalles:

- Códigos de 4 dígitos de `crypto/rand`, únicos entre los vivos.
- Estado en memoria del subproceso, que es por conexión.
- Antes de ejecutar, `confirmar` relee el recurso. Si cambió (otro ETag), no ejecuta y avisa.

### Guardia anti auto-confirmación en el fork (decidido por el usuario)

En el fork, `handleFunctionResult` vuelve a llamar al LLM apenas responde un tool, sin esperar al usuario. Por eso se agrega un parche chico en el despacho de tool calls MCP:

- Cuando un resultado de tool empieza con `PENDIENTE:`, la conexión guarda el código y queda "esperando la siguiente frase".
- Solo la **frase del usuario inmediatamente siguiente** puede habilitar la confirmación (decidido por el usuario), y solo si contiene una afirmación (sí, si, dale, confirmo, hazlo, ok, de acuerdo; normalizada sin tildes ni mayúsculas) **o** los 4 dígitos del código (en cifras o en palabras).
  - Si los contiene: se habilita un único `mcp_confirmar` con ese código durante ese turno.
  - Si no: lo pendiente se descarta en la guardia (cualquier `mcp_confirmar` posterior se rechaza), aunque el MCP siga teniendo el código vivo hasta que expire.
- Si llega una llamada a `mcp_confirmar` sin habilitación, no se reenvía al MCP. Al LLM se le responde "Rechazado: el usuario no confirmó".
- Solo cuenta como frase del usuario un texto de ASR no vacío. No cuentan la palabra de activación, los resultados vacíos o de ruido, ni las interrupciones (mi decisión). El builder engancha la guardia en los puntos donde se agregan mensajes de usuario (connection.go y connection_handlemsg.go).
- Es genérico: aplica a cualquier tool MCP llamado `confirmar` o `confirm`.
- Lleva tests unitarios en el fork.

### Prompt

En `server/xiaozhi/config.template.yaml` se agrega un bloque breve que reemplaza las reglas de procedimiento de secretaria.md, ahora en código:

- Cuándo usar cada herramienta.
- Lista de proyectos conocidos (joyeria, casa, mascotas, mesa-libre, bot-opticas, bot-ofertas, scraper-bancos, bot-secretaria, otros, tareas).
- Tareas en Tasks y eventos con hora en el calendario.
- Para crear un evento, si el usuario no dijo el calendario, preguntarle en cuál (no adivinar).
- Una herramienta por turno (el fork solo ejecuta la primera llamada).
- Preguntar el contexto de la bitácora si no está claro. No anotar en la bitácora sin que el usuario lo pida, salvo al completar una tarea, que lo hace la herramienta.
- Para corregir algo, usar `bitacora_corregir`. Editar o borrar archivos y notas no es posible por voz: decirlo.
- Ante PENDIENTE, preguntar y esperar. Nunca llamar `confirmar` sin un sí explícito del usuario.
- Ante un aviso de duplicado, preguntar y usar `forzar` solo si el usuario insiste.

**Ojo:** ese archivo tiene un cambio sin commitear del usuario (el nombre "weita"). Se edita encima, sin pisarlo.

### Respuestas para voz

- Texto plano breve, sin XML, iCal ni JSON crudo.
- Listados con un máximo de 10 ítems y "y N más".
- Contenido (bitácora de un día, nota, archivo) truncado a unos **2000 caracteres**, con aviso. Como no hay herramientas de sobrescritura, truncar ya no arriesga pérdida de datos.
- Fechas en hora local y en lenguaje natural corto ("mañana 15:00", "vie 21/11").
- Errores en una línea en español. Nunca stack traces ni URLs con credenciales.

### Cliente Nextcloud (port del TS)

- `net/http` con basic auth, timeout de 15 s y reintento ante 423.
- Rutas:
  - WebDAV: `/remote.php/dav/files/{user}{path}`
  - CalDAV: `/remote.php/dav/calendars/{user}/{cal}/`
  - Notes: `/apps/notes/api/v1/notes` con `OCS-APIRequest: true`
- Calendarios:
  - Los de tareas son los que soportan VTODO; los de eventos, los que soportan VEVENT. Se saltan inbox, outbox, trashbin y contact_birthdays, como el TS.
  - El id del calendario nuevo es el slug del nombre.
- iCal a mano, como `ical.ts`:
  - `toIcalUtc`, `toIcalDate`, `extractIcalField` (con unfolding y params)
  - `setVtodoField`, `makeVtodo`
  - VEVENT con all_day (VALUE=DATE, fin = día siguiente) y RRULE
  - `RELATED-TO;RELTYPE=PARENT`
- PROPFIND y REPORT con `encoding/xml`.
- UIDs: `<unixnano>@xiaozhi-nextcloud`.
- Zona horaria: `import _ "time/tzdata"` para no depender de tzdata en alpine.
- `resumen_del_dia` y `tareas_pendientes` sin proyecto consultan los calendarios en paralelo, con un tope de 4.

### Empaquetado

- Módulo Go nuevo en `server/nextcloud-mcp/`, independiente de `xiaozhi-server-go`.
- `server/Dockerfile`: etapa builder con `CGO_ENABLED=0 go build -o /out/nextcloud-mcp`, y el binario se copia a `/app/nextcloud-mcp`.
- `/app/.mcp_server_settings.json` se versiona en `server/xiaozhi/`:
  ```json
  {"mcpServers": {"nextcloud": {"command": "/app/nextcloud-mcp", "args": [], "env": {}}}}
  ```
- Credenciales: el subproceso las hereda del entorno del contenedor. Creo que mcp-go v0.29 hace `cmd.Env = append(os.Environ(), env...)`; **el builder debe verificarlo**.
  - Si no las hereda, `entrypoint.sh` renderiza el JSON con `envsubst` y `chmod 600`.
- Si faltan `NEXTCLOUD_*`, el MCP avisa en stderr y sigue respondiendo el handshake MCP con cero tools (no sale: un hijo muerto haría esperar a xiaozhi ~30 s en cada conexión), con la voz intacta. El entrypoint no aborta.
- `docker-compose.yml` pasa `NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD` y `TZ` al servicio xiaozhi.
- Nunca se loguea a stdout, que es el canal MCP. Logs a stderr, sin credenciales.

### Pruebas

- Fixtures XML/JSON escritos a mano según la documentación de WebDAV, CalDAV y Notes y la forma que esperan los parsers del TS.
- Unitarias:
  - iCal
  - parsers XML
  - parser de bitácora: formato propio, correcciones, archivos editados a mano
  - normalización e identificación por título
  - store de confirmaciones: expira, uso único, ETag cambiado
  - filtro de `resumen_del_dia`
- Integración contra un `httptest.Server` que simula WebDAV (con ETag e If-Match), CalDAV y Notes, para cada herramienta. Incluye el 412 concurrente.
- Fork: tests unitarios de la guardia.
- E2E manual por voz (usuario):
  - anotar en la bitácora, corregir, leer el día y la semana
  - crear una tarea duplicada y luego completarla, y verificar que queda en la bitácora
  - borrar una tarea con confirmación, y verificar que se rechaza la auto-confirmación
  - "¿qué tengo hoy?"
  - guardar en una nota existente
- Verificar al inicio que DeepSeek, vía LiteLLM, hace tool calling bien.

## Riesgos conocidos

- **Tool calling de DeepSeek por LiteLLM:** se valida primero. Si falla, se cambia el alias a otro modelo de DeepSeek sin razonamiento (mi decisión; sigue siendo DeepSeek).
- **Latencia:** `bitacora_leer` de un mes o `resumen_del_dia` con unos 10 calendarios hacen muchas peticiones. El paralelismo acotado y el tope de 31 días lo mitigan.
- **Archivos editados a mano o por el bot de Telegram:** el parser debe ser tolerante. Las correcciones del bot de Telegram (que edita en sitio) no se marcan, y está bien.
- **Identificación por título con transcripción imperfecta:** la búsqueda "contiene" más los candidatos cuando no hay coincidencia lo mitigan. Ante ambigüedad nunca se actúa.

## Lo que queda al builder

- Estructura interna de paquetes, nombres y librería de testing.
- Verificar la herencia de env en mcp-go stdio y elegir entre herencia y envsubst.
- Verificar si la API de Notes respeta `If-Match`.
- Redacción exacta de las respuestas, dentro de los límites de arriba.
- Cómo descubrir los calendarios de eventos y de tareas (componentes soportados en PROPFIND).
