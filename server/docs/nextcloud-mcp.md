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
- [ ] **3. Alert manual** desde dentro de la red docker:

  ```
  docker run --rm --network server_xiaozhi-net curlimages/curl -s -X POST http://mqtt-gateway:8007/push -H "Authorization: Bearer $PUSH_TOKEN" -H 'Content-Type: application/json' -d '{"payload":{"type":"alert","status":"Prueba","message":"Hola desde el server","emotion":"happy"}}'
  ```

  Esperado: `{"sent":1}` y el alert en pantalla. (El nombre real de la red sale de `docker network ls`.)
- [ ] **4. Rollback.** `MQTTUDP_ENABLED=false`, recrear `xiaozhi`, reiniciar el parlante: debe volver a WebSocket. Si el firmware se queda pegado a MQTT por la NVS, el rollback es dejar el gateway levantado; anotar el resultado.

Resultado: _(pendiente)_
