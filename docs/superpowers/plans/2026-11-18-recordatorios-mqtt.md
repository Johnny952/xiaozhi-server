# Recordatorios por MQTT — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que el parlante avise solo (alert en pantalla) cuando vence un recordatorio o está listo el resumen de la mañana, y que al apretar el botón lea lo pendiente. Etapa 1: sin tocar el firmware.

**Architecture:**
- El dispositivo pasa de WebSocket a MQTT+UDP vía `xiaozhi-mqtt-gateway` (Node, vendorizado). El gateway puentea al ws del fork y expone un `POST /push` interno.
- El OTA del fork entrega el bloque `mqtt` solo si `MQTTUDP.Enabled`. Apagarlo es el rollback.
- Los recordatorios son VTODO con VALARM en la lista "Recordatorios" de Nextcloud Tasks. `nextcloud-mcp` suma tools para la voz y un subcomando `scheduler`, que corre como servicio aparte y empuja los alerts.
- Al conectar (hello), el fork llama a la tool oculta `recordatorios_pendientes` y habla el texto.

**Tech Stack:** Node 20 (gateway, `node:test`), Go 1.24 (`nextcloud-mcp`, fork), Docker Compose, CalDAV de Nextcloud.

**Spec:** `docs/superpowers/specs/2026-11-18-recordatorios-mqtt-design.md`. Es la fuente de verdad del texto hablado, los payloads y los límites. Este plan cita la línea de la spec (l78 gateway, l92 OTA, l103 tools, l134 resumen, l154 scheduler, l169 botón, l189 compose, l197 Milestone 0, l206 pruebas).

## Global Constraints

- **Milestone 0 es una compuerta.** Las tareas 4–11 no empiezan hasta que el usuario confirme el checklist de la tarea 3. Si fallan los pasos 2 (conversación por UDP) o 3 (alert manual), detenerse y consultar al usuario (spec l197).
- No se toca el firmware. El dispositivo usa el fw stock 2.4.2 y recibe la config por OTA.
- `POST /push` y los puertos HTTP internos no se publican al host. Solo se publican `${LAN_IP}:1883/tcp` y `${LAN_IP}:8884/udp`.
- Credenciales solo por env: `MQTT_USERNAME`, `MQTT_PASSWORD`, `PUSH_TOKEN`, `NEXTCLOUD_*`. Nunca se loguean ni van en respuestas.
- Texto hablado y de pantalla en español. El `message` del alert tiene como máximo ~40 caracteres.
- Zona horaria: `TZ` = America/Santiago, con `time/tzdata` embebido.
- Tests de `nextcloud-mcp`, desde `server/nextcloud-mcp`:
  `docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/gomod -v "$HOME/.cache/nc-mcp-gomod":/gomod -v "$PWD":/src -w /src golang:1.24-alpine go test ./...`
  (abajo, `GOTEST`).
- Tests del fork, desde `server/xiaozhi-server-go`:
  `docker run --rm -v "$PWD":/src -w /src golang:1.24-alpine sh -c 'apk add -q gcc musl-dev pkgconf opus-dev opusfile-dev && go test ./src/httpsvr/ota/ ./src/configs/ ./src/core/... -run <Patrón> -v'`
  (abajo, `FORKTEST <Patrón>`).
- Tests del gateway, desde `server/mqtt-gateway`:
  `docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -v "$PWD":/src -w /src node:20-alpine sh -c 'npm ci --silent && node --test'`
  (abajo, `NODETEST`).
- No hay Go ni Node local y no puede quedar ningún contenedor corriendo: siempre `docker run --rm`. Excepción: `docker compose up` del stack en las tareas 3 y 11, que es el despliegue real.
- Seguridad del repo:
  - Nunca leer ni hacer `cat` de `server/.env`, de otros `.env` ni de `.mk`. Solo se edita `server/.env.example`. Al usuario se le indica qué variables agregar a su `.env`.
  - Escanear el diff en busca de secretos antes de cualquier push. No hacer push ni merge sin preguntar.
