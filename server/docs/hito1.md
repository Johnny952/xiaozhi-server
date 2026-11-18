# Hito 1 — el equipo real habla con nuestro servidor

**Fecha:** 2026-11-17 · **Resultado: GO**

## Despliegue

- Coolify 4.7.5 (servidor `localhost`), proyecto "Casa inteligente", entorno production.
- Build pack Docker Compose desde la rama `main`, commit `d943b75`.
- Variables seteadas en la UI de Coolify: `LAN_IP=192.168.100.13`, `LITELLM_MASTER_KEY`.
- Puertos publicados solo en la LAN (desde `ports:` del compose; no usar "Ports Exposes" ni Domains):
  - `192.168.100.13:8100` → WebSocket (8000 en el contenedor)
  - `192.168.100.13:8180` → OTA HTTP (8080 en el contenedor)

## Configuración del equipo

Equipo: Elshare "emo Xiaozhi". Firmware stock 2.4.2, board `sp-esp32-s3-1.54-muma-a`, pantalla 240x240, 16 MB de flash, idf v5.5.4. IP 192.168.100.107.

Portal Wi-Fi (192.168.4.1) → Avanzado → URL OTA personalizada:

```
http://192.168.100.13:8180/api/ota/
```

**Ojo: la barra final es obligatoria.** Sin ella Gin responde `307` y el equipo no sigue la redirección.

Errores que vimos en la pantalla del equipo:

| Código | Causa |
|---|---|
| 113 (host unreachable) | Puerto equivocado (8081 en vez de 8180) |
| 307 | URL OTA sin `/` final |

## Lo observado

- **OTA:** a las 22:46:30 el `POST /api/ota/` respondió 200 con `websocket.url = ws://192.168.100.13:8100/xiaozhi/v1/`. No ofreció firmware ni activación. El equipo quedó registrado localmente.
- **WebSocket:** se conectó a las 22:49:12. Lo abre solo al apretar el botón o con la wake word ("你好小智"). Hablarle sin eso no abre la conexión.
- **`hello` del equipo:**
  - `version: 1`, `transport: websocket`
  - `audio_params`: `format: opus`, `sample_rate: 16000`, `channels: 1`, `frame_duration: 60`
  - `features`: `mcp: true`, `glyph_push: false`
  - Modo de escucha: `listen` con `mode: auto`, `state: start`
- **Herramientas MCP del equipo:**
  - `self.get_device_status`
  - `self.audio_speaker.set_volume`
  - `self.screen.set_brightness`
  - `self.screen.set_theme`
- **Audio:** llegaron frames Opus y el servidor los decodificó.
- **Privacidad:** el ASR externo (Doubao) intentó conectarse y el blackhole lo cortó (`dial tcp 0.0.0.0:443: connection refused`). El servidor no sacó audio de la LAN.

## Decisión

**GO.** El equipo hace POST al OTA, abre el WebSocket, manda `hello` y llegan frames de audio. Seguimos al Task 5.

- `sample_rate` del provider ASR `openai`: **16000**.
- El idioma que reporta el equipo es `en-US`. No afecta: el ASR fuerza `language: es`.

## Notas para lo que sigue

- La lista de `extra_hosts` (blackhole) tiene que seguir la selección de providers. Si se cambia a un provider externo, revisar que su host siga bloqueado o que se decida dejarlo salir explícitamente.
- El `.dockerignore` excluye `docs/`. Este archivo no entra a la imagen, a propósito.
- Ya no hay contenedor de prueba local en el escritorio (192.168.100.12). Solo corre el despliegue de Coolify en el servidor.
