# Xiaozhi Server Base — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que el Xiaozhi (firmware stock 2.4.2, `esp32-muma-a`) converse en español chileno hablando solo con el servidor Ubuntu propio: fork de xiaozhi-server-go → Speaches (faster-whisper + Piper) → LiteLLM → DeepSeek.

**Architecture:** Fork de xiaozhi-server-go vendorizado en `server/xiaozhi-server-go/`, con dos providers nuevos (`asr/openai`, `tts/openai`) que hablan la API OpenAI-compatible de Speaches, y un endpoint OTA endurecido (loguea el body, nunca ofrece firmware). Todo en un `docker compose` en `server/`. Hito 1 (solo xiaozhi + OTA + handshake con el equipo real) va antes que todo lo demás y termina en un go/no-go.

**Tech Stack:** Go 1.24 (CGO: go-sqlite3, opus), gin, gorm/sqlite, Docker multi-stage (golang:1.24-alpine → alpine), Speaches (faster-whisper `small` int8, Piper), LiteLLM + Postgres 16, DeepSeek `deepseek-chat`.

**Spec:** `docs/superpowers/specs/2026-11-17-xiaozhi-server-base-design.md`

## Global Constraints

- Puertos publicados solo en `192.168.100.13`: `8100→8000` (WebSocket), `8180→8080` (OTA/web), `8140→4000` (LiteLLM). Speaches y Postgres sin puertos publicados.
- No tocar Coolify (8000), Traefik (80/443/8080), Frigate (5000), Nextcloud. No usar Coolify para desplegar.
- `restart: unless-stopped` en todos los servicios.
- Speaches limitado a `cpus: "4"` (de 12). No subir modelos ni CPU sin preguntar.
- STT: `Systran/faster-whisper-small`, int8, `language: es`, con `prompt` de vocabulario. TTS: Piper `es_MX-claude-high` (configurable; alternativa `es_AR-daniela-high`). LLM: `deepseek-chat` vía `http://litellm:4000/v1`.
- Ningún audio ni texto sale a servicios externos salvo DeepSeek. El equipo nunca recibe oferta de firmware ni activación, nunca se vincula a xiaozhi.me.
- No se guarda contenido: LiteLLM sin logging de mensajes (`turn_off_message_logging: true`), sin historial persistente, `log_level: INFO` en la config final, `delete_audio: true`, `save_tts_audio: false`.
- Secretos solo en `server/.env` (git-ignorado): `DEEPSEEK_API_KEY`, `LITELLM_MASTER_KEY`, `LITELLM_DB_PASSWORD`, `LAN_IP`. `server/.env.example` versionado.
- Latencia ≤3 s es objetivo, no requisito: se mide y se reporta.
- Idioma de persona, `quick_reply` y `CMD_exit`: español chileno, sin markdown.

## Review Focus

1. **Cambio de config ignorado** — server-go copia `.config.yaml` a `config.db` en el primer arranque y después lee solo la DB (`configs.LoadConfig`). Esperado: editar el template + `docker compose up -d --force-recreate xiaozhi` aplica el cambio. Test en Task 3 (paso de recreate).
2. **Archivo `.bin` en `ota_bin/`** — si aparece cualquier `.bin`, upstream lo ofrece al equipo. Esperado: nunca se ofrece firmware, haya o no archivos. Test en Task 2 (`TestOTANeverOffersFirmwareEvenWithBin`).
3. **Silencio / ASR vacío** — el usuario aprieta y no habla, o Whisper devuelve `""`. Esperado: no se llama al LLM, no crashea, la sesión sigue. Test en Task 5 (`TestSendLastAudioEmptyBufferNoRequest`, `TestEmptyTranscriptReportsEmpty`).
4. **Speaches caído o lento** (arrancando o descargando modelo) — esperado: error logueado con timeout acotado, la conexión del equipo no se cuelga. Tests en Task 5 y 6 (`...Timeout`, `...Non200`).
5. **Reinicio del servidor** — esperado: todo vuelve sin reconfigurar el equipo y los modelos no se re-descargan. Verificación en Task 8.

