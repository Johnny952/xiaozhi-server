# nextcloud-mcp: checklist E2E y notas operativas

## Task 1: tool calling de DeepSeek vía LiteLLM

**Estado: PENDIENTE (no ejecutado).** Requiere la API key de DeepSeek, que no estaba disponible al implementar. El primer test E2E por voz lo valida. Si DeepSeek no emite tool calls, la corrección es cambiar el alias `deepseek-chat` en `server/litellm/litellm-config.yaml` por un modelo de DeepSeek que sí los emita (sin razonamiento).

## Limitación conocida

El fork de xiaozhi ejecuta solo la primera llamada a herramienta (`toolCall[0]`) por respuesta del LLM. Si el modelo emite varias tool calls en una misma respuesta, las demás se ignoran. Las tools MCP llegan al LLM con el prefijo `mcp_`. Por eso conviene pedir una acción por frase.

## Checklist E2E por voz (lo ejecuta el usuario)

- [ ] **Paso 1, antes de todo lo demás:** verificar que DeepSeek, vía LiteLLM, hace tool calling correcto (Task 1). Sin esto nada de lo siguiente funciona.
- [ ] Conectar un dispositivo con `NEXTCLOUD_*` vacías: debe recibir `hello` de inmediato (el MCP responde el handshake sin tools).
- [ ] Buscar un archivo con `_` en el nombre (LIKE).
- [ ] Anotar en la bitácora, corregir una entrada, leer el día y leer la semana.
- [ ] Crear una tarea duplicada (debe avisar) y luego completarla; verificar que queda en la bitácora.
- [ ] Borrar una tarea con confirmación; verificar que se rechaza la auto-confirmación.
- [ ] "¿Qué tengo hoy?"
- [ ] Guardar en una nota existente.

## Latencia medida

| Herramienta | Medición 1 | Medición 2 | Medición 3 | Notas |
|---|---|---|---|---|
| `resumen_del_dia` | | | | |
| `bitacora_leer` (semana) | | | | |

## Milestone 0: MQTT

Compuerta antes de seguir con los recordatorios: valida que el parlante hable por MQTT+UDP a través del gateway y que el servidor pueda empujar un alert.

- [ ] **1. Variables y despliegue.** Agregar al `.env` (lo hace el usuario): `MQTT_USERNAME`, `MQTT_PASSWORD`, `PUSH_TOKEN` y `MQTTUDP_ENABLED=true`. Luego `docker compose up -d --build mqtt-gateway xiaozhi`.
- [ ] **2. Conversación por UDP.** Reiniciar el parlante. En los logs del gateway aparecen la conexión MQTT y el canal UDP. Tener una conversación normal.
- [ ] **3. Alert manual.** El token vive en el contenedor, no en la shell, y la red depende del nombre del proyecto compose, así que conviene colgarse de la red del propio gateway. Correr cada línea por separado (en una sola línea `A=.. B=.. cmd` las variables no quedan):

  ```
  GW=$(docker ps --format '{{.Names}}' | grep mqtt-gateway | head -1)
  TOKEN=$(docker exec "$GW" printenv PUSH_TOKEN)
  echo "largo_token=${#TOKEN}"   # debe ser > 0
  docker run --rm --network container:$GW curlimages/curl -s -X POST http://localhost:8007/push -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"payload":{"type":"alert","status":"Prueba","message":"Hola desde el server","emotion":"happy"}}'
  ```

  Esperado: `{"sent":1}` y el alert en pantalla. `{"error":"no connected device"}` significa que el parlante no está conectado por MQTT (ver `docker logs "$GW" --tail 50`).
- [ ] **4. Rollback.** `MQTTUDP_ENABLED=false`, recrear `xiaozhi`, reiniciar el parlante: debe volver a WebSocket. Si el firmware se queda pegado a MQTT por la NVS, el rollback es dejar el gateway levantado; anotar el resultado.

Resultado: _(pendiente)_

## Recordatorios: checklist E2E

Requiere el Milestone 0 aprobado. Agregar al `.env` (lo hace el usuario): `RECORDATORIOS_MAC` (MAC del parlante; vacío = todos los conectados) y `RESUMEN_HORA` (HH:MM, America/Santiago; vacío = sin resumen). `RESUMEN_HORA` inválida o `PUSH_TOKEN` vacío hacen que `recordatorios` falle al arrancar con un mensaje claro. Luego `docker compose up -d --build` (`recordatorios` comparte build e imagen con `xiaozhi`).

- [ ] **1.** "Recuérdame en 3 minutos tomar agua": aparece en Nextcloud Tasks, en la lista Recordatorios, con alarma.
- [ ] **2.** A los 3 minutos aparece el alert en la pantalla.
- [ ] **3.** Apretar el botón: el parlante lo lee y en Nextcloud la tarea queda completada.
- [ ] **4.** "¿Qué recordatorios tengo?" y luego "cancela el de tomar agua".
- [ ] **5.** Con `RESUMEN_HORA` 2 minutos adelante: alert "Buenos días"; el botón lee el resumen; una segunda vez no lo repite.
- [ ] **6.** `docker compose restart recordatorios`: no repite avisos ya dados.
- [ ] **7.** "¿Qué tareas tengo?" no muestra los recordatorios.
