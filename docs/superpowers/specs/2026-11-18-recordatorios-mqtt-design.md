# Recordatorios y avisos al parlante vía MQTT: diseño

Fecha: 2026-11-18. Estado: borrador (etapa 1), revisado por builder check, pendiente de aprobación.

Las decisiones marcadas **(decisión de Claude)** las tomé yo sin preguntar; el resto lo decidió el usuario o sale del código.

## Intención

Hoy el parlante solo habla cuando el usuario aprieta el botón. Queremos que el servidor pueda tomar la iniciativa, como lo hace el bot secretaria: alarmas, recordatorios creados por voz y un resumen diario de tareas. El usuario quiere decirle a Weita "recuérdame mañana a las 10 llamar al proveedor" y que, a esa hora, el parlante avise.

La etapa 1 se hace **sin tocar el firmware**: el servidor manda un `alert` por MQTT (sonido de vibración, emoción, texto en pantalla). El usuario aprieta el botón de escucha y Weita le lee lo pendiente. La etapa 2 (firmware propio que despierta solo y muestra un globo de idea) queda fuera, pero el diseño debe permitir cambiar `alert` por `wake` sin rehacer nada.

## Objetivos

1. Agregar el transporte MQTT+UDP (xiaozhi-mqtt-gateway) al stack, de modo que el servidor tenga un canal abierto hacia el dispositivo aunque esté en reposo.
2. Crear, listar y cancelar recordatorios por voz. Se guardan como VTODO con DUE en Nextcloud Tasks (lista "Recordatorios").
3. Un planificador que, al vencer un recordatorio, envía un `alert` al dispositivo.
4. Un aviso diario a una hora fija (resumen del día).
5. Al apretar el botón de escucha después de un aviso, Weita lee en voz alta los recordatorios vencidos no leídos y, si toca, el resumen del día.
6. Milestone 0: comprobar que el firmware de fábrica (esp32-muma-a 2.4.2) acepta MQTT y muestra el `alert`, antes de construir el resto.

## No-objetivos (fase posterior)

- **Etapa 2:** firmware propio con mensaje `{"type":"wake"}` que despierta el dispositivo y habla sin botón, y un globo de idea en pantalla. Se entrega por OTA.
- Alarma fuerte y repetitiva (necesita firmware).
- Recurrencia (RRULE): "todos los lunes".
- Posponer ("en 10 minutos más") como comando dedicado. Se puede hacer creando otro recordatorio.
- Varios dispositivos con agendas distintas.

## Anti-objetivos

- Que el dispositivo deje de funcionar para conversar. Si MQTT falla, la conversación normal tiene que seguir funcionando o poder volver a WebSocket.
- Que el mismo recordatorio suene una y otra vez, o que se lea dos veces.
- Que un recordatorio se pierda en silencio porque el dispositivo estaba apagado.
- Exponer el gateway o su endpoint de push fuera de la LAN.
- Duplicar el almacenamiento: los recordatorios viven en Nextcloud, no en una base nueva.

## Restricciones

- El dispositivo tiene firmware de fábrica; el USB-C solo carga. Cualquier cambio de firmware es por OTA (etapa 2).
- Stack actual en `server/docker-compose.yml`: xiaozhi (Go), speaches, litellm, litellm-db en la red `xiaozhi-net`; puertos publicados en `${LAN_IP}`.
- El fork Go ejecuta solo `toolCall[0]` por respuesta; las tools MCP llevan prefijo `mcp_`.
- El MCP nextcloud corre por stdio, un proceso por conexión.
- La verificación de tool calling de DeepSeek por voz sigue pendiente (`server/docs/nextcloud-mcp.md`). Es requisito para crear recordatorios por voz.
- Zona horaria: America/Santiago (la del MCP); el OTA envía `TimezoneOffset=-180`.
- Reglas de trabajo: nunca leer `.env` ni `.mk`, sin push ni merge sin preguntar, `docker run --rm`.