---

## File Structure

```
xiaozhi-esp32/
  .gitignore                         # server/.env, **/config.db, **/logs/, **/tmp/
  server/
    docker-compose.yml               # xiaozhi (+ speaches, litellm, litellm-db en Task 7)
    .env.example
    litellm-config.yaml              # Task 7
    xiaozhi/
      config.template.yaml           # config de server-go con ${LAN_IP}, ${LITELLM_MASTER_KEY}
      entrypoint.sh                  # envsubst template → /app/.config.yaml, exec server
    xiaozhi-server-go/               # fork vendorizado (commit upstream 0b1575cb)
      Dockerfile                     # reemplazado: multi-stage con CGO
      src/httpsvr/ota/handler.go     # Task 2
      src/httpsvr/ota/ota_privacy_test.go
      src/core/providers/asr/openai/openai.go, openai_test.go, wav.go   # Task 5
      src/core/providers/tts/openai/openai.go, openai_test.go           # Task 6
      src/core/providers/tts/tts.go  # campos nuevos en Config (Task 6)
      src/configs/config.go          # campos nuevos en TTSConfig (Task 6)
      src/main.go                    # blank imports (Tasks 5, 6)
    docs/latency.md                  # Task 8: mediciones
```

**Decisión (mía): `config.db` efímero.** `config.db` vive en el filesystem del contenedor, no en un volumen. Sobrevive a reinicios (mismo contenedor) y se regenera desde el template en cada recreate. No hay estado de servidor que importe (sin activación, sin historial). Así un cambio de config no queda ignorado por la DB.

---

### Task 1: Repo, fork vendorizado y build Docker

**Files:**
- Create: `xiaozhi-esp32/.gitignore`, `server/xiaozhi-server-go/` (copia de `/tmp/claude-1000/-home-johnny-Escritorio-proyectos-music-bot/f1f4a41d-a2bc-4641-923f-728e919b435c/scratchpad/xsg` sin `.git`; si el scratchpad ya no existe, `git clone` upstream y `git checkout 0b1575cb`)
- Modify: `server/xiaozhi-server-go/Dockerfile` (reemplazo completo)

**Interfaces:**
- Produces: imagen que, con `WORKDIR /app`, corre `/app/xiaozhi-server` y contiene `/app/web`, `/app/music`, `/app/config.yaml` upstream. Binario linkeado contra opus/opusfile/sqlite.

- [ ] **Step 1:** `git init` en `xiaozhi-esp32/`, crear `.gitignore` con `server/.env`, `**/config.db`, `**/.config.yaml`, `**/logs/`, `**/tmp/`, `server/xiaozhi-server-go/ota_bin/*.bin`.
- [ ] **Step 2:** Copiar el fork; anotar commit upstream `0b1575cb` en `server/xiaozhi-server-go/UPSTREAM.md` (una línea + URL).
- [ ] **Step 3:** Correr los tests upstream tal cual en un contenedor golang:
  Run: `docker run --rm -v $PWD/server/xiaozhi-server-go:/src -w /src golang:1.24-alpine sh -c "apk add --no-cache gcc musl-dev pkgconf opus-dev opusfile-dev && go test ./src/httpsvr/ota/... ./src/configs/... ./src/core/utils/..."`
  Expected: PASS (línea base; si algo falla upstream, anotarlo en UPSTREAM.md y seguir).
- [ ] **Step 4:** Escribir Dockerfile multi-stage: builder `golang:1.24-alpine` con `gcc musl-dev pkgconf opus-dev opusfile-dev`, `CGO_ENABLED=1 go build -o /out/xiaozhi-server ./src`; runtime `alpine:3.20` con `ca-certificates sqlite-libs opus opusfile gettext tzdata`, copia binario, `web/`, `music/`, `config.yaml`.
- [ ] **Step 5:** `docker build -t xiaozhi-server:dev server/xiaozhi-server-go` → Expected: build OK; `docker run --rm xiaozhi-server:dev ls /app` muestra `xiaozhi-server web music config.yaml`.
- [ ] **Step 6:** Commit `chore: vendor xiaozhi-server-go 0b1575cb + multi-stage Dockerfile` (incluye spec y este plan).