- Commits con este trailer:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_013Xj2arizKA9o5MSF2MZLMQ
  ```
- Después de cada tarea, correr `graphify update .` desde `/home/johnny/Escritorio/proyectos/music-bot`.

## Review Focus

1. **El scheduler cae o reinicia a mitad del día.** Lo esperable es que no repita avisos ya notificados (marca `X-WEITA-NOTIFIED` en Nextcloud) ni el resumen (`resumen_enviado` en el archivo de estado). → Test `TestSchedulerRestartNoDuplicates` (Tarea 9).
2. **El usuario edita la tarea desde el celular entre la lectura y la marca.** El PUT con etag da 412: no se pisa la edición y se reintenta en el ciclo siguiente. → Test `TestSchedulerEtagConflictRetries` (Tarea 9).
3. **El MCP no está listo cuando llega el hello, o la tool falla.** La conexión sigue normal, sin hablar nada y sin error en el diálogo. → Test `TestHelloPendientesToolErrorIsSilent` (Tarea 10).
4. **Un recordatorio "para hoy" sin hora** ("recuérdame mañana pagar la luz"). Se rechaza pidiendo la hora; no se crea un VTODO con DUE de fecha sola que el scheduler nunca dispararía. → Test `TestRecordatorioCrearSinHoraPideHora` (Tarea 6).
5. **MAC con mayúsculas o `_` en `RECORDATORIOS_MAC`.** El gateway la normaliza y encuentra la conexión. → Test `push normaliza MAC con guiones bajos y mayúsculas` (Tarea 1).

---

## Milestone 0

### Task 1: Gateway vendorizado con `POST /push`

**Files:**
- Create: `server/mqtt-gateway/` (copia del upstream sin `.git`), `server/mqtt-gateway/UPSTREAM.md`, `server/mqtt-gateway/push.js`, `server/mqtt-gateway/test/push.test.js`, `server/mqtt-gateway/Dockerfile`, `server/mqtt-gateway/config/mqtt.json`
- Modify: el entrypoint del upstream (`app.js` o el que declare `package.json`) para validar credenciales y montar el push server

**Interfaces:**
- Produces: HTTP `POST http://mqtt-gateway:8007/push`, header `Authorization: Bearer ${PUSH_TOKEN}`, body `{"mac"?: string, "payload": object}` → 200 `{"sent":n}` | 400 | 401 | 404. Consumido por la Tarea 9 (`HTTPNotifier`) y por el curl de la Tarea 3.
- Produces: `createPushServer({ token, listConnections }) -> http.Server`, donde `listConnections()` devuelve `[{ mac: string, sendMqttMessage(str) }]`, y `normalizeMac(s) -> string`, ambos en `push.js`.

- [ ] **Step 1: Vendorizar.** `git clone --depth 1 https://github.com/78/xiaozhi-mqtt-gateway` en el scratchpad y copiar el árbol sin `.git` a `server/mqtt-gateway/`. En `UPSTREAM.md` anotar la URL, el commit y la fecha. Leer el entrypoint y anotar en `UPSTREAM.md` dónde se guardan las conexiones, cuál es su campo de MAC/deviceId y dónde está `validateMqttCredentials`.

- [ ] **Step 2: Escribir el test que falla** (`test/push.test.js`, con `node:test` y un `listConnections` falso):
  - `rechaza sin token` → 401.
  - `rechaza token incorrecto` → 401.
  - `400 sin payload o con JSON inválido`.
  - `push normaliza MAC con guiones bajos y mayúsculas`: `mac:"AA_BB_CC_DD_EE_FF"` llega a la conexión `aa:bb:cc:dd:ee:ff` → 200 `{"sent":1}`, y `sendMqttMessage` recibe `JSON.stringify(payload)`.
  - `404 si la mac no está conectada`.
  - `sin mac envía a todas` → con 2 conexiones devuelve `{"sent":2}`.
  - `sin mac y nadie conectado` → 404.

- [ ] **Step 3: Correr** `NODETEST`. Expected: FAIL (`push.js` no existe).

- [ ] **Step 4: Implementar `push.js`.** Usa `http` de la stdlib, sin dependencias nuevas. Compara el token con `crypto.timingSafeEqual`. `normalizeMac` reemplaza `_` por `:` y pasa a minúsculas. Si el token está vacío, el server no arranca y lo loguea.

- [ ] **Step 5: Correr** `NODETEST`. Expected: PASS.

- [ ] **Step 6: Integrar en el entrypoint.**
  - `validateMqttCredentials` acepta solo `MQTT_USERNAME`/`MQTT_PASSWORD` de env; si faltan, rechaza todo.
  - Arrancar `createPushServer` en `PUSH_PORT` (default 8007) con el `listConnections` real.
  - `config/mqtt.json`: `production.chat_servers ["ws://xiaozhi:8000/xiaozhi/v1/"]`.
  - Si el ws del fork exige auth, el bridge manda los headers `device-id` y `client-id` (spec l78).

- [ ] **Step 7: Dockerfile.** `node:20-alpine`, `npm ci --omit=dev`, `EXPOSE 1883 8884/udp`. Variables: `MQTT_PORT=1883`, `UDP_PORT=8884`, `PUBLIC_IP`, `PUSH_TOKEN`, `PUSH_PORT=8007`. Verificar con `docker build -t mqtt-gateway:dev server/mqtt-gateway` (sin run).

- [ ] **Step 8: Commit** `feat(mqtt-gateway): vendorizar gateway y agregar POST /push`.