## Enfoques considerados

**Transporte** (elegido por el usuario: C)

| Opción | Qué es | Por qué no / por qué sí |
|---|---|---|
| A. WebSocket persistente | Mantener el ws abierto | El firmware cierra el ws al terminar la conversación; no hay canal en reposo. |
| B. Firmware propio con ws | Cambiar el firmware para escuchar siempre | Exige firmware desde el día uno; queda para etapa 2. |
| **C. MQTT vía gateway** | El firmware de fábrica ya soporta MQTT+UDP; se mantiene suscrito en reposo | **Elegido.** Sin firmware nuevo; el `alert` ya existe en el protocolo. |

**Almacenamiento** (elegido por el usuario: A)

| Opción | Por qué no / por qué sí |
|---|---|
| **A. VTODO con DUE en Nextcloud Tasks** | **Elegido.** Se ven y editan desde Nextcloud; reutiliza nextcloud-mcp. |
| B. Tabla sqlite en xiaozhi | Invisible fuera del bot; otro almacén. |
| C. Calendario (VEVENT) | Mezcla recordatorios con eventos; las tareas ya tienen flujo de completar. |

## Diseño

### Vista general

```
              +-------------------- xiaozhi-net --------------------+
 ESP32 --MQTT 1883/UDP--> mqtt-gateway --ws--> xiaozhi (Go) --stdio--> nextcloud-mcp --> Nextcloud
                              ^                                                           ^
                              | POST /push (token)                                        |
                         recordatorios (scheduler) -------------- CalDAV -----------------+
```

### Componente 1: mqtt-gateway (servicio nuevo)

- Base: upstream `xiaozhi-mqtt-gateway` (Node). Se vendoriza o se fija una versión en `server/mqtt-gateway/` con un Dockerfile propio.
- Variables: `MQTT_PORT=1883`, `UDP_PORT` (ver Puertos), `PUBLIC_IP=${LAN_IP}`, credenciales MQTT, `PUSH_TOKEN`.
- `config/mqtt.json` → `production.chat_servers: ["ws://xiaozhi:8000/xiaozhi/v1/"]`.
- **Autenticación MQTT:** clientId de 3 partes (`GID@@@mac@@@uuid`) validado por `validateMqttCredentials` contra usuario/clave fijos de env, que son los mismos que entrega el OTA.
- **Endpoint de push (único cambio al gateway):**
  - `POST /push`, `Authorization: Bearer <PUSH_TOKEN>`, cuerpo `{ "mac"?: string, "payload": object }`.
  - Normaliza la MAC (acepta `:` o `_`, minúsculas) y busca en el Map de conexiones la que tenga ese `macAddress`. Sin `mac`: envía a todas.
  - Llama a `connection.sendMqttMessage(JSON.stringify(payload))`.
  - Respuestas: 200 `{sent: n}` con n ≥ 1; 401 token inválido; 404 dispositivo no conectado, **o sin `mac` y ningún dispositivo conectado** (decisión de Claude: así el planificador reintenta igual que con un dispositivo concreto); 400 cuerpo inválido.
  - Escucha solo en la red interna (puerto no publicado en el host).
- Si el ws del Go exige autenticación, el gateway envía los headers necesarios (el bridge ya manda `device-id` y `client-id`).

### Componente 2: OTA del fork Go

- Cuando `Transport.MQTTUDP.Enabled` es true, `src/httpsvr/ota/handler.go` llena `OTAResponse.MQTT` (`MQTTInfo`, ya existe en `dto.go`):
  - `endpoint`: `${LAN_IP}:1883` (desde `MQTTUDP.MQTT.IP/Port`);
  - `client_id`: el de 3 partes que ya arma el handler (`CGID_test@@@mac_con_guiones_bajos@@@clientID`);
  - `username`/`password`: de config/env, iguales a los del gateway. El struct `MQTTUDP.MQTT` hoy no tiene esos campos: se agregan `Username` y `Password` (decisión de Claude), con valores tomados de env (`MQTT_USERNAME`/`MQTT_PASSWORD`) para no guardarlos en la BD de config;
  - `publish_topic`: `device-server`; `subscribe_topic`: `null`.