### Task 2: OTA privado (log del body, nunca firmware, TZ Chile)

**Files:**
- Modify: `src/httpsvr/ota/handler.go` (`handlePostOTA`, líneas ~224-276)
- Test: `src/httpsvr/ota/ota_privacy_test.go` (mismo setup que `onboarding_binding_test.go`: gin test mode, sqlite en memoria, swap de `database.DB` y `utils.DefaultLogger` con `t.Cleanup`)

**Interfaces:**
- Produces: `POST /api/ota/` con header `device-id` responde `{server_time:{timestamp, timezone_offset:-180}, firmware:{version:<versión del equipo>, url:""}, websocket:{url: configs.Cfg.Web.Websocket}}`, sin claves `activation` ni `mqtt`. Log INFO `[OTA] body device=<id> raw=<json crudo>`.

- [ ] **Step 1: Tests que fallan**

```go
func TestOTALogsRawBody(t *testing.T)            // body con "board":{"type":"esp32-muma-a"} → el log capturado contiene `esp32-muma-a` y `"partition_table"`
func TestOTANeverOffersFirmwareEvenWithBin(t *testing.T) // crea ota_bin/9.9.9.bin en t.TempDir() (chdir), POST con application.version "2.4.2" → firmware.url == "" && firmware.version == "2.4.2"
func TestOTATimezoneChile(t *testing.T)          // server_time.timezone_offset == -180
func TestOTANoActivationNoMQTT(t *testing.T)     // JSON de respuesta no tiene claves "activation" ni "mqtt"
func TestOTAMissingDeviceID(t *testing.T)        // sin header → 400 (comportamiento upstream preservado)
```
Para capturar el log: logger apuntando a un `bytes.Buffer` o a un archivo en `t.TempDir()`; usar lo que ya permita `utils.NewLogger`.

- [ ] **Step 2:** Run `go test ./src/httpsvr/ota/ -run 'TestOTA' -v` (en el contenedor del Task 1 Step 3) → Expected: FAIL en LogsRawBody, NeverOffers, Timezone.
- [ ] **Step 3:** En `handlePostOTA`: leer el body crudo una vez (`io.ReadAll` + reponer `c.Request.Body`), loguearlo; quitar la búsqueda en `ota_bin/` (responder siempre versión del equipo y url vacía); `TimezoneOffset: -180`. Dejar `GET /ota_bin/*` registrado pero no se usa.
- [ ] **Step 4:** Run mismo comando → Expected: PASS, y `go test ./src/httpsvr/...` sigue verde.
- [ ] **Step 5:** Commit `feat(ota): log raw body, never offer firmware, Chile TZ`.

### Task 3: Config template + entrypoint

**Files:**
- Create: `server/xiaozhi/config.template.yaml`, `server/xiaozhi/entrypoint.sh`

**Interfaces:**
- Consumes: imagen del Task 1 (tiene `envsubst`).
- Produces: contenedor que arranca configurado en el primer intento. Variables usadas: `${LAN_IP}`, `${LITELLM_MASTER_KEY}`.

Template = `config.yaml` upstream con estos cambios (el resto igual):
- `server.auth.enabled: false` (el firmware stock no manda token propio; LAN cerrada).
- `transport.websocket.port: 8000`, `web.port: 8080`.
- `web.websocket: ws://${LAN_IP}:8100/xiaozhi/v1/` (el transport acepta cualquier ruta).
- `web.vision`: `http://${LAN_IP}:8180/api/vision`.
- `log.log_level: DEBUG` mientras dure el Hito 1; `INFO` desde el Task 8.
- `prompt`: persona corta en español chileno, respuestas de 1-3 frases, sin markdown ni emojis, sin listas.
- `quick_reply`: frases en español ("¡Hola!", "Dime"), o deshabilitado si la clave lo permite. `CMD_exit: ["chao", "adiós", "nos vemos", "hasta luego"]`.
- `delete_audio: true`, `save_tts_audio: false`.
- Hito 1: `selected_module` queda con lo que upstream trae (ASR/TTS no se usan aún). LLM `openai` → `url: http://litellm:4000/v1`, `model_name: deepseek-chat`, `api_key: ${LITELLM_MASTER_KEY}`.

