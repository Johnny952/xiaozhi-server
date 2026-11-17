# Xiaozhi auto-hospedado: servidor base — Diseño

Fecha: 2026-11-17 · Estado: borrador para aprobación

## 1. Intención y por qué

El dueño tiene un asistente de voz Xiaozhi y quiere que **toda la conversación pase por su propio servidor** en vez de la nube china de Xiaozhi (`api.tenclass.net` / `xiaozhi.me`), por privacidad. El único tercero aceptado es **DeepSeek**, que recibe el texto transcrito (no el audio) para generar respuestas. Además quiere **registro de tokens y costos** de cada llamada al LLM.

Esta es la **base**. Después vendrán integraciones (music-bot, Home Assistant, búsqueda web, etc.), así que la base debe dejar un punto claro donde enchufarlas (herramientas/MCP del servidor), pero **no las implementa**. Decisión del dueño: las integraciones quedan **sin bloquear**, no probadas. No se construye una herramienta de prueba de punta a punta en esta fase; se valida cuando llegue la primera integración real.

Idioma: **español latinoamericano (Chile)**, tanto en reconocimiento como en voz.

## 2. El dispositivo (hechos verificados)

- Elshare "emo Xiaozhi": ESP32-S3 en carcasa cerrada, pantalla 1.54", 2 botones laterales, USB-C, microSD.
- Firmware de fábrica: placa `esp32-muma-a`, versión **2.4.2** (variante de fabricante, no existe en el repo 78/xiaozhi-esp32).
- **El USB-C no enumera en el PC** en ningún estado (solo carga). No hay flasheo por cable sin abrir la carcasa.
- La página de configuración Wi-Fi del firmware (red `Xiaozhi-XXXX`, `http://192.168.4.1`) tiene en **Avanzado** el campo **"URL OTA personalizada"**. Esto está confirmado por el dueño.
- Al conectarse a la nube de fábrica pide activarse en xiaozhi.me. **No se ha activado y no se activará.**

**Consecuencia de diseño:** el firmware de fábrica se mantiene. Se le cambia la URL OTA a nuestro servidor. Ese firmware ya habla el protocolo Xiaozhi (OTA por HTTP + WebSocket con audio Opus), que es lo que implementa xiaozhi-server-go. **El firmware propio queda fuera de esta fase** (ver §4).

## 3. Objetivos

1. El dispositivo, con URL OTA `http://192.168.100.13:8180/api/ota/`, obtiene de nuestro servidor la URL del WebSocket y conversa por voz en español de punta a punta.
2. Flujo: ESP32 → **xiaozhi-server-go** (fork) → STT **faster-whisper** → LLM vía **LiteLLM** → **DeepSeek** → TTS **Piper** → ESP32.
3. LiteLLM registra tokens y costo por llamada en una base persistente, consultable desde su interfaz web en la LAN.
4. Todo corre en contenedores con un solo `docker compose up -d` en `xiaozhi-esp32/server/`.
5. El servidor **registra el cuerpo del POST OTA** del dispositivo (placa, chip, particiones, versión), porque es nuestra única fuente para identificar el hardware de cara a un futuro firmware propio.
6. El OTA **no ofrece actualización de firmware** ni código de activación: el dispositivo debe quedar en su versión actual y no pedir xiaozhi.me.

## 4. No-objetivos (esta fase)

- Firmware propio para el ESP32 (la carpeta `xiaozhi-esp32/esp32/` queda vacía por ahora). Si se hace después, se instalaría por OTA sin posibilidad de recuperación por USB, y requiere antes identificar la placa con los datos del objetivo 5.
- Integraciones: music-bot, Home Assistant, búsqueda web, MCP de terceros.
- Acceso desde fuera de la casa (Internet, Cloudflare Tunnel, Traefik/Coolify proxy). Tailscale tampoco, por ahora.
- GPU / modelos grandes. Solo CPU.
- Multi-usuario o múltiples dispositivos (debe funcionar con uno; si funciona con más, mejor, pero no se optimiza para eso).