- Se usan las estructuras existentes (`configs/config.go` `MQTTUDP`, defaults en `config_default_init.go`); no se crean nuevas.
- Con `Enabled=false` el comportamiento es el actual (solo WebSocket). Es el interruptor de rollback.
- **Dónde vive el interruptor** (decisión de Claude): después del primer arranque la config se lee de la BD y se edita con la API de config del sistema (`src/httpsvr/webapi/system_config.go`). Para no depender de la BD en un rollback, se agrega una variable `MQTTUDP_ENABLED` que, si está definida, sobrescribe `Enabled` al arrancar. Rollback = cambiarla a `false` en compose y reiniciar `xiaozhi`.

### Componente 3: herramientas nuevas en nextcloud-mcp

Todas en un archivo nuevo `tools/recordatorios.go`, mismo estilo que `tools/tareas.go`.

| Tool | Entrada | Efecto |
|---|---|---|
| `recordatorio_crear` | `texto`, `cuando` | `text.ParseWhen(cuando, loc)`; si no hay hora, error pidiendo hora. Crea VTODO en la lista "Recordatorios" (se crea si no existe con `MkCalendarTodos`). Devuelve la hora interpretada en palabras para que Weita la confirme. |
| `recordatorio_listar` | — | Recordatorios futuros no completados ni cancelados, ordenados por DUE. |
| `recordatorio_cancelar` | `texto` o `uid` | `STATUS:CANCELLED`. Sin confirmación (es reversible desde Nextcloud). Si hay varias coincidencias, devuelve la lista y no cancela. |
| `recordatorios_pendientes` | — | Uso interno (ver Componente 5). Devuelve el texto a leer y marca lo leído. |

- **`recordatorios_pendientes` no se le ofrece al LLM** (decisión de Claude): se registra en el MCP pero el fork Go la filtra de la lista de tools que manda al modelo; solo la llama `handleHelloMessage`. Así el LLM no puede marcar recordatorios como leídos por error.

- **VALARM:** `ical.MakeVTodo` acepta una alarma opcional y escribe `BEGIN:VALARM / ACTION:DISPLAY / TRIGGER;RELATED=END:PT0S / DESCRIPTION:<summary> / END:VALARM`, para que Nextcloud también avise en sus clientes. Las tareas normales no cambian.
- **Marcas** (X-props, con `ical.SetVTodoField`, que respeta el anidamiento):
  - `X-WEITA-NOTIFIED:<timestamp>`: ya se envió el `alert`.
  - `X-WEITA-SPOKEN:<timestamp>`: ya se leyó en voz alta.
- **Alarma vs recordatorio:** en etapa 1 son lo mismo (mismo `alert`). El LLM puede usar cualquiera de las dos palabras; la tool es una sola.

**Ciclo de vida de un recordatorio** (decidido por el usuario: opción C del builder check)

1. Se crea abierto (`NEEDS-ACTION`) con DUE.
2. Vence → el planificador envía el `alert` y marca `X-WEITA-NOTIFIED`.
3. Weita lo lee en voz alta → `X-WEITA-SPOKEN` **y `STATUS:COMPLETED` + `COMPLETED:<ts>`**. En Nextcloud queda como hecho; el historial se ve con "mostrar completadas".
4. Cancelado por voz → `STATUS:CANCELLED`.

Además, para que un recordatorio nunca aparezca dos veces:

- La lista "Recordatorios" queda **fuera** de `tareas_pendientes`, `tarea_completar/cambiar/borrar` y de la sección de tareas del resumen (se filtra por nombre en `todoCalendars`, con un parámetro para incluirla en las tools de recordatorios). El resumen tiene su propia sección de recordatorios (ver Resumen de la mañana).
- `isCompleted` (`tools/agenda.go`) pasa a descartar también `CANCELLED`, en todas las listas. Es un cambio de comportamiento para las tareas normales: una tarea cancelada deja de aparecer como pendiente.

### Resumen de la mañana (reemplaza la lógica de `resumen_del_dia`)

Propuesta de Claude a partir de lo que pidió el usuario ("conciso: eventos del día, tareas que vencen hoy, recordatorios de hoy, prioridad alta, vencidas sin completar para reagendar o cancelar"). Una sola función produce el texto, tanto si el usuario pide "resumen del día" por voz como si lo lee Weita tras el aviso matinal.

Orden y reglas:

| # | Sección | Qué entra | Límite |
|---|---|---|---|
| 1 | Eventos | Eventos de hoy, "a las 10, reunión con X"; todo el día primero | 5 y "N más" |
| 2 | Recordatorios | De la lista "Recordatorios", DUE hoy, no completados: "a las 15, llamar al proveedor" | 5 |
| 3 | Vencen hoy | Tareas con DUE hoy | 5 |
| 4 | Prioridad alta | PRIORITY 1–4, sin DUE o con DUE futuro (las de hoy o vencidas ya salen arriba) | 3 |
| 5 | Atrasadas | DUE antes de hoy, no completadas: "Quedaron atrasadas: X (de ayer), Y (del lunes). ¿Las reagendo o las cancelo?" | 5 y "N más" |

- **Las secciones vacías no se dicen.** Si todo está vacío: "Hoy no tienes nada agendado."
- Sin el nombre de la lista entre corchetes (hoy aparece `[Personal]`): en voz es ruido. Se agrega solo si dos tareas tienen el mismo título.
- Horas en palabras con `text.Human`; fechas de las atrasadas relativas ("de ayer", "del lunes", "del 3 de noviembre").
- Las atrasadas van al final y terminan en pregunta, para que el usuario conteste en el mismo turno ("cancela la primera", "pasa la otra al viernes"). Weita responde con las tools existentes: `tarea_cambiar` (nueva fecha) y `tarea_borrar`/`tarea_completar`. No se crea una tool nueva.
- Ejemplo: *"Hoy martes 18: a las 10, dentista; a las 15, recordatorio de llamar al proveedor. Vence hoy: pagar la luz. Prioridad alta: preparar presupuesto. Quedaron atrasadas: enviar factura, de ayer. ¿La reagendo o la cancelo?"*

### Componente 4: planificador `recordatorios` (servicio nuevo)

- Mismo binario que el MCP, subcomando: `/app/nextcloud-mcp scheduler`. Corre como servicio aparte en compose, con la imagen de xiaozhi. Reutiliza los paquetes `nc` e `ical`.
- Env: `NEXTCLOUD_*`, `PUSH_URL` (`http://mqtt-gateway:<puerto>/push`), `PUSH_TOKEN`, `RECORDATORIOS_MAC` (vacía = todos los conectados), `RESUMEN_HORA` (ej. `08:00`, vacía = desactivado), `TZ`.
- **Ciclo cada 60 s:**
  1. `Query` en la lista "Recordatorios": DUE entre `now-24h` y `now`, sin COMPLETED/CANCELLED, sin `X-WEITA-NOTIFIED`.
  2. Por cada uno, `Notifier.Notify(ctx, Aviso{Titulo, Texto})`.
  3. Si el push devolvió 200, `PutObject` con `X-WEITA-NOTIFIED` (con etag; si hay conflicto, se reintenta en el próximo ciclo).
  4. Si devolvió 404 (dispositivo apagado o desconectado), no se marca y se reintenta en cada ciclo hasta 24 h. Así no se pierde: cuando el dispositivo vuelva y se apriete el botón, igual se lee (Componente 5).
  5. Varios vencidos en el mismo ciclo: un solo `alert` con "Tienes N recordatorios".
