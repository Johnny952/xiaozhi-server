# ASR (voz a texto): configuración actual, mediciones y plan B

## Configuración actual

- **Proveedor:** `SpeachesASR` en `server/xiaozhi/config.template.yaml`. Es de tipo `openai` y apunta a speaches 0.8.3-cpu (faster-whisper, int8).
- **Vocabulario:** el campo `prompt` le pasa a Whisper las palabras del dominio: Weita, Nextcloud, bitácora, los nombres de proyectos y algunos chilenismos. Es lo que más mejora la precisión. Cuando aparezca un proyecto o una palabra que transcriba mal, agréguela ahí.
- **Modelo:** `Systran/faster-whisper-small` (elegido por latencia; ver mediciones). El `model` del config tiene que coincidir con la línea `MODELS` de `server/speaches/preload-entrypoint.sh` y con el healthcheck de speaches en `server/docker-compose.yml`.

## Mediciones (2026-11-18)

- **Equipo:** contenedor speaches local, CPU.
- **Audio:** unos 9 s de TTS limpio con esta frase: *"Weita, anota en la bitácora de joyería que mañana tengo que llamar al proveedor, y crea una tarea en mesa libre para revisar el bot de ópticas."*

| Modelo + prompt | Tiempo | Resultado |
|---|---|---|
| `Systran/faster-whisper-small` + prompt antiguo (solo chilenismos) | ~2,7 s | "**hueita**, anota, en la **vitácora**…" |
| `Systran/faster-whisper-small` + vocabulario de dominio | ~3,0 s | Correcto |
| `deepdml/faster-whisper-large-v3-turbo-ct2` + vocabulario de dominio | ~10,5 s | Correcto |

RAM con los dos modelos cargados: unos 2,7 GiB.

**Conclusión:** el vocabulario es lo que corrige los errores. En CPU, turbo cuadruplica la latencia. Con audio limpio no mostró ninguna ventaja; con voz real, ruido o frases largas todavía falta medirlo.

## Plan B: ASR de DeepSeek

Es para el caso de que small con vocabulario no alcance y la CPU no aguante turbo.

### Qué ofrece

Fuentes, consultadas el 2026-11-18: <https://api-docs.deepseek.com/guides/asr> y <https://api-docs.deepseek.com/quick_start/pricing/>

- **Endpoint:** `POST https://api.deepseek.com/v1/tools/asr`, con `Authorization: Bearer <DEEPSEEK_API_KEY>`. El cuerpo es multipart con un único campo, `file`.
- **Respuesta:** `{ "request_id", "text", "duration_ms" }`.
- **Idioma:** se detecta solo, sin parámetro. El español (`es`) está soportado.
- **Audio:** WAV PCM de 16 bits entre 8 y 48 kHz (16 kHz sirve) u Ogg Opus. Si el audio es estéreo, usa solo el canal izquierdo.
- **Límites:** 120 s y 8 MiB como máximo por archivo. No hay streaming.
- **Errores:**
  - 400: petición inválida.
  - 408 y 504: timeout.
  - 413: archivo demasiado grande.
  - 429: más de 50 peticiones en curso.
  - 503: servicio no disponible.
- **Precio:** 0,116 USD por hora de audio (en promoción, 0,058 USD), cobrado por segundo.

### Qué implicaría usarlo

1. **Un proveedor nuevo en el fork Go.** No es compatible con `/v1/audio/transcriptions` de OpenAI, así que el proveedor `openai` (`xiaozhi-server-go/src/core/providers/asr/openai/openai.go`) no sirve. Hay que crear un tipo, por ejemplo `deepseek`, que:
   - envuelva el PCM en WAV de 16 kHz;
   - suba el archivo a `/v1/tools/asr`;
   - lea `text`.
   Se pueden reutilizar el VAD y la lógica de acumulación del proveedor `openai`.
2. **Se pierde el vocabulario.** La API no acepta `prompt`, justo lo que más ayudó en las mediciones. Tampoco se puede fijar el idioma.
3. **Privacidad:** la voz sale del servidor y va a DeepSeek (China). La documentación no dice nada de retención ni de región.
4. **Clave:** puede reutilizar la `DEEPSEEK_API_KEY` que ya usa LiteLLM. Habría que pasársela también al servicio `xiaozhi`.
5. **Costo:** despreciable para uso personal. Además libera la CPU y la RAM que hoy consume speaches.
6. **Latencia:** no está publicada. Hay que medirla, porque incluye el viaje por internet y no hay streaming.
7. **Disponibilidad:** pasa a depender de internet y de los límites de DeepSeek. Conviene dejar `SpeachesASR` configurado como respaldo.

### Cuándo activarlo

Solo si se cumplen las tres condiciones:

- small con vocabulario sigue fallando con voz real;
- turbo es demasiado lento en el hardware de producción;
- se acepta que el audio salga a un tercero.

Antes de integrarlo, medir la precisión y la latencia con un `curl` de prueba sobre audios reales.
