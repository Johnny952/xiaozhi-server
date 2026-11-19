# nextcloud-mcp

Servidor MCP en Go (stdio) que expone agenda, tareas, bitácora y notas de Nextcloud al asistente de voz. Va empaquetado en la imagen del servidor de voz (`server/xiaozhi-server-go`), que lo lanza como subproceso. El único destino de red es `NEXTCLOUD_URL`. stdout es el canal MCP; los logs van a stderr, sin credenciales.

## Variables de entorno

| Variable | Descripción |
|---|---|
| `NEXTCLOUD_URL` | URL base de Nextcloud (obligatoria) |
| `NEXTCLOUD_USER` | Usuario (obligatoria) |
| `NEXTCLOUD_PASSWORD` | App password de Nextcloud (obligatoria) |
| `TZ` | Zona horaria, p. ej. `America/Santiago` (obligatoria; se embebe `time/tzdata`) |

Variables opcionales (MCP y scheduler):

| Variable | Descripción |
|---|---|
| `RESUMEN_HORA` | Hora (HH:MM) del resumen diario; vacía = sin resumen |
| `RECORDATORIOS_STATE` | Archivo de estado compartido (por defecto `/data/recordatorios-state.json`) |

Si falta alguna, el binario avisa en stderr y sigue respondiendo el protocolo MCP **sin ninguna tool** (no sale, para no retrasar la conexión de los dispositivos); el servidor de voz sigue funcionando sin estas tools.

## Subcomando `scheduler`

`nextcloud-mcp scheduler` corre el proceso de avisos (servicio `recordatorios` en compose, misma imagen que `xiaozhi`). Revisa los recordatorios de Nextcloud Tasks y empuja alerts al `mqtt-gateway` por `POST /push`; además dispara el resumen diario. Comparte con el MCP por stdio el archivo de estado (volumen `recordatorios-state` en `/data`), así no repite avisos tras reiniciar.

| Variable | Descripción |
|---|---|
| `NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD` | Obligatorias |
| `PUSH_URL` | Endpoint interno del gateway (`http://mqtt-gateway:8007/push`) |
| `PUSH_TOKEN` | Token bearer del gateway |
| `RECORDATORIOS_MAC` | MAC del parlante destino; vacía = todos los conectados |
| `RESUMEN_HORA` | HH:MM del resumen diario; vacía = sin resumen |
| `RECORDATORIOS_STATE` | Ruta del estado (por defecto `/data/recordatorios-state.json`) |
| `TZ` | `America/Santiago` |

## Tool oculta

`recordatorios_pendientes` es de uso interno: la consume el flujo del botón del alert para leer los avisos pendientes, y no está pensada para pedirla por voz.

## Tests

Desde `server/nextcloud-mcp`:

```
docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/gomod -v "$HOME/.cache/nc-mcp-gomod":/gomod -v "$PWD":/src -w /src golang:1.24-alpine go test ./...
```

## Herramientas

El servidor de voz las expone al LLM con el prefijo `mcp_` (p. ej. `mcp_agenda`).

- `agenda`: lee los eventos de todos los calendarios en un rango de hasta 31 días; por defecto hoy.
- `evento_crear`: crea un evento; avisa si se cruza con otro y no duplica el mismo día salvo con `forzar`.
- `evento_cancelar`: pide borrar un evento; devuelve una pregunta de confirmación y un código.
- `resumen_del_dia`: eventos del día y tareas vencidas, que vencen hoy o de prioridad alta.
- `tareas_pendientes`: lista las tareas no completadas de un proyecto o de todos.
- `tarea_crear`: crea una tarea en un proyecto; no duplica pendientes con el mismo título salvo con `forzar`. El parámetro `crear_proyecto` crea el proyecto si no existe.
- `tarea_completar`: marca una tarea como hecha y lo anota en la bitácora del proyecto.
- `tarea_anotar`: agrega un texto con fecha y hora al final de la descripción de una tarea.
- `tarea_cambiar`: pide cambiar título, vencimiento o prioridad; devuelve pregunta de confirmación y código.
- `tarea_borrar`: pide borrar una tarea; devuelve pregunta de confirmación y código.
- `bitacora_anotar`: anota una entrada en la bitácora de hoy de un contexto; solo agrega.
- `bitacora_corregir`: corrige una entrada agregando una corrección en el día original; el texto original se conserva.
- `bitacora_leer`: lee un día completo o un resumen de un rango de hasta 31 días; sin contexto lee todas.
- `nota_guardar`: guarda texto en una nota; si existe una con ese título la agrega al final, si no la crea.
- `notas_buscar`: busca notas por título o contenido; una coincidencia la lee, varias las lista.
- `estado_proyecto_anotar`: agrega una actualización con fecha a `Proyectos/<proyecto>/estado.md`; solo agrega.
- `estado_proyecto_leer`: lee el archivo de estado de un proyecto.
- `archivo_leer`: lee un archivo de texto por ruta o por nombre; solo lectura.
- `confirmar`: ejecuta una acción pendiente (borrar o cambiar) con el código de 4 dígitos, solo si el usuario dijo que sí.