## 5. Anti-objetivos (sería un fracaso aunque "funcione")

- Que el audio o el texto pase por cualquier servicio externo distinto de DeepSeek.
- Que el dispositivo se active o se vincule a xiaozhi.me, o que reciba una actualización de firmware no pedida.
- Que el servidor rompa lo que ya corre ahí: Coolify (8000), Traefik (80/443/8080), Frigate (5000), Nextcloud, etc.
- Que la carga de CPU degrade a Frigate de forma sostenida (picos durante una respuesta son aceptables; consumo constante en reposo no).
- Claves (DeepSeek, LiteLLM master key) en el repositorio.

## 6. Restricciones del servidor

- Ubuntu 24.04.3, i5-10500T (6c/12t, AVX2), 15 GB RAM (~11 GB libres), sin GPU NVIDIA, 41 GB libres en disco.
- Docker 28.5.1, Compose v2.40.3.
- IP LAN `192.168.100.13`.
- Puertos ya ocupados: 22, 80, 443, 1234, 3000, 5000, 6001-6002, 8000, 8080. Por eso xiaozhi-server-go **no puede usar sus puertos por defecto (8000/8080) hacia el host**.
- Frigate comparte CPU.

## 7. Enfoques considerados

| | Enfoque | Trade-off |
|---|---|---|
| **A (elegido)** | **Fork de xiaozhi-server-go + proveedores ASR/TTS genéricos "compatibles con OpenAI"** apuntando a **Speaches** (faster-whisper + Piper en un contenedor, con API OpenAI `/v1/audio/transcriptions` y `/v1/audio/speech`). | Hay que escribir dos proveedores en Go. A cambio, STT/TTS quedan intercambiables por cualquier servicio con API OpenAI (incluido LiteLLM si se quisiera). Mantiene el servidor que el dueño pidió. |
| B | Usar xiaozhi-esp32-server (Python, xinnan-tech), que ya trae FunASR/Whisper/Piper locales. | Menos código propio, pero no es el servidor que el dueño pidió, es más pesado y su ecosistema está muy orientado a China. |
| C | Servidor propio desde cero (Node/Go) que hable el protocolo Xiaozhi. | Control total, pero reimplementa el protocolo, VAD, Opus y manejo de sesión: mucho más trabajo para la base. |

El dueño eligió **A**.

## 8. Diseño

### 8.1 Componentes (un `docker-compose.yml`)

| Servicio | Imagen / origen | Expuesto al host | Rol |
|---|---|---|---|
| `xiaozhi` | build local del fork de **AnimeAIChat/xiaozhi-server-go** | `192.168.100.13:8100 → 8000` (WebSocket), `192.168.100.13:8180 → 8080` (OTA/web) | Protocolo con el ESP32, VAD, Opus, orquestación |
| `speaches` | imagen oficial CPU de Speaches | no (solo red interna) | STT faster-whisper + TTS Piper |
| `litellm` | imagen oficial de LiteLLM | `192.168.100.13:8140 → 4000` (UI + API, solo LAN) | Proxy a DeepSeek, registro de tokens/costos |
| `litellm-db` | Postgres oficial | no | Base de LiteLLM (propia; **no** reusar el Postgres existente del servidor) |

Red interna de compose compartida; los servicios se hablan por nombre (`http://speaches:8000`, `http://litellm:4000`).

Los puertos se publican atados a la IP LAN (`192.168.100.13:`) para no exponerlos en la interfaz de Tailscale ni en otras.

### 8.2 Flujo de una conversación