- **Resumen diario:** a `RESUMEN_HORA`, una vez por día, envía un `alert` con status "Buenos días" y mensaje "Tienes tu resumen del día". Guarda `resumen_enviado = hoy` en el archivo de estado **solo si el push devolvió 200**; si devolvió 404, reintenta en cada ciclo hasta las 23:59 de ese día (decisión de Claude, misma regla de no pérdida que los recordatorios).
- **Interfaz `Notifier`:** `AlertNotifier` (etapa 1) arma `{"type":"alert","status":"Recordatorio","message":<texto corto>,"emotion":"thinking"}` y hace el POST. En etapa 2 se agrega `WakeNotifier` sin tocar el ciclo.
- **Archivo de estado:** JSON en un volumen compartido por `xiaozhi` y `recordatorios` (`/data/recordatorios-state.json`): `{"resumen_enviado": "2026-11-18", "resumen_leido": "2026-11-18"}`. Escritura atómica (archivo temporal + rename). **Cada proceso escribe solo su campo** (decisión de Claude): el planificador `resumen_enviado`, el MCP `resumen_leido`; antes de escribir se relee el archivo y se toma un `flock` sobre `recordatorios-state.lock`, para que una escritura no pise la otra.
- **Entrypoint** (decisión de Claude): el servicio `recordatorios` usa la imagen de xiaozhi con `entrypoint: ["/app/nextcloud-mcp", "scheduler"]` en compose; no se crea otra imagen.

### Componente 5: lectura al apretar el botón

- En modo MQTT, cada vez que se aprieta el botón de escucha el dispositivo manda `hello` y el gateway abre un ws nuevo al servidor.
- En `handleHelloMessage` (`src/core/connection_handlemsg.go`), después de `sendHelloMessage()`, el servidor:
  1. Espera a que el MCP esté listo (timeout ~2 s; si no está, sigue sin leer nada).
  2. Llama a `mcp_recordatorios_pendientes` directamente (sin pasar por el LLM).
  3. Si devuelve texto, lo envía a TTS como primera respuesta y lo agrega al diálogo como mensaje del asistente, para que el usuario pueda seguir ("¿y a qué hora era lo segundo?").
- `recordatorios_pendientes` devuelve, en este orden:
  - recordatorios con `X-WEITA-NOTIFIED` (o vencidos) y sin `X-WEITA-SPOKEN`, y los marca `X-WEITA-SPOKEN`;
  - el resumen de la mañana (ver arriba) si `RESUMEN_HORA` está configurada, ya pasó esa hora hoy y `resumen_leido != hoy`; luego marca `resumen_leido`. **No depende de que el `alert` haya llegado** (decisión de Claude): si el dispositivo estuvo apagado a las 8, el primer botón del día igual lee el resumen.
  - Como el resumen ya incluye los recordatorios de hoy, cuando se leen los dos juntos los recordatorios vencidos se dicen primero y el resumen omite los que se acaban de leer (ya quedaron `COMPLETED`).
  - Si no hay nada, devuelve vacío y el servidor no dice nada extra.
- Recordatorios vencidos hace más de 24 h y nunca leídos: se leen una vez con "Tenías pendiente…". No se pierden.

### Pantalla (etapa 1)

- El `alert` del firmware: `SetStatus(status)`, `SetEmotion(emotion)`, `SetChatMessage("system", message)` y suena `OGG_VIBRATION`.
- Usamos `emotion: "thinking"`, status corto ("Recordatorio" / "Buenos días") y el texto del recordatorio (recortado a ~40 caracteres) como mensaje.
- Cómo se ve en el firmware de fábrica es desconocido: se prueba en el Milestone 0. El globo de idea real es etapa 2.

### Puertos y compose