### Task 2: OTA entrega `mqtt` solo con `MQTTUDP.Enabled`

**Files:**
- Modify: `server/xiaozhi-server-go/src/configs/config.go:38-45` (struct `MQTTUDP.MQTT`), `config.go:303` (`CheckAndModifyConfig`)
- Modify: `server/xiaozhi-server-go/src/httpsvr/ota/handler.go:25-37` (`OtaFirmwareResponse`), `handler.go:~257-265` (armado de la respuesta)
- Test: `src/httpsvr/ota/ota_mqtt_test.go` (nuevo), `src/configs/config_mqtt_test.go` (nuevo)

**Interfaces:**
- Produces: campos `MQTTUDP.MQTT.Username`, `MQTTUDP.MQTT.Password string` (yaml `username`, `password`).
- Produces: `type MQTTInfo struct { Endpoint, ClientID, Username, Password, PublishTopic string; SubscribeTopic *string }`, con tags json `endpoint`, `client_id`, `username`, `password`, `publish_topic`, `subscribe_topic`. Va en `OtaFirmwareResponse.MQTT *MQTTInfo \`json:"mqtt,omitempty"\``.
- Env leídas en `CheckAndModifyConfig`: `MQTTUDP_ENABLED` ("true"/"false"; vacío = no tocar), `MQTT_USERNAME`, `MQTT_PASSWORD`.

- [ ] **Step 1: Escribir los tests que fallan.** Reusar `setupPrivacyTest` y `postOTA` de `ota_privacy_test.go`.
  - `TestOTAMQTTWhenEnabled`: con `Enabled=true`, `IP="192.168.1.10"`, `Port=1883`, `Username="u"`, `Password="p"`, POST con device `aa:bb:cc:dd:ee:05` y header `client-id: cid`. Assert: `mqtt.endpoint=="192.168.1.10:1883"`, `client_id=="CGID_test@@@aa_bb_cc_dd_ee_05@@@cid"`, `username=="u"`, `password=="p"`, `publish_topic=="device-server"`, `subscribe_topic` presente y `null`; `websocket.url` sigue presente.
  - `TestOTANoActivationNoMQTT` (existente) debe seguir verde con `Enabled=false`.
  - `TestCheckAndModifyConfigMQTTEnv`: `MQTTUDP_ENABLED=true`, `MQTT_USERNAME=u`, `MQTT_PASSWORD=p` → se reflejan. Con `MQTTUDP_ENABLED=""` se respeta el yaml.

- [ ] **Step 2: Correr** `FORKTEST 'OTA|CheckAndModifyConfigMQTT'`. Expected: FAIL de compilación (campos inexistentes).

- [ ] **Step 3: Implementar.** Agregar los campos y el override en `CheckAndModifyConfig`, usando `os.Getenv` como las demás env. En el handler, llenar `resp.MQTT` solo si `cfg.MQTTUDP.Enabled`, usando el `client_id` ya armado en la línea 251. Si existe un default en `config_default_init.go` o un espejo en `webapi/system_config.go` para `MQTTUDP.MQTT`, agregar los campos ahí también; `Password` nunca se devuelve por la webapi.

- [ ] **Step 4: Correr** `FORKTEST 'OTA|CheckAndModifyConfigMQTT'`. Expected: PASS, incluido `TestOTANoActivationNoMQTT`.

- [ ] **Step 5: Commit** `feat(ota): entregar config mqtt cuando MQTTUDP está habilitado`.

### Task 3: Compose del gateway y compuerta manual

**Files:**
- Modify: `server/docker-compose.yml` (servicio `mqtt-gateway`; env del servicio `xiaozhi`)
- Modify: `server/.env.example`
- Modify: `server/docs/nextcloud-mcp.md` (sección "Milestone 0: MQTT")

- [ ] **Step 1: Compose.**
  - Servicio `mqtt-gateway`: `build: ./mqtt-gateway`, imagen `mqtt-gateway:dev`, red `xiaozhi-net`, `restart: unless-stopped`.
    - Puertos: `"${LAN_IP}:1883:1883/tcp"` y `"${LAN_IP}:8884:8884/udp"`. 8007 no se publica.
    - Env: `PUBLIC_IP=${LAN_IP}`, `MQTT_USERNAME`, `MQTT_PASSWORD`, `PUSH_TOKEN`.
    - `depends_on: [xiaozhi]`.
  - En `xiaozhi`, agregar env: `MQTTUDP_ENABLED`, `MQTT_USERNAME`, `MQTT_PASSWORD`. La IP y el puerto MQTT que entrega el OTA son `${LAN_IP}` y 1883: setearlos por yaml o env, como ya se hace con el websocket.
  - Verificar con `docker compose -f server/docker-compose.yml config -q` (no imprime secretos con `-q`).