1. El dispositivo hace `POST /api/ota/` a `:8180`. El servidor **registra el cuerpo completo** (objetivo 5) y responde con `websocket.url = ws://192.168.100.13:8100/xiaozhi/v1/` (o la ruta que use server-go) y token, **sin** bloque `mqtt` (así el firmware elige WebSocket), **sin** `activation` y **sin** `firmware.url` (o con la misma versión que reporta el dispositivo, para que no actualice).
2. El dispositivo abre el WebSocket y envía audio **Opus** (16 kHz mono).
3. server-go decodifica a PCM, hace VAD y, al terminar el turno, el **proveedor ASR OpenAI-compatible** empaqueta el PCM como WAV y lo envía a `speaches /v1/audio/transcriptions` con `language=es` y un `prompt` de vocabulario.
4. El texto va al **proveedor LLM openai** de server-go apuntando a `http://litellm:4000` con modelo `deepseek-chat`. Streaming si server-go lo soporta.
5. server-go segmenta la respuesta en frases; cada frase va al **proveedor TTS OpenAI-compatible** → `speaches /v1/audio/speech` (voz Piper, formato wav/pcm), y server-go la codifica a Opus y la envía al dispositivo.

### 8.3 Modelos (decididos)

- **STT:** faster-whisper `small`, cuantización `int8`, `language=es`, con `prompt` inicial de vocabulario chileno/del hogar (editable en config). Se evaluará `large-v3-turbo` después si la precisión no alcanza, midiendo latencia en esta CPU.
- **TTS:** Piper `es_MX-claude-high`. Alternativa a probar: `es_AR-daniela-high`. La voz debe ser un valor de configuración, no código.
- **LLM:** DeepSeek `deepseek-chat` vía LiteLLM. Prompt de sistema en español chileno, respuestas cortas aptas para voz (sin markdown, sin listas largas).

### 8.4 Fork de xiaozhi-server-go

- Vive en `xiaozhi-esp32/server/xiaozhi-server-go/` (copia del upstream con historial propio; el método exacto —submódulo, subtree o copia— es decisión del constructor).
- Cambios mínimos y aislados:
  - Proveedor **ASR `openai`**: `base_url`, `api_key`, `model`, `language`, `prompt`, timeout.
  - Proveedor **TTS `openai`**: `base_url`, `api_key`, `model`, `voice`, `response_format`, `speed`, timeout.
  - Registro del cuerpo del POST OTA (log estructurado y/o tabla en su SQLite).
  - Respuesta OTA según §8.2 (sin activación, sin actualización).
  - Lo que haga falta en la config de puertos/URLs.
- Ambos proveedores se seleccionan con `selected_module` como los existentes. No se tocan los otros proveedores.

### 8.5 Configuración y secretos

- `server/.env` (ignorado, con `server/.env.example` versionado): `DEEPSEEK_API_KEY`, `LITELLM_MASTER_KEY`, contraseña de Postgres, IP LAN.
- Config de LiteLLM (`litellm-config.yaml`) versionada, con la clave por variable de entorno. Debe tener el costo de DeepSeek configurado para que los costos salgan bien.
- Config de server-go versionada como plantilla. Hay que resolver su particularidad de primer arranque (genera `.config.yaml` y termina; luego persiste en `config.db`): el contenedor debe arrancar ya configurado sin pasos manuales, y la config debe sobrevivir a reinicios (volumen).
- Modelos de Speaches en un volumen persistente, para no descargarlos en cada arranque.

### 8.6 Despliegue

- `docker compose` simple en el servidor, **no vía Coolify** (Coolify enruta por Traefik, y aquí se quieren puertos LAN directos). El código se sincroniza al servidor como el constructor estime (git, rsync).
- `restart: unless-stopped` en todo.
- Límite de CPU para `speaches` (por ejemplo, 4 de 12 hilos) para proteger a Frigate; el valor exacto se ajusta midiendo.

### 8.7 Puesta en marcha del dispositivo

Una vez el servidor responde: entrar a la red `Xiaozhi-XXXX` → `http://192.168.4.1` → Avanzado → "URL OTA personalizada" = `http://192.168.100.13:8180/api/ota/` → guardar y conectar al Wi-Fi de la casa.

## 9. Criterios de éxito