- `mqtt-gateway`: publica `${LAN_IP}:1883:1883/tcp` y el puerto UDP de audio `${LAN_IP}:8884:8884/udp` (default del gateway). **Verificado por el usuario el 2026-11-18: ni 1883/TCP ni 8884/UDP están en uso en el servidor** (`ss -tulpn` sin resultados), así que no hay choque con un Mosquitto de Frigate. `PUBLIC_IP=${LAN_IP}` (es lo que el gateway le da al dispositivo para el UDP).
- El puerto HTTP de push no se publica.
- `recordatorios`: sin puertos.
- Volumen nuevo `recordatorios-state` montado en `xiaozhi` y `recordatorios`.
- Credenciales y `PUSH_TOKEN` en `server/.env` (el usuario las agrega; el builder documenta los nombres en `.env.example` si existe).

### Milestone 0 (compuerta antes de construir lo demás)

1. Levantar el gateway y activar `MQTTUDP` en el OTA.
2. Reiniciar el dispositivo: ¿pide OTA, recibe `mqtt` y se conecta al gateway? ¿La conversación funciona igual (audio por UDP)?
3. Con un `curl` al endpoint de push, enviar un `alert` manual y observar: sonido, emoción, texto.
4. Probar rollback: `Enabled=false`, reiniciar, ¿vuelve a WebSocket?

Si el paso 2 o 3 falla, se detiene y se replantea con el usuario (posible adelanto de la etapa 2).

### Pruebas

- nextcloud-mcp: tests unitarios con `nctest` para las 4 tools, VALARM en `MakeVTodo`, las marcas, el paso a `COMPLETED` al leerse, que "Recordatorios" no salga en `tareas_pendientes`, que `CANCELLED` se descarte, y el resumen de la mañana (orden de secciones, secciones vacías omitidas, "nada agendado", límites, atrasadas con pregunta).
- Planificador: tests del ciclo con un `Notifier` falso y CalDAV de `nctest` (vencido, ya notificado, 404 y reintento, varios a la vez, resumen una vez por día y su reintento tras 404 hasta medianoche).
- Gateway: test del endpoint de push (token, normalización de MAC, 404).
- Go: test de que el OTA llena `mqtt` solo con `Enabled=true`.
- E2E por voz: lo ejecuta el usuario, se agrega al checklist de `server/docs/nextcloud-mcp.md`.

## Riesgos conocidos

- **El firmware de fábrica puede no aceptar MQTT o no mostrar el `alert`.** Mitigación: Milestone 0.
- **Rollback incompleto:** el firmware puede guardar la config MQTT en NVS y seguir intentándolo aunque el OTA ya no la mande. Verificar en el Milestone 0; si pasa, el rollback es dejar el gateway levantado.
- **Todo el audio pasa por el gateway:** un punto de falla más y algo de latencia.
- **Retraso:** el polling de 60 s puede atrasar el aviso hasta un minuto.
- **Solo `toolCall[0]`:** "recuérdame X y Y" crea solo uno. Weita debe pedir de a uno.
- **Tool calling de DeepSeek por voz sin verificar.** Sin eso no se crean recordatorios por voz (el planificador igual funciona con recordatorios creados desde Nextcloud).
- **MCP no listo al `hello`:** el timeout evita bloquear; en ese caso la lectura queda para el próximo botón.
- **Sin cifrado MQTT** (QoS 0, texto plano en la LAN). Aceptable solo en LAN.

## Lo que queda al builder

- Cómo vendorizar el gateway (submódulo, copia fija o imagen con parche) y el puerto HTTP interno del push.
- Nombres exactos de variables de entorno y su documentación.
- Formato exacto del texto que lee Weita y del recorte para la pantalla.
- Cómo esperar al MCP en `handleHelloMessage` sin bloquear la conexión.
- Orden de tareas del plan, siempre con el Milestone 0 primero.