- [ ] **Step 1:** `entrypoint.sh`: `set -eu`; `envsubst '${LAN_IP} ${LITELLM_MASTER_KEY}' < /config/config.template.yaml > /app/.config.yaml`; `chmod 600`; si `/app/.config.yaml` contiene `${` → salir con error claro; `exec /app/xiaozhi-server`.
- [ ] **Step 2:** Verificar: `docker run --rm -e LAN_IP=1.2.3.4 -e LITELLM_MASTER_KEY=sk-x -v $PWD/server/xiaozhi:/config --entrypoint sh xiaozhi-server:dev -c '/config/entrypoint.sh & sleep 3; grep -c "1.2.3.4" /app/.config.yaml'` → Expected: ≥1 y en los logs no aparece `首次启动已创建私有配置文件` (ErrInitialConfigCreated).
- [ ] **Step 3:** Commit `feat(server): config template + entrypoint`.

### Task 4: Compose (solo xiaozhi) + Hito 1 con el equipo real

**Files:**
- Create: `server/docker-compose.yml`, `server/.env.example`

**Interfaces:**
- Produces: servicio `xiaozhi` en red `xiaozhi-net`, puertos `${LAN_IP}:8100:8000` y `${LAN_IP}:8180:8080`, monta `./xiaozhi:/config:ro`, `entrypoint: ["/config/entrypoint.sh"]`, `env_file: .env`, `restart: unless-stopped`, `environment: TZ=America/Santiago`.

- [ ] **Step 1:** Escribir compose y `.env.example` (las 4 variables, valores placeholder). El usuario crea `server/.env` real (no lo escribo yo con claves).
- [ ] **Step 2:** `docker compose up -d --build xiaozhi`; `ss -ltnp | grep -E '8100|8180'` → Expected: escuchando solo en 192.168.100.13.
- [ ] **Step 3:** Smoke OTA desde el host: `curl -s -X POST http://192.168.100.13:8180/api/ota/ -H 'device-id: aa:bb:cc:dd:ee:ff' -H 'Content-Type: application/json' -d '{"application":{"version":"2.4.2"},"board":{"type":"esp32-muma-a"}}'` → Expected: JSON con `websocket.url` = `ws://192.168.100.13:8100/xiaozhi/v1/`, `firmware.url` vacío, sin `activation`.
- [ ] **Step 4:** Recreate: cambiar `CMD_exit` en el template, `docker compose up -d --force-recreate xiaozhi`, ver en el log de config cargada que tomó el cambio (Review Focus 1). Revertir.
- [ ] **Step 5 (usuario + yo):** El usuario entra al portal Wi-Fi (192.168.4.1 → Avanzado → "URL OTA personalizada") y pone `http://192.168.100.13:8180/api/ota/`. Yo sigo `docker compose logs -f xiaozhi`.
- [ ] **Step 6:** Registrar en `server/docs/hito1.md`: body OTA crudo completo, mensaje `hello` del WebSocket (sobre todo `audio_params`: formato, `sample_rate`, `frame_duration`), headers del upgrade, qué muestra la pantalla.
- [ ] **Step 7: GO / NO-GO.** GO si: el equipo hace POST al OTA, abre el WebSocket, manda `hello` y llegan frames de audio al apretar/hablar. Si no: hasta ~1 día de parches (headers, ruta, versión de protocolo) y si no sale → parar y ofrecer al usuario enfoque B (xinnan-tech) o abrir la carcasa. **No seguir al Task 5 sin GO.**
- [ ] **Step 8:** Commit `feat(server): compose for xiaozhi + milestone 1 notes`.