- [ ] **Step 2: `.env.example`.** Agregar, con un comentario en inglés antes de cada una (estilo actual): `MQTT_USERNAME=`, `MQTT_PASSWORD=`, `PUSH_TOKEN=`, `MQTTUDP_ENABLED=false`, `RECORDATORIOS_MAC=`, `RESUMEN_HORA=07:30`.

- [ ] **Step 3: Checklist en `server/docs/nextcloud-mcp.md`** (spec l197):
  1. Agregar las variables al `.env` (lo hace el usuario), con `MQTTUDP_ENABLED=true`. `docker compose up -d --build mqtt-gateway xiaozhi`.
  2. Reiniciar el parlante. En los logs del gateway aparece la conexión MQTT y UDP. Tener una conversación normal.
  3. Mandar un alert manual desde dentro de la red docker:
     `docker run --rm --network server_xiaozhi-net curlimages/curl -s -X POST http://mqtt-gateway:8007/push -H "Authorization: Bearer $PUSH_TOKEN" -H 'Content-Type: application/json' -d '{"payload":{"type":"alert","status":"Prueba","message":"Hola desde el server","emotion":"happy"}}'`
     Expected: `{"sent":1}` y el alert en pantalla. (El nombre real de la red sale de `docker network ls`.)
  4. Rollback: `MQTTUDP_ENABLED=false`, recrear xiaozhi, reiniciar el parlante. Debe volver a WebSocket. Si el fw se queda pegado a MQTT por la NVS, el rollback es dejar el gateway levantado; anotar el resultado.

- [ ] **Step 4: Commit** `feat(compose): servicio mqtt-gateway y checklist del Milestone 0`.

- [ ] **Step 5: COMPUERTA.** Pedirle al usuario que corra el checklist y reporte los 4 pasos. Si fallan los pasos 2 o 3, detenerse y consultar. No seguir a la Tarea 4 sin su confirmación.

---

## Recordatorios en nextcloud-mcp

### Task 4: VALARM en `ical.Todo`

**Files:**
- Modify: `server/nextcloud-mcp/ical/ical.go` (`Todo` y `MakeVTodo`, l241)
- Test: `server/nextcloud-mcp/ical/ical_test.go`

**Interfaces:**
- Produces: `Todo.Alarm bool`. Con `Alarm` y `Due != nil`, `MakeVTodo` emite dentro del VTODO:
  ```
  BEGIN:VALARM
  ACTION:DISPLAY
  TRIGGER;RELATED=END:PT0S
  DESCRIPTION:<Summary escapado>
  END:VALARM
  ```
- `ParseVTodo` pone `Alarm=true` si existe el bloque. `Field` y `SetVTodoField` no deben confundir el `DESCRIPTION` del VALARM con el del VTODO.

- [ ] **Step 1: Tests que fallan.**
  - `TestMakeVTodoAlarm`: el bloque exacto aparece una vez, antes de `END:VTODO`.
  - `TestMakeVTodoNoAlarmByDefault`: sin `BEGIN:VALARM`.
  - `TestParseVTodoAlarmRoundTrip`: `Alarm` vuelve en true y `Description` es la del VTODO, no la del VALARM.
  - `TestSetVTodoFieldIgnoresAlarm`: `SetVTodoField(ics,"DESCRIPTION","x")` cambia solo la del VTODO.