1. Tras configurar la URL OTA, el dispositivo arranca **sin pedir xiaozhi.me** y queda listo para hablar.
2. Una pregunta en español chileno recibe respuesta hablada coherente. Objetivo de latencia, desde que el usuario termina de hablar hasta el primer audio de respuesta: **≤ 3 s** típico. Decisión del dueño: es una **meta, no un requisito**. Si no se alcanza, se acepta la respuesta más lenta, se mide y se le muestran los números al dueño. No se le quita CPU a Frigate ni se baja de modelo sin consultarle.
3. La conversación aparece en LiteLLM con tokens y costo.
4. Los logs del servidor muestran el POST OTA completo del dispositivo.
5. Durante una conversación el dispositivo no contacta `tenclass.net` ni `xiaozhi.me`. Se verifica de forma razonable, por ejemplo con logs DNS del router o una captura de tráfico; el método queda a criterio del constructor.
6. Reinicio del servidor → todo vuelve solo, sin reconfigurar el dispositivo.

## 10. Riesgos conocidos

- **Compatibilidad del protocolo:** server-go se desarrolló contra el firmware upstream; el 2.4.2 del fabricante podría diferir (formato OTA, versión de protocolo, MCP). Mitigación: el primer hito es solo el OTA + handshake WebSocket, con logs completos.
- **No verificado:** que el proveedor LLM `openai` de server-go acepte un `base_url` arbitrario (LiteLLM). Si no, se ajusta en el fork.
- **Voces Piper en Speaches:** verificar cómo Speaches nombra o descarga las voces Piper en español y si `es_MX-claude-high` está disponible tal cual.
- **Latencia en CPU** con Frigate compartiendo. Por eso `small int8` y el límite de CPU. Si la meta de 3 s no se cumple, se mide y se informa (ver §9); el límite de CPU no se sube por cuenta propia.

## 10b. Plan escalonado (go/no-go)

**Hito 1:** contenedor `xiaozhi` + OTA + handshake WebSocket con el dispositivo real, con logs completos. En este hito no se construyen Speaches, LiteLLM ni los proveedores Go.
- **Si funciona**, se sigue con el resto.
- **Si el firmware 2.4.2 no es compatible** (exige activación, otra versión de protocolo, ignora `websocket`, etc.): se permite un parche acotado en el fork (orden de un día de trabajo). Si con eso no basta, **se detiene y se vuelve al dueño**. Las opciones a ponerle delante son el enfoque B (xinnan-tech) o abrir la carcasa. No hay ingeniería inversa sin límite.

## 10c. Contenido de las conversaciones

LiteLLM y server-go **no guardan el texto** de prompts ni respuestas: LiteLLM con el registro de contenido desactivado (solo tokens y costo), y server-go sin historial persistente más allá de la sesión activa. Los logs de depuración pueden mostrar transcripciones durante el desarrollo, pero quedan desactivados en la configuración final.

## 10d. Decisiones del dueño tras la revisión

- Latencia: los 3 s son meta, no requisito. Se mide y se informa; prioridad a no afectar a Frigate.
- Integraciones: sin bloquear, sin herramienta de prueba en esta fase.

## 11. Decisiones tomadas por el asistente (revisables)

- Plan escalonado con go/no-go tras el hito 1 (§10b).
- No guardar el contenido de las conversaciones (§10c).

- Puertos 8100 (WS), 8180 (OTA) y 8140 (LiteLLM UI), atados a la IP LAN.
- Postgres propio para LiteLLM, no reusar el existente.
- Docker compose directo, sin Coolify.
- Límite de CPU para Speaches.
- Firmware propio fuera de esta fase, dado que el USB no sirve para flashear.
- OTA nunca ofrece actualización de firmware.
- Este documento vive en `xiaozhi-esp32/docs/superpowers/specs/`. El proyecto no es repo git; se recomienda `git init` en `xiaozhi-esp32/` al empezar.

## 12. Queda a criterio del constructor

- Cómo se incorpora el fork (submódulo/subtree/copia) y cómo se sincroniza al servidor.
- Estructura interna de los proveedores Go y sus tests.
- Detalles de VAD, segmentación de frases y streaming dentro de lo que server-go ya ofrece.
- Valor exacto del límite de CPU y del timeout de cada proveedor.
- Método de verificación del criterio 5.
