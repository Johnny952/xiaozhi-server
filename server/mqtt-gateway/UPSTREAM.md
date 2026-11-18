# Upstream

- Origen: https://github.com/78/xiaozhi-mqtt-gateway
- Commit: c5e3235df8db8f06d1710074ec10e870159e0844 (2025-05-26)
- Vendorizado: 2026-11-18 (copia sin `.git`)

## Mapa del entrypoint (`app.js`)

- Conexiones: `MQTTServer.connections` (`Map` connectionId -> `MQTTConnection`).
- MAC/deviceId: `MQTTConnection.macAddress` (minúsculas con `:`), extraído del clientId `GID@@@mac@@@uuid`.
- Envío MQTT al dispositivo: `MQTTConnection.sendMqttMessage(str)` (topic `devices/p2p/<mac>`).
- `validateMqttCredentials`: `utils/mqtt_config_v2.js`, llamada desde `MQTTConnection.handleConnect`.
- Bridge WebSocket: `WebSocketBridge` envía los headers `device-id` y `client-id` al `chat_servers` de `config/mqtt.json`.

## Cambios locales

- `push.js` + `test/`: servidor HTTP `POST /push` (Bearer `PUSH_TOKEN`), puerto `PUSH_PORT` (8007), interno.
- `validateMqttCredentials`: solo acepta `MQTT_USERNAME`/`MQTT_PASSWORD` del entorno; sin ellas rechaza todo. clientId debe tener 3 partes (se elimina la rama de 2 partes sin autenticación). Se quitó la firma HMAC y el log de usuario/contraseña.
- Manejo de `SIGTERM`; cierre del push server.
- `config/mqtt.json` apunta a `ws://xiaozhi:8000/xiaozhi/v1/`.
- `.gitignore`: se versiona `package-lock.json` (necesario para `npm ci`).