- [ ] **Step 2:** `GOTEST`. Expected: FAIL.
- [ ] **Step 3: Implementar.** Si `Field`/`SetVTodoField` recorren líneas, saltar las que están entre `BEGIN:VALARM` y `END:VALARM`.
- [ ] **Step 4:** `GOTEST`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(ical): VALARM en VTODO`.

### Task 5: CANCELLED cuenta como cerrada y "Recordatorios" sale de las tareas

**Files:**
- Modify: `server/nextcloud-mcp/tools/agenda.go` (`todoCalendars` l127, `isCompleted` l569, y sus llamadores: tareas_pendientes, tarea_completar, tarea_cambiar, tarea_borrar y `resumenDelDia`)
- Test: `server/nextcloud-mcp/tools/agenda_test.go`

**Interfaces:**
- Produces: `const RecordatoriosList = "Recordatorios"`; `todoCalendars(cals []nc.Calendar, includeRecordatorios bool) []nc.Calendar`. Las tools de tareas pasan `false`; las Tareas 6–8 pasan `true` o filtran por nombre.
- Produces: `isCompleted(t ical.Todo) bool`, que es true con `COMPLETED` o `CANCELLED`.

- [ ] **Step 1: Tests que fallan.**
  - `TestTareasPendientesExcluyeRecordatorios`: nctest con listas "Personal" y "Recordatorios", una tarea en cada una. La respuesta nombra solo la de Personal.
  - `TestTareaCompletarNoVeRecordatorios`: buscar por el texto del recordatorio da "no encontré".
  - `TestTareasPendientesDescartaCancelled`: un VTODO `STATUS:CANCELLED` no aparece.
- [ ] **Step 2:** `GOTEST`. Expected: FAIL.
- [ ] **Step 3: Implementar.** Comparar el nombre con `text.Normalize`.
- [ ] **Step 4:** `GOTEST`. Expected: PASS, incluidos los tests previos de tareas.
- [ ] **Step 5: Commit** `feat(tools): separar lista Recordatorios y tratar CANCELLED como cerrada`.

### Task 6: Tools `recordatorio_crear`, `recordatorio_listar`, `recordatorio_cancelar`

**Files:**
- Create: `server/nextcloud-mcp/tools/recordatorios.go`, `tools/recordatorios_test.go`
- Modify: `tools/registry.go` (`All(d)` suma `recordatorioTools(d)...`)

**Interfaces:**
- Consumes: `ical.Todo.Alarm` (T4), `todoCalendars(cals, true)` y `isCompleted` (T5), `text.ParseWhen`, `text.Human`, `text.Find`, `text.List`, `nc.MkCalendarTodos`, `nc.PutObject`.
- Produces: `recordatorioTools(d Deps) []Tool`; `ensureRecordatorios(ctx, d) (nc.Calendar, error)` (busca la lista o la crea con `MkCalendarTodos`); `openRecordatorios(ctx, d) ([]todoObj, error)`, que devuelve los VTODO no cerrados de la lista con su href, su etag y su `ical.Todo` (`todoObj` es el tipo que ya use `fetchTodos`, o uno nuevo con esos 3 campos). Lo reusan las Tareas 7 y 8.

- [ ] **Step 1: Tests que fallan** (nctest, `Now` fijo el martes 2026-11-17 09:00 America/Santiago):
  - `TestRecordatorioCrearCreaListaYAlarm`: sin la lista "Recordatorios", `crear(texto:"llamar al proveedor", cuando:"2026-11-17T15:00")` → hace MKCALENDAR, el PUT tiene `DUE` 15:00 local, VALARM y `SUMMARY:llamar al proveedor`. La respuesta contiene `text.Human` de la fecha ("hoy a las 15").
  - `TestRecordatorioCrearSinHoraPideHora`: `cuando:"2026-11-18"` → error en una línea que pide la hora; no hay PUT.
  - `TestRecordatorioCrearEnElPasado`: `cuando` anterior a `Now` → error, no crea.
  - `TestRecordatorioListarOrdenadoFuturos`: con 3 recordatorios (uno pasado, uno cancelado y dos futuros desordenados) lista solo los futuros, por DUE.
  - `TestRecordatorioCancelarUnico`: queda `STATUS:CANCELLED` (PUT con etag) y no pide confirmación.
  - `TestRecordatorioCancelarAmbiguo`: con 2 coincidencias las lista y no hace PUT.
  - `TestRecordatorioCancelarPorUID`.
- [ ] **Step 2:** `GOTEST`. Expected: FAIL.
- [ ] **Step 3: Implementar.** Las descripciones de las tools, en español, dicen cuándo usarlas ("cuando el usuario pide que le recuerdes algo a una hora"). Los textos de respuesta, según la spec l103-133.
- [ ] **Step 4:** `GOTEST`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(tools): recordatorios crear, listar y cancelar`.

### Task 7: Resumen de la mañana

**Files:**
- Create: `server/nextcloud-mcp/tools/resumen.go`, `tools/resumen_test.go`
- Modify: `tools/agenda.go` (`resumenDelDia` l504 delega en `ResumenManana`; se borra la lógica vieja)

**Interfaces:**
- Consumes: `fetchEvents`, `fetchTodos`, `todoCalendars`, `isCompleted`, `openRecordatorios` (T6).
- Produces: `ResumenManana(ctx context.Context, d Deps) (string, error)`. Lo usan `resumen_del_dia` y la Tarea 8.