### Task 5: Provider ASR `openai` (Speaches)

**Files:**
- Create: `src/core/providers/asr/openai/openai.go`, `wav.go`, `openai_test.go`
- Modify: `src/main.go` (blank import `_ "xiaozhi-server-go/src/core/providers/asr/openai"`)

**Interfaces:**
- Consumes: `asr.Provider`, `asr.NewBaseProvider`, `InitAudioProcessing`, `asr.Register`, `listener.OnAsrResult(text string, isFinal bool) bool`.
- Produces: `asr.Register("openai", NewProvider)`; `func NewProvider(config *asr.Config, deleteFile bool, logger *utils.Logger) (asr.Provider, error)`; `func pcmToWAV(pcm []byte, sampleRate, channels int) []byte` (header de 44 bytes, 16-bit LE).
- Config (`config.Data`): `base_url` (req.), `api_key` (opcional), `model` (default `Systran/faster-whisper-small`), `language` (default `es`), `prompt`, `timeout` (seg, default 15), `sample_rate` (default 16000, igualar al `audio_params.sample_rate` del Hito 1).

Comportamiento: `AddAudio` acumula PCM en un buffer protegido por mutex. `SendLastAudio(data)` agrega `data`, arma WAV, hace POST multipart a `{base_url}/v1/audio/transcriptions` (`file`, `model`, `language`, `prompt`, `response_format=json`), y llama `OnAsrResult(text, true)` desde una goroutine (no bloquear el loop de mensajes). Buffer vacío → `OnAsrResult("", true)` sin HTTP. Texto vacío y silencio largo → incrementar `SilenceCount` como stepfun (`SilenceTime() > 30s`), si no `SilenceCount = 0`. `Reset` vacía el buffer. Patrón y métodos restantes: copiar de `asr/stepfun`.

- [ ] **Step 1: Tests que fallan** (httptest.Server falso de Speaches)

```go
func TestPCMToWAVHeader(t *testing.T)                 // len == 44+len(pcm); "RIFF","WAVE","fmt ","data"; sampleRate 16000 en offset 24; bitsPerSample 16
func TestTranscribeSendsMultipart(t *testing.T)       // server verifica path /v1/audio/transcriptions, campos model/language/prompt, file empieza con "RIFF"; responde {"text":"hola po"} → listener recibe ("hola po", true)
func TestSendLastAudioEmptyBufferNoRequest(t *testing.T) // 0 bytes → server no recibe requests, listener recibe ("", true)
func TestEmptyTranscriptReportsEmpty(t *testing.T)    // server responde {"text":""} → listener ("", true), sin panic
func TestTranscribeNon200(t *testing.T)               // 503 → error logueado, listener ("", true), buffer reseteado
func TestTranscribeTimeout(t *testing.T)              // server duerme 2s, timeout=1 → listener ("", true) en <1.5s
func TestResetClearsBuffer(t *testing.T)              // AddAudio, Reset, SendLastAudio(nil) → sin request
func TestNewProviderRequiresBaseURL(t *testing.T)     // sin base_url → error
```
Listener falso con canal para esperar el resultado asíncrono.

- [ ] **Step 2:** Run `go test ./src/core/providers/asr/openai/ -v` → Expected: FAIL (paquete no existe).
- [ ] **Step 3:** Implementar `openai.go` + `wav.go` según arriba; blank import en `main.go`.
- [ ] **Step 4:** Run → Expected: PASS; `go build ./src` OK.
- [ ] **Step 5:** Commit `feat(asr): OpenAI-compatible batch ASR provider`.

### Task 6: Provider TTS `openai` (Speaches/Piper, mp3)

