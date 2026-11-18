# Recordatorios y avisos al parlante vía MQTT: diseño

Fecha: 2026-11-18. Estado: borrador (etapa 1), pendiente de aprobación.

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
  - Respuestas: 200 `{sent: n}`; 401 token inválido; 404 dispositivo no conectado; 400 cuerpo inválido.
  - Escucha solo en la red interna (puerto no publicado en el host).
- Si el ws del Go exige autenticación, el gateway envía los headers necesarios (el bridge ya manda `device-id` y `client-id`).

### Componente 2: OTA del fork Go

- Cuando `Transport.MQTTUDP.Enabled` es true, `src/httpsvr/ota/handler.go` llena `OTAResponse.MQTT` (`MQTTInfo`, ya existe en `dto.go`):
  - `endpoint`: `${LAN_IP}:1883` (desde `MQTTUDP.MQTT.IP/Port`);
  - `client_id`: el de 3 partes que ya arma el handler (`CGID_test@@@mac_con_guiones_bajos@@@clientID`);
  - `username`/`password`: de config/env, iguales a los del gateway;
  - `publish_topic`: `device-server`; `subscribe_topic`: `null`.
- Se usan las estructuras existentes (`configs/config.go` `MQTTUDP`, defaults en `config_default_init.go`); no se crean nuevas.
- Con `Enabled=false` el comportamiento es el actual (solo WebSocket). Es el interruptor de rollback.

### Componente 3: herramientas nuevas en nextcloud-mcp

Todas en un archivo nuevo `tools/recordatorios.go`, mismo estilo que `tools/tareas.go`.

| Tool | Entrada | Efecto |
|---|---|---|
| `recordatorio_crear` | `texto`, `cuando` | `text.ParseWhen(cuando, loc)`; si no hay hora, error pidiendo hora. Crea VTODO en la lista "Recordatorios" (se crea si no existe con `MkCalendarTodos`). Devuelve la hora interpretada en palabras para que Weita la confirme. |
| `recordatorio_listar` | — | Recordatorios futuros no completados ni cancelados, ordenados por DUE. |
| `recordatorio_cancelar` | `texto` o `uid` | `STATUS:CANCELLED`. Sin confirmación (es reversible desde Nextcloud). Si hay varias coincidencias, devuelve la lista y no cancela. |
| `recordatorios_pendientes` | — | Uso interno (ver Componente 5). Devuelve el texto a leer y marca lo leído. |

- **VALARM:** `ical.MakeVTodo` acepta una alarma opcional y escribe `BEGIN:VALARM / ACTION:DISPLAY / TRIGGER;RELATED=END:PT0S / DESCRIPTION:<summary> / END:VALARM`, para que Nextcloud también avise en sus clientes. Las tareas normales no cambian.
- **Marcas** (X-props, con `ical.SetVTodoField`, que respeta el anidamiento):
  - `X-WEITA-NOTIFIED:<timestamp>`: ya se envió el `alert`.
  - `X-WEITA-SPOKEN:<timestamp>`: ya se leyó en voz alta.
- **Alarma vs recordatorio:** en etapa 1 son lo mismo (mismo `alert`). El LLM puede usar cualquiera de las dos palabras; la tool es una sola.

### Componente 4: planificador `recordatorios` (servicio nuevo)

- Mismo binario que el MCP, subcomando: `/app/nextcloud-mcp scheduler`. Corre como servicio aparte en compose, con la imagen de xiaozhi. Reutiliza los paquetes `nc` e `ical`.
- Env: `NEXTCLOUD_*`, `PUSH_URL` (`http://mqtt-gateway:<puerto>/push`), `PUSH_TOKEN`, `RECORDATORIOS_MAC` (vacía = todos los conectados), `RESUMEN_HORA` (ej. `08:00`, vacía = desactivado), `TZ`.
- **Ciclo cada 60 s:**
  1. `Query` en la lista "Recordatorios": DUE entre `now-24h` y `now`, sin COMPLETED/CANCELLED, sin `X-WEITA-NOTIFIED`.
  2. Por cada uno, `Notifier.Notify(ctx, Aviso{Titulo, Texto})`.
  3. Si el push devolvió 200, `PutObject` con `X-WEITA-NOTIFIED` (con etag; si hay conflicto, se reintenta en el próximo ciclo).
  4. Si devolvió 404 (dispositivo apagado o desconectado), no se marca y se reintenta en cada ciclo hasta 24 h. Así no se pierde: cuando el dispositivo vuelva y se apriete el botón, igual se lee (Componente 5).
  5. Varios vencidos en el mismo ciclo: un solo `alert` con "Tienes N recordatorios".