- [ ] **Step 1: Tests que fallan** (spec l134-153; `Now` = martes 18 de noviembre de 2026, 07:30):
  - `TestResumenEjemploSpec`: datos del ejemplo de la spec → string exacto: `"Hoy martes 18: a las 10, dentista; a las 15, recordatorio de llamar al proveedor. Vence hoy: pagar la luz. Prioridad alta: preparar presupuesto. Quedaron atrasadas: enviar factura, de ayer. ¿La reagendo o la cancelo?"`
  - `TestResumenNadaAgendado` → `"Hoy no tienes nada agendado."`
  - `TestResumenOmiteSeccionesVacias`: solo hay atrasadas → no aparecen "Vence hoy" ni "Prioridad alta".
  - `TestResumenTodoElDiaPrimero`.
  - `TestResumenLimites`: 7 eventos → 5 + "y 2 más"; 6 atrasadas → 5 + "y 1 más"; 4 de prioridad alta → 3.
  - `TestResumenAtrasadasPlural`: 2 atrasadas → "¿Las reagendo o las cancelo?"; fechas relativas "de ayer", "del lunes" (hace 2–6 días), "del 3 de noviembre" (más de 6 días).
  - `TestResumenPrioridadAlta`: entra con prioridad 1–4 y sin DUE o con DUE futuro; no entra con prioridad 5, ni si vence hoy (ya está en "Vence hoy").
  - `TestResumenListaSoloSiDuplicado`: dos tareas "comprar pan" en listas distintas → "comprar pan [Casa]" y "comprar pan [Trabajo]".
  - `TestResumenExcluyeCompletadosYCancelados`.
- [ ] **Step 2:** `GOTEST`. Expected: FAIL.
- [ ] **Step 3: Implementar.** Pedir eventos y tareas con `parallel`. Cada sección es una función pura sobre los datos ya traídos, para poder testearla sin red.
- [ ] **Step 4:** `GOTEST`. Expected: PASS, y los tests previos de `resumen_del_dia` actualizados al formato nuevo.
- [ ] **Step 5: Commit** `feat(tools): resumen de la mañana`.

### Task 8: Archivo de estado y tool oculta `recordatorios_pendientes`

**Files:**
- Create: `server/nextcloud-mcp/state/state.go`, `state/state_test.go`
- Modify: `tools/recordatorios.go`, `tools/recordatorios_test.go`, `tools/registry.go` (`Deps` suma `State *state.Store` y `ResumenHora string`), `main.go` (arma el Store con `/data/recordatorios-state.json` y `RESUMEN_HORA`)

**Interfaces:**
- Produces: `state.Open(path string) *state.Store`; `(*Store).Get() (State, error)`; `(*Store).Set(field string, value string) error`, con `State{ResumenEnviado, ResumenLeido string}` (json `resumen_enviado`, `resumen_leido`, formato `YYYY-MM-DD`). `Set` toma `flock` exclusivo sobre `<dir>/recordatorios-state.lock`, relee el archivo, cambia solo ese campo y escribe atómico (tmp + rename). Un archivo inexistente vale como estado vacío.
- Produces: tool `recordatorios_pendientes` (sin args). Las marcas son las constantes `MarkNotified = "X-WEITA-NOTIFIED"` y `MarkSpoken = "X-WEITA-SPOKEN"`, que también usa la Tarea 9.

- [ ] **Step 1: Tests de state que fallan.**
  - `TestStateSetPreservaOtroCampo`: `Set("resumen_enviado",…)` y luego `Set("resumen_leido",…)` → ambos presentes.
  - `TestStateArchivoInexistente`.
  - `TestStateConcurrente`: 20 goroutines alternando los dos campos → JSON válido y los dos campos presentes al final.
- [ ] **Step 2: Tests de la tool que fallan** (spec l169-182):
  - `TestPendientesLeeVencidosYMarca`: un recordatorio con DUE hace 10 min y sin SPOKEN → texto con el summary; el PUT deja `X-WEITA-SPOKEN`, `STATUS:COMPLETED` y `COMPLETED:`.
  - `TestPendientesViejoTeniasPendiente`: DUE hace 30 h → empieza con "Tenías pendiente".
  - `TestPendientesNoRepite`: segunda llamada → texto vacío.
  - `TestPendientesIncluyeResumenUnaVez`: con `ResumenHora` "07:30", `Now` 08:00 y `resumen_leido` vacío → recordatorios primero, después el resumen, y `resumen_leido` = hoy. La segunda llamada no repite el resumen.
  - `TestPendientesResumenAntesDeHora`: a las 07:00 no incluye el resumen.
  - `TestPendientesResumenSinLosRecienLeidos`: un recordatorio de hoy leído en esta llamada no aparece de nuevo dentro del resumen.
  - `TestPendientesNadaDevuelveVacio`: el resultado es `""`, no un mensaje, para que el fork no hable.