**Files:**
- Modify: `src/core/providers/tts/tts.go` (Config), `src/configs/config.go` (`TTSConfig`) — agregar `BaseURL string \`yaml:"base_url"\``, `APIKey \`yaml:"api_key"\``, `Model \`yaml:"model"\``, `Speed float64 \`yaml:"speed"\``, `ResponseFormat \`yaml:"response_format"\``, `Timeout int \`yaml:"timeout"\``; y copiarlos donde se convierte `configs.TTSConfig` → `tts.Config` (buscar con codegraph_callers de `tts.Create`).
- Create: `src/core/providers/tts/openai/openai.go`, `openai_test.go`
- Modify: `src/main.go` (blank import)

**Interfaces:**
- Produces: `tts.Register("openai", NewProvider)`; `func NewProvider(config *tts.Config, deleteFile bool) (tts.Provider, error)`; `ToTTS(text string) (string, error)` devuelve la ruta `OutputDir/openai_tts_<nanos>.mp3`.
- Defaults: `model: speaches-ai/piper-es_MX-claude-high`, `voice: es_MX-claude-high` (verificar ids exactos con `GET /v1/models` de Speaches en Task 7 y ajustar el template, no el código), `response_format: mp3` (siempre mp3: `AudioToOpusData` remuestrea mp3 a 24 kHz pero no WAV de 22050), `speed: 1.0`, `timeout: 20`.

- [ ] **Step 1: Tests que fallan**

```go
func TestToTTSPostsSpeechRequest(t *testing.T) // server verifica POST /v1/audio/speech, JSON {model, voice, input:"hola", response_format:"mp3", speed:1}; responde bytes mp3 fijos → archivo existe en OutputDir, termina en .mp3, contenido idéntico
func TestToTTSForcesMP3(t *testing.T)          // response_format "wav" en config → request lleva "mp3"
func TestToTTSNon200(t *testing.T)             // 500 → error, no queda archivo
func TestToTTSTimeout(t *testing.T)            // timeout 1s, server 2s → error en <1.5s
func TestToTTSEmptyText(t *testing.T)          // "" o solo espacios → error sin request
func TestTTSConfigYAMLFields(t *testing.T)     // en configs: yaml con base_url/model/speed se parsea a TTSConfig
```

- [ ] **Step 2:** Run `go test ./src/core/providers/tts/openai/ ./src/configs/ -v` → Expected: FAIL.
- [ ] **Step 3:** Implementar provider y campos; `Authorization: Bearer` solo si `api_key != ""`.
- [ ] **Step 4:** Run → Expected: PASS; `go build ./src` OK; `go test ./src/...` sin regresiones respecto a la línea base del Task 1.
- [ ] **Step 5:** Commit `feat(tts): OpenAI-compatible TTS provider (mp3)`.

### Task 7: Speaches + LiteLLM + Postgres en compose

**Files:**
- Modify: `server/docker-compose.yml`, `server/xiaozhi/config.template.yaml`, `server/.env.example`
- Create: `server/litellm-config.yaml`

**Interfaces:**
- Consumes: providers `openai` de Tasks 5 y 6.
- Produces: `http://speaches:8000` y `http://litellm:4000` en `xiaozhi-net`.

Compose:
- `speaches`: imagen CPU oficial (`ghcr.io/speaches-ai/speaches:latest-cpu`, fijar el tag concreto que resulte en el momento), sin puertos, `cpus: "4"`, volumen `speaches-models:/home/ubuntu/.cache/huggingface/hub` (verificar ruta en la doc de la imagen), `WHISPER__COMPUTE_TYPE=int8`, sin telemetría.
- `litellm`: `ghcr.io/berriai/litellm` con tag fijo, `${LAN_IP}:8140:4000`, monta `litellm-config.yaml`, `DATABASE_URL=postgresql://litellm:${LITELLM_DB_PASSWORD}@litellm-db:5432/litellm`, `LITELLM_MASTER_KEY`, `DEEPSEEK_API_KEY`, `STORE_MODEL_IN_DB=False`.
- `litellm-db`: `postgres:16-alpine`, volumen `litellm-db-data`, sin puertos.
- `xiaozhi`: `depends_on` speaches y litellm.