- **Resumen diario:** a `RESUMEN_HORA`, una vez por día, envía un `alert` con status "Buenos días" y mensaje "Tienes tu resumen del día". Guarda "enviado hoy" en el archivo de estado.
- **Interfaz `Notifier`:** `AlertNotifier` (etapa 1) arma `{"type":"alert","status":"Recordatorio","message":<texto corto>,"emotion":"thinking"}` y hace el POST. En etapa 2 se agrega `WakeNotifier` sin tocar el ciclo.
- **Archivo de estado:** JSON en un volumen compartido por `xiaozhi` y `recordatorios` (`/data/recordatorios-state.json`): `{"resumen_enviado": "2026-11-18", "resumen_leido": "2026-11-18"}`. Escritura atómica (archivo temporal + rename).

### Componente 5: lectura al apretar el botón

- En modo MQTT, cada vez que se aprieta el botón de escucha el dispositivo manda `hello` y el gateway abre un ws nuevo al servidor.
- En `handleHelloMessage` (`src/core/connection_handlemsg.go`), después de `sendHelloMessage()`, el servidor:
  1. Espera a que el MCP esté listo (timeout ~2 s; si no está, sigue sin leer nada).
  2. Llama a `mcp_recordatorios_pendientes` directamente (sin pasar por el LLM).
  3. Si devuelve texto, lo envía a TTS como primera respuesta y lo agrega al diálogo como mensaje del asistente, para que el usuario pueda seguir ("¿y a qué hora era lo segundo?").
- `recordatorios_pendientes` devuelve, en este orden:
  - recordatorios con `X-WEITA-NOTIFIED` (o vencidos) y sin `X-WEITA-SPOKEN`, y los marca `X-WEITA-SPOKEN`;
  - el resumen del día (reutiliza la lógica de `resumen_del_dia` en `tools/agenda.go`) si `resumen_enviado == hoy` y `resumen_leido != hoy`; luego marca `resumen_leido`.
  - Si no hay nada, devuelve vacío y el servidor no dice nada extra.
- Recordatorios vencidos hace más de 24 h y nunca leídos: se leen una vez con "Tenías pendiente…". No se pierden.

### Pantalla (etapa 1)

- El `alert` del firmware: `SetStatus(status)`, `SetEmotion(emotion)`, `SetChatMessage("system", message)` y suena `OGG_VIBRATION`.
- Usamos `emotion: "thinking"`, status corto ("Recordatorio" / "Buenos días") y el texto del recordatorio (recortado a ~40 caracteres) como mensaje.
- Cómo se ve en el firmware de fábrica es desconocido: se prueba en el Milestone 0. El globo de idea real es etapa 2.

### Puertos y compose

- `mqtt-gateway`: publica `${LAN_IP}:1883:1883/tcp` y el puerto UDP de audio (`${LAN_IP}:<UDP>:<UDP>/udp`). `PUBLIC_IP=${LAN_IP}` (es lo que el gateway le da al dispositivo para el UDP).
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

- nextcloud-mcp: tests unitarios con `nctest` para las 4 tools, VALARM en `MakeVTodo` y las marcas.
- Planificador: tests del ciclo con un `Notifier` falso y CalDAV de `nctest` (vencido, ya notificado, 404 y reintento, varios a la vez, resumen una vez por día).
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
- El puerto UDP concreto (el default del gateway es 8884; no debe chocar con 8100/8180).
- Nombres exactos de variables de entorno y su documentación.
- Formato exacto del texto que lee Weita y del recorte para la pantalla.
- Cómo esperar al MCP en `handleHelloMessage` sin bloquear la conexión.
- Orden de tareas del plan, siempre con el Milestone 0 primero.