- [ ] **Step 3:** `GOTEST`. Expected: FAIL.
- [ ] **Step 4: Implementar.** `flock` con `syscall.Flock` (linux, que es la imagen). Primero marcar y después llamar a `ResumenManana`, para que los recién leídos queden fuera.
- [ ] **Step 5:** `GOTEST`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(tools): recordatorios_pendientes y archivo de estado compartido`.

### Task 9: Subcomando `scheduler`

**Files:**
- Create: `server/nextcloud-mcp/scheduler/scheduler.go`, `scheduler/notifier.go`, `scheduler/scheduler_test.go`
- Modify: `server/nextcloud-mcp/main.go` (dispatch: `os.Args[1] == "scheduler"` → `scheduler.Run(...)`; si no, sigue `run(...)` como hoy), `main_test.go`

**Interfaces:**
- Consumes: `openRecordatorios` (exportado como `tools.OpenRecordatorios`), `state.Store`, `tools.MarkNotified`. No usa `ResumenManana`: el scheduler solo avisa que el resumen está listo, y lo arma el MCP al apretar el botón.
- Produces: `type Aviso struct{ Titulo, Texto string }`; `type Notifier interface{ Notify(ctx context.Context, a Aviso) error }`; `var ErrNotConnected = errors.New("dispositivo no conectado")` (que mapea el 404).
- Produces: `HTTPNotifier{URL, Token, MAC string; Client *http.Client}`, que envía `{"mac":MAC,"payload":{"type":"alert","status":Titulo,"message":Texto,"emotion":"thinking"}}`.
- Produces: `type Scheduler struct{ Deps tools.Deps; N Notifier; State *state.Store; ResumenHora string }`, con `(*Scheduler) Tick(ctx) error` (un ciclo) y `Run(env func(string) string, errw io.Writer) int` (loop de 60 s con `time.Ticker`, que sale limpio con SIGTERM).
- Env: `NEXTCLOUD_*`, `PUSH_URL`, `PUSH_TOKEN`, `RECORDATORIOS_MAC`, `RESUMEN_HORA`, `TZ`. Si falta alguna, sale con código ≠0 y un mensaje en stderr.

- [ ] **Step 1: Tests que fallan** (nctest + un `fakeNotifier` que registra avisos y devuelve un error configurable; spec l154-168):
  - `TestTickAvisaVencido`: DUE hace 1 min → 1 aviso `{Titulo:"Recordatorio", Texto:<summary recortado a 40>}` y PUT con `X-WEITA-NOTIFIED`.
  - `TestTickIgnoraYaNotificado`.
  - `TestTickIgnoraMasDe24h`.
  - `TestTickIgnoraFuturo`.
  - `TestTick404Reintenta`: el notifier devuelve `ErrNotConnected` → sin PUT; en el siguiente Tick, con el notifier OK, avisa y marca.
  - `TestTickVarios`: 3 vencidos → 1 aviso con Texto "Tienes 3 recordatorios" y 3 marcas.
  - `TestSchedulerEtagConflictRetries`: con `nctest.MutateBeforeNextPut`, el PUT da 412 → no se pisa la edición; el próximo Tick no vuelve a avisar si el aviso ya salió (el reintento es solo de la marca, relee y vuelve a intentar el PUT).
  - `TestTickResumenUnaVezPorDia`: `Now` 07:31 con `ResumenHora` 07:30 → aviso `{Titulo:"Buenos días", Texto:"Tienes tu resumen del día"}` y `resumen_enviado` = hoy; el segundo Tick no avisa.
  - `TestTickResumenReintentaHastaMedianoche`: con 404 a las 07:31 no escribe el estado; a las 12:00 con OK avisa; con 404 a las 23:58 tampoco escribe, y al día siguiente a las 00:01 no avisa (todavía no es `RESUMEN_HORA`).
  - `TestSchedulerRestartNoDuplicates`: un Tick con un Scheduler nuevo, sobre el mismo nctest y el mismo archivo de estado → 0 avisos.
  - `TestHTTPNotifier` (httptest): verifica el header Bearer y el body exacto; 404 → `ErrNotConnected`; 500 → otro error.
  - En `main_test.go`, `TestSchedulerFaltanEnv` → código ≠0.
- [ ] **Step 2:** `GOTEST`. Expected: FAIL.
- [ ] **Step 3: Implementar.** Para la marca NOTIFIED de `TestSchedulerEtagConflictRetries`, guardar en memoria los UIDs avisados pero no marcados, y reintentar solo la marca. Si el proceso reinicia en ese hueco, se acepta un aviso duplicado; dejarlo comentado.
- [ ] **Step 4:** `GOTEST`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(scheduler): avisos de recordatorios y del resumen diario`.

---

## Fork y despliegue

### Task 10: Hello lee pendientes y la tool queda oculta al LLM

**Files:**
- Modify: `server/xiaozhi-server-go/src/core/mcp/client.go:199` (`GetAvailableTools`)
- Modify: `server/xiaozhi-server-go/src/core/connection_handlemsg.go:138` (después de `h.sendHelloMessage()`)
- Test: `src/core/mcp/client_hidden_test.go`, `src/core/connection_hello_pendientes_test.go`