`litellm-config.yaml`: modelo `deepseek-chat` → `deepseek/deepseek-chat`, `api_key: os.environ/DEEPSEEK_API_KEY`, `input_cost_per_token` / `output_cost_per_token` con precios vigentes de DeepSeek (verificar en su página de pricing al implementar y anotar la fecha en un comentario); `litellm_settings: {turn_off_message_logging: true, telemetry: false}`; `general_settings: {master_key: os.environ/LITELLM_MASTER_KEY, disable_spend_logs: false}`.

Template: `selected_module: {ASR: openai, TTS: openai, LLM: openai}`; bloque `ASR.openai` (type openai, base_url `http://speaches:8000`, model, language `es`, prompt de vocabulario chileno: "po, cachai, wena, al tiro, fome, bacán, luca, pololo", sample_rate del Hito 1); `TTS.openai` (type openai, base_url, model, voice, response_format mp3, speed 1.0, output_dir `tmp/`).

- [ ] **Step 1:** Levantar `docker compose up -d speaches`, precargar modelos (`POST /v1/models/<id>` desde un contenedor en la red) y listar `GET /v1/models` → anotar ids exactos y ajustar template.
- [ ] **Step 2:** Smoke STT/TTS desde la red interna: `docker compose exec xiaozhi wget -qO- ...` o contenedor curl temporal: TTS "Hola, ¿cómo estái?" → mp3 no vacío; ese mp3 → STT → texto parecido.
- [ ] **Step 3:** `docker compose up -d litellm litellm-db`; `curl http://192.168.100.13:8140/v1/chat/completions -H "Authorization: Bearer $LITELLM_MASTER_KEY" ...` → respuesta de DeepSeek; `GET /spend/logs` muestra tokens y costo y **no** el contenido del mensaje.
- [ ] **Step 4:** `docker compose up -d --force-recreate xiaozhi`; log muestra providers `openai` creados sin error.
- [ ] **Step 5:** Commit `feat(server): speaches + litellm stack`.

### Task 8: End-to-end, latencia, privacidad y reinicio

**Files:**
- Create: `server/docs/latency.md`, `server/README.md` (cómo levantar, cambiar voz, cambiar config = editar template + recreate, rotar claves)
- Modify: `server/xiaozhi/config.template.yaml` (`log_level: INFO`)

- [ ] **Step 1:** Conversación real con el equipo (5 turnos en español chileno). Criterio: entiende, responde con voz Piper, sin markdown leído en voz alta.
- [ ] **Step 2:** Latencia: con logs DEBUG, para 5 turnos registrar timestamps de fin de habla (listen stop) → texto ASR → primer token LLM → primer audio TTS enviado. Tabla en `latency.md` con mediana y peor caso, y CPU de Speaches durante el turno (`docker stats`). Si mediana > 3 s: reportar dónde se va el tiempo y opciones, **sin** cambiar modelos/CPU sin preguntar.
- [ ] **Step 3:** Privacidad (criterio 5): durante 3 turnos, `sudo tcpdump -ni any 'not net 192.168.0.0/16 and not net 172.16.0.0/12 and not net 10.0.0.0/8'` en el host → solo tráfico a `api.deepseek.com` (resolver IPs antes); y desde la IP del equipo (`host <ip-equipo>`) nada fuera de la LAN. Anotar resultado.
- [ ] **Step 4:** Volver a `log_level: INFO`, recreate. Verificar que el log ya no tiene transcripciones ni respuestas a nivel INFO (si upstream loguea `[ASR] [识别结果...]` en INFO, bajarlo a DEBUG en el fork, con test si es trivial; si no, anotarlo).
- [ ] **Step 5:** Reinicio (criterio 6): `sudo reboot` (coordinar con el usuario por Frigate/Nextcloud). Tras boot: todos los contenedores `Up`, Speaches no re-descarga (logs), el equipo reconecta solo y conversa.
- [ ] **Step 6:** Verificar Frigate sigue normal (`docker stats` durante una conversación: ningún pico sostenido que lo afecte).
- [ ] **Step 7:** `graphify update .` si aplica; commit `docs: latency, privacy check, README`.