**Interfaces:**
- Consumes: `Manager.ExecuteTool(ctx, name, args)` (manager.go:409), `Client.IsReady()` (client.go:287), `h.SystemSpeak(text)` (connection.go:1041), `h.dialogueManager.Put(chat.Message{Role:"assistant", Content:text})`.
- Produces: `var hiddenTools = map[string]bool{"recordatorios_pendientes": true}` en `client.go`. `GetAvailableTools` las saltea; `HasTool` y `CallTool` las siguen aceptando.
- Produces: `func (h *ConnectionHandler) speakPendientes(ctx context.Context)`, que se lanza con `go` desde `handleHelloMessage`.

- [ ] **Step 1: Tests que fallan.**
  - `TestGetAvailableToolsOcultaPendientes`: un Client con tools `recordatorio_crear` y `recordatorios_pendientes` → solo `mcp_recordatorio_crear`. `HasTool("recordatorios_pendientes")` y `HasTool("mcp_recordatorios_pendientes")` dan true.
  - `TestHelloPendientesHabla`: con un ejecutor falso que devuelve "Tenías pendiente: pagar la luz." → `SystemSpeak` recibe ese texto y el diálogo suma un mensaje assistant. Para inyectar el ejecutor y el speaker, `speakPendientes` llama a dos campos función del handler (`pendientesExec`, `pendientesSpeak`) que por defecto apuntan a `mcpManager.ExecuteTool` y `SystemSpeak`.
  - `TestHelloPendientesVacioNoHabla`.
  - `TestHelloPendientesToolErrorIsSilent`: si el ejecutor da error o el MCP no queda listo en 2 s → no habla, no agrega al diálogo y solo deja un log.
  - Confirmar en el test qué nombre acepta `ExecuteTool`/`IsMCPTool` (crudo o con `mcp_`) y usar ese.
- [ ] **Step 2:** `FORKTEST 'GetAvailableToolsOculta|HelloPendientes'`. Expected: FAIL.
- [ ] **Step 3: Implementar.** Esperar a que esté listo con un poll cada 100 ms hasta 2 s, con un timeout total de 10 s para la tool. Nunca bloquear el hello.
- [ ] **Step 4:** `FORKTEST 'GetAvailableToolsOculta|HelloPendientes|ConfirmGuard'`. Expected: PASS (ConfirmGuard sigue verde).
- [ ] **Step 5: Commit** `feat(fork): leer recordatorios pendientes al conectar`.

### Task 11: Servicio `recordatorios`, volumen y checklist E2E

**Files:**
- Modify: `server/docker-compose.yml`, `server/.env.example` (si falta algo de la Tarea 3), `server/docs/nextcloud-mcp.md`, `server/nextcloud-mcp/README.md`

- [ ] **Step 1: Compose.**
  - Volumen `recordatorios-state`, montado en `/data` de `xiaozhi` y de `recordatorios`.
  - Servicio `recordatorios`: imagen `xiaozhi-server:dev`, `entrypoint ["/app/nextcloud-mcp","scheduler"]`, red `xiaozhi-net`, `restart: unless-stopped`.
    - Env: `NEXTCLOUD_*`, `PUSH_URL=http://mqtt-gateway:8007/push`, `PUSH_TOKEN`, `RECORDATORIOS_MAC`, `RESUMEN_HORA`, `TZ=America/Santiago`.
    - `depends_on: [mqtt-gateway]`.
  - `xiaozhi` también recibe `RESUMEN_HORA` (la necesita el MCP por stdio). Si `.mcp_server_settings.json` no hereda la env del proceso, pasarla en su bloque `env`.
  - Verificar: `docker compose -f server/docker-compose.yml config -q` y `docker build` de la imagen (sin run).
- [ ] **Step 2: Checklist E2E en `server/docs/nextcloud-mcp.md`** (spec l206-212):
  1. "Recuérdame en 3 minutos tomar agua" → aparece en Nextcloud Tasks, en la lista Recordatorios, con alarma.
  2. A los 3 min, alert en pantalla.
  3. Apretar el botón → lo lee; en Nextcloud queda completado.
  4. "¿Qué recordatorios tengo?" y "cancela el de tomar agua".
  5. Con `RESUMEN_HORA` 2 min adelante → alert "Buenos días"; botón → resumen; segunda vez → no lo repite.
  6. `docker compose restart recordatorios` → no repite avisos.
  7. "¿Qué tareas tengo?" no muestra recordatorios.
- [ ] **Step 3: README.** Documentar el subcomando `scheduler`, sus env y la tool oculta.
- [ ] **Step 4: Commit** `feat(compose): servicio recordatorios y checklist E2E`.
- [ ] **Step 5:** Pedirle al usuario que agregue las variables nuevas al `.env`, levante el stack y corra el checklist. No hacer push.
