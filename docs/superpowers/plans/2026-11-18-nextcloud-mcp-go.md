# Nextcloud MCP (Go) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que weita sea una secretaria por voz sobre el Nextcloud propio. Un servidor MCP en Go con 19 herramientas de dominio (bitácora, agenda, tareas, notas, estado de proyecto, lectura de archivos) que solo agregan. Borrar, cancelar y cambiar pasan por una confirmación de dos pasos, que una guardia en el fork vuelve a validar.

**Architecture:** Binario estático `nextcloud-mcp` (módulo propio en `server/nextcloud-mcp/`), que el fork lanza por stdio vía `.mcp_server_settings.json`.
- Habla WebDAV, CalDAV y la API de Notes con `net/http`.
- La lógica pura (iCal, normalización y matching, formato de bitácora, store de confirmaciones) va en paquetes sin red. Las herramientas se prueban contra un Nextcloud falso hecho con `httptest`.
- En el fork, `confirm_guard.go` habilita un único `mcp_confirmar` solo si la siguiente frase del usuario lo autoriza.

**Tech Stack:** Go 1.24, stdlib + `github.com/mark3labs/mcp-go v0.29.0`, `CGO_ENABLED=0`, `encoding/xml`, `time/tzdata`; Docker multi-stage existente (`server/Dockerfile`).

**Spec:** `docs/superpowers/specs/2026-11-18-nextcloud-mcp-go-design.md`. Es la fuente de verdad del texto de las respuestas, los formatos de archivo y las reglas de cada herramienta. Este plan cita la línea de la spec en vez de repetirla.

## Global Constraints

- Ninguna herramienta reescribe ni borra contenido existente de archivos ni de notas. Solo agregan al final. Los únicos borrados son `evento_cancelar` y `tarea_borrar`, y solo después de `confirmar`.
- Ningún audio ni texto sale a servicios externos salvo DeepSeek. El MCP solo habla con `NEXTCLOUD_URL`.
- Credenciales solo por env (`NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD`, una app password nueva). Nunca se loguean, nunca van en respuestas ni en URLs devueltas. stdout es el canal MCP; todo log va a stderr.
- Si falta alguna `NEXTCLOUD_*`, el binario sale con código ≠0 y un mensaje en stderr. xiaozhi sigue arrancando y la voz queda intacta.
- Las respuestas están en español, en una línea cuando es error. Muestran máximo 10 ítems más "y N más", y el contenido hasta unos 2000 caracteres con nota de recorte. No llevan XML, iCal ni JSON (spec l266).
- Fechas de entrada en ISO 8601 local. La zona es `TZ` (America/Santiago) y se embebe `time/tzdata`.
- HTTP: timeout de 15 s y máximo 4 peticiones paralelas por herramienta.
- UIDs con la forma `<unixnano>@xiaozhi-nextcloud`.
- Tests del módulo, desde `server/nextcloud-mcp`:
  `docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/gomod -v "$HOME/.cache/nc-mcp-gomod":/gomod -v "$PWD":/src -w /src golang:1.24-alpine go test ./...`
  (abajo, `GOTEST`).
- Tests del fork, desde `server/xiaozhi-server-go`:
  `docker run --rm -v "$PWD":/src -w /src golang:1.24-alpine sh -c 'apk add -q gcc musl-dev pkgconf opus-dev opusfile-dev && go test ./src/core/ -run ConfirmGuard -v'`
  (abajo, `FORKTEST`).
- No hay Go local y no puede quedar ningún contenedor corriendo: siempre `docker run --rm`.
- Seguridad del repo:
  - Nunca hacer `cat` de `server/.env` ni de otros `.env`.
  - Escanear el diff en busca de secretos antes de cada push.
  - `server/xiaozhi/config.template.yaml` tiene un cambio del usuario sin commitear; solo se toca en el Task 12.
- Commits con este trailer:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_013Xj2arizKA9o5MSF2MZLMQ
  ```
- Después de cambiar código: `graphify update .` desde `/home/johnny/Escritorio/proyectos/music-bot`.

## Review Focus

1. **Negación junto a una palabra de afirmación.** Ejemplos: "no, no lo borres", "ok no", "mejor no, déjalo". Esperado: no habilita `confirmar`. Decir los 4 dígitos del código sí lo habilita. Test en Task 10: `TestConfirmGuardNegationBlocks`.
2. **`codigo` llega como número JSON** (`4821` → `float64`, o `"4821"`, o `"04821"` mal formado). Esperado: `4821` y `"4821"` autorizan; cualquier otra cosa se rechaza sin pánico. En el MCP pasa igual. Tests: Task 10 `TestConfirmGuardNumericCode` y Task 8 `TestConfirmarAcceptsNumber`.
3. **Auto-confirmación en la misma cadena:** el LLM recibe `PENDIENTE:` y llama `confirmar` sin que el usuario hable. Esperado: "Rechazado: el usuario no confirmó", sin reenviarlo al MCP. Test en Task 10: `TestConfirmGuardSelfConfirmRejected`.
4. **Código vencido o reusado:** se confirma a los 121 s, o se llama `confirmar` dos veces con el mismo código. Esperado: no ejecuta, responde en una línea que el código venció o no existe y pide repetir la orden. Test en Task 7: `TestStoreExpiresAndSingleUse`.
5. **Título ambiguo o transcripción imperfecta.** Ejemplo: "alambre" coincide con "Comprar alambre" y "Alambre de cobre". Esperado: lista los dos (título y fecha) y no hace nada, ni siquiera emite un PENDIENTE. Tests: Task 5 `TestFindAmbiguous` y Task 8 `TestTareaBorrarAmbiguousNoPending`.

---

### Task 1: Validar tool calling de DeepSeek vía LiteLLM

**Files:** ninguno, salvo `server/litellm/litellm-config.yaml` si falla.

- [ ] **Step 1:** Mandar un POST a `http://192.168.100.13:8140/v1/chat/completions` con `model: deepseek-chat`, una tool `agenda` (`desde`, `hasta` string) y el mensaje "¿qué tengo mañana?".
  - Pasar la key como `-H "Authorization: Bearer $(cat <scratchpad>/.mk)"` dentro del comando. Nunca imprimirla.
  - Esperado: `choices[0].message.tool_calls[0].function.name == "agenda"`, con `arguments` como JSON válido.
- [ ] **Step 2:** Repetir con un historial donde el tool result es `PENDIENTE: pregunta al usuario "¿Borro la tarea 'X'?". Solo si responde que sí en su próximo mensaje, llama confirmar con codigo=4821.`.
  - Esperado: el modelo responde con texto (una pregunta) y **no** emite tool call de `confirmar`.
- [ ] **Step 3:** Si el Step 1 o el 2 falla, cambiar el alias `deepseek-chat` a otro modelo DeepSeek sin razonamiento en `litellm-config.yaml`, repetir y commitear `fix(litellm): deepseek model with working tool calls`. Si pasa, anotar el resultado para el doc del Task 13. No hay commit.

### Task 2: Andamiaje del módulo y cliente HTTP WebDAV

**Files:**
- Create: `server/nextcloud-mcp/go.mod` (`module nextcloud-mcp`, `go 1.24`, require `github.com/mark3labs/mcp-go v0.29.0`), `go.sum`
- Create: `server/nextcloud-mcp/nc/client.go`, `nc/webdav.go`
- Test: `server/nextcloud-mcp/nc/client_test.go`, `nc/fake_test.go`
- Create: `server/nextcloud-mcp/nctest/fake.go`: Nextcloud falso, exportado para que lo usen también `tools/`.

**Interfaces:**
- Produces:
  - `nc.New(baseURL, user, pass string) *Client`. Tiene campos `HTTP *http.Client` (timeout de 15 s) y `LockWait time.Duration` (2 s; los tests lo bajan a 1 ms).
  - `ErrNotFound`, `ErrConflict` (412 que persiste después de los reintentos), `ErrLocked`.
  - `(c) Get(ctx, path string) (body []byte, etag string, err error)`: archivos bajo `/remote.php/dav/files/{user}`. `path` empieza con `/`.
  - `(c) Put(ctx, path string, body []byte, etag string) (newETag string, err error)`.
    - Con `etag == ""` manda `If-None-Match: *`; si no, `If-Match: etag`.
    - Ante 423, espera `LockWait` y reintenta una vez.
  - `(c) Append(ctx, path string, build func(old []byte, exists bool) ([]byte, error)) error`.
    - Hace GET (404 cuenta como `exists=false`), `build` y PUT condicional.
    - Ante 412 repite el ciclo completo hasta 2 veces más y después devuelve `ErrConflict`.
  - `(c) MkdirAll(ctx, dir string) error`: MKCOL de cada segmento; 405 cuenta como OK.
  - `(c) List(ctx, dir string) ([]Entry, error)`: PROPFIND Depth 1, sin la propia carpeta.
  - `(c) SearchName(ctx, name string) ([]Entry, error)`: DAV `SEARCH` en `/remote.php/dav/` con `d:displayname like %name%`, limitado a 20.
  - `Entry{Path, Name string; Dir bool; Size int64; Modified time.Time}`, con `Path` relativo a la raíz del usuario.
  - `nctest.New(t, user) *Fake`: tiene `URL`, `Files map[string][]byte` y ETags. Expone hooks para los tests: `FailNextPut(code int)` y `MutateBeforeNextPut(path string, body []byte)` (simula una escritura concurrente). Los Tasks 4 y 8 le agregan CalDAV y Notes.

- [ ] **Step 1: Tests que fallan** en `client_test.go`, contra `nctest.Fake`:
  - `TestGetPutETag`: Put nuevo manda `If-None-Match: *`. Put con ETag viejo da 412 y devuelve `ErrConflict` (sin reintentos en `Put`).
  - `TestAppendRetriesOn412`: con `MutateBeforeNextPut` una vez, el resultado contiene la mutación más lo agregado. Con 3 mutaciones seguidas devuelve `ErrConflict` y el archivo queda con la última mutación intacta.
  - `TestPut423RetriesOnce`.
  - `TestMkdirAllTolerates405`.
  - `TestListParsesPropfind`: fixture con 1 carpeta, 2 archivos y nombres con espacios y `%20`.
  - `TestBasicAuthAndNoCredsInErrors`: ningún `error.Error()` contiene la password ni `user:pass@`.
- [ ] **Step 2:** `GOTEST` → FAIL (no compila).
- [ ] **Step 3:** Implementar `client.go`, `webdav.go` y el `Fake` (WebDAV: GET, PUT con If-Match e If-None-Match, MKCOL, PROPFIND Depth 1, SEARCH por substring de nombre).
  - El ETag es el hash del contenido, entre comillas.
  - Los paths se escapan por segmento con `url.PathEscape`.
- [ ] **Step 4:** `GOTEST` → PASS.
- [ ] **Step 5:** Commit `feat(nextcloud-mcp): module scaffold + WebDAV client with ETag/If-Match`.

### Task 3: Paquete iCal

**Files:** Create `server/nextcloud-mcp/ical/ical.go`, `ical/vtimezone.go`; Test `ical/ical_test.go`

**Interfaces:**
- Produces:
  - `Field(ics, name string) (value string, params map[string]string, ok bool)`: el primer componente que tiene la propiedad. Desdobla líneas (CRLF + espacio o tab) y des-escapa `\n \, \; \\`.
  - `SetVTodoField(ics, name, value string) string`: reemplaza o agrega dentro de VTODO, escapa y pliega a 75 octetos.
  - `type Todo struct{UID, Summary, Description, Status, ParentUID string; Due *time.Time; DueIsDate bool; Priority int; Completed *time.Time}`
  - `MakeVTodo(t Todo, now time.Time) string`: incluye `DTSTAMP`, `CREATED` y `RELATED-TO;RELTYPE=PARENT:` si `ParentUID != ""`. Un `DUE` de solo fecha va como `VALUE=DATE`.
  - `ParseVTodo(ics string, loc *time.Location) Todo`
  - `type Event struct{UID, Summary, Location, Description, RRule string; Start, End time.Time; AllDay bool}`
  - `MakeVEvent(e Event, loc *time.Location, now time.Time) string`. Escribe `DTSTART;TZID=<loc>` más el bloque VTIMEZONE. Un evento de todo el día usa `VALUE=DATE` con DTEND al día siguiente. Copia `RRULE` tal cual.
  - `ParseVEvents(ics string, loc *time.Location) []Event`: un objeto expandido trae varios VEVENT. Acepta horas floating, `Z` y `TZID=` (vía `time.LoadLocation`, con fallback a `loc`).
  - `ToUTC(t) string` (`20060102T150405Z`) y `ToDate(t) string` (`20060102`).
  - `VTimezone(loc) (string, bool)`: devuelve el bloque fijo de `America/Santiago` (STANDARD -04 desde el primer domingo de abril; DAYLIGHT -03 desde el primer domingo de septiembre; `RRULE:FREQ=YEARLY;BYMONTH=4|9;BYDAY=1SU`). Para cualquier otra zona da `false`, y entonces `MakeVEvent` escribe en UTC (`Z`) sin TZID.

- [ ] **Step 1: Tests que fallan:**
  - `TestFieldUnfoldsAndParams`: SUMMARY plegado en 3 líneas; `DTSTART;TZID=America/Santiago:20261121T150000` da params TZID.
  - `TestSetVTodoFieldReplaceAndAppend`.
  - `TestMakeVTodoRoundTrip`: con Parent, Due de fecha y prioridad 1.
  - `TestMakeVEventTZIDAndVTimezone`: contiene `BEGIN:VTIMEZONE` y `TZID:America/Santiago`; el round-trip con `ParseVEvents` da la misma hora local.
  - `TestAllDayEndNextDay`.
  - `TestParseVEventsFloatingUTCTZID`: 3 eventos, uno de cada tipo.
  - `TestParseExpandedRecurrence`: 2 VEVENT con RECURRENCE-ID dan 2 eventos.
- [ ] **Step 2:** `GOTEST` → FAIL.
- [ ] **Step 3:** Implementar. La referencia es `/home/johnny/Escritorio/proyectos/nextcloud-ollama-telegram/src/nextcloud/ical.ts`, para leer la lógica, no para traducir línea a línea.
- [ ] **Step 4:** `GOTEST` → PASS.
- [ ] **Step 5:** Commit `feat(nextcloud-mcp): iCal helpers (VTODO/VEVENT, TZID, VTIMEZONE)`.

### Task 4: CalDAV, descubrimiento de calendarios y Notes

**Files:** Create `nc/caldav.go`, `nc/notes.go`; Modify `nctest/fake.go`; Test `nc/caldav_test.go`, `nc/notes_test.go`

**Interfaces:**
- Consumes: `Client` (Task 2).
- Produces:
  - `type Calendar struct{ID, Name string; Events, Todos bool}`
  - `(c) Calendars(ctx) ([]Calendar, error)`.
    - Hace PROPFIND Depth 1 de `/remote.php/dav/calendars/{user}/` con `displayname`, `resourcetype` y `C:supported-calendar-component-set`.
    - Salta `inbox`, `outbox`, `trashbin` y `contact_birthdays`, y todo lo que no es `C:calendar`.
  - `(c) MkCalendarTodos(ctx, name string) (Calendar, error)`: MKCALENDAR con id `text.Slug(name)` y solo el componente VTODO.
  - `type CalObject struct{Href, ETag, Data string}`
  - `(c) Query(ctx, calID, comp string, from, to *time.Time, expand bool) ([]CalObject, error)`: REPORT `calendar-query`. Con rango agrega `C:time-range`; con `expand` agrega `<C:expand>`.
  - `(c) GetObject(ctx, href) (CalObject, error)`, `(c) PutObject(ctx, calID, uid, data, etag string) (string, error)` (archivo `{uid}.ics`), `(c) DeleteObject(ctx, href, etag string) error` (con If-Match).
  - `type Note struct{ID int64; Title, Content, Category, ETag string; Modified int64}`
  - `(c) Notes(ctx)`, `(c) Note(ctx, id)`, `(c) CreateNote(ctx, title, content)`, `(c) UpdateNoteContent(ctx, n Note, content string) error`.
    - Base `/index.php/apps/notes/api/v1/notes`, con header `OCS-APIRequest: true`.
    - El update manda `If-Match: n.ETag`; 412 devuelve `ErrConflict`.
  - Fake: calendarios en memoria (MKCALENDAR, REPORT filtrado por componente y por solapamiento simple de DTSTART/DUE con el rango, PUT/GET/DELETE con ETag) y Notes CRUD con ETag.

- [ ] **Step 1: Tests que fallan:**
  - `TestCalendarsSkipsSystemAndClassifies`: el fixture trae los 4 calendarios de sistema, uno mixto (VEVENT+VTODO), uno solo VTODO y una libreta de direcciones. Esperado: 2 calendarios, con flags correctos.
  - `TestMkCalendarSlugID`: "Mesa Libre" da `mesa-libre`.
  - `TestQueryBuildsTimeRangeAndExpand`: el fake captura el body y verifica `time-range start=... end=...` en UTC más `<C:expand`.
  - `TestDeleteSendsIfMatch`.
  - `TestNotesHeaderAndIfMatch`: sin `OCS-APIRequest` el fake responde 400; un update con ETag viejo da `ErrConflict`.
- [ ] **Step 2:** `GOTEST` → FAIL.
- [ ] **Step 3:** Implementar con structs de `encoding/xml` para multistatus. Esto mueve `Slug` a `text` en el Task 5; mientras tanto se usa un helper local que el Task 5 reemplaza.
- [ ] **Step 4:** `GOTEST` → PASS.
- [ ] **Step 5:** Verificar contra el Nextcloud real (solo lectura) que la API de Notes respeta `If-Match`. Es una tarea que la spec deja al builder (l338).
  - Correr un programa de prueba en `docker run --rm` con las env del usuario. El usuario las exporta en su shell; yo no leo `.env`.
  - El programa hace GET de una nota y luego PUT con un ETag inventado sobre una nota de prueba que crea y borra el usuario.
  - Si **no** responde 412: `UpdateNoteContent` vuelve a leer la nota justo antes del PUT y compara `Modified`. Se anota en el comentario de la función.
- [ ] **Step 6:** Commit `feat(nextcloud-mcp): CalDAV discovery/query/write + Notes API client`.

### Task 5: Texto: normalización, matching, formato de voz y fechas

**Files:** Create `text/normalize.go`, `text/match.go`, `text/voice.go`, `text/dates.go`; Test `text/*_test.go`

**Interfaces:**
- Produces:
  - `Normalize(s) string`: minúsculas, sin tildes ni diéresis (ñ→n), solo letras, dígitos y espacios simples.
  - `Slug(s) string`: Normalize con espacios convertidos en `-`.
  - `Find(titles []string, q string) []int`: índices de las coincidencias exactas normalizadas; si no hay, los que contienen `q`; si no hay, nil.
  - `Candidates(titles []string, q string, n int) []string`: hasta `n` títulos ordenados por cantidad de palabras normalizadas en común con `q` (con al menos 1) y después alfabéticamente.
  - `List(items []string, max int) string`: une con `"; "`. Con más de `max` agrega `" y N más"`.
  - `Clip(s string, max int) string`: con `len > max` corta en el último salto de línea o espacio antes de `max` y agrega `"\n(recortado; pide una parte más específica)"`.
  - `Human(t, now time.Time, withTime bool) string`: "hoy 15:00", "mañana 15:00", "ayer", y otros días "vie 21/11" (con hora si `withTime`). Días abreviados en español sin tilde en la salida: lun mar mie jue vie sab dom.
  - `MonthName(m time.Month) string` ("noviembre").
  - `ParseWhen(s string, loc) (t time.Time, dateOnly bool, err error)`.
    - Acepta `2006-01-02`, `2006-01-02T15:04`, `2006-01-02 15:04`, con segundos opcionales, y RFC3339 con offset (se convierte a `loc`).
    - Un error da un mensaje de una línea: "Fecha no válida: X (usa AAAA-MM-DD o AAAA-MM-DDTHH:MM)".
  - `ParsePriority(s string) (int, error)`: alta=1, media=5, baja=9 y números del 0 al 9; con `""` devuelve 5. `PriorityWord(p int) string`: 1–4 alta, 5 media, 6–9 baja, 0 "".

- [ ] **Step 1: Tests que fallan:**
  - `TestNormalize`: "Joyería  ÑANDÚ" da "joyeria nandu".
  - `TestSlug`: "Mesa Libre" da "mesa-libre".
  - `TestFindExactBeatsContains`: "comprar alambre" con ["Comprar alambre", "Comprar alambre de cobre"] da [0].
  - `TestFindAmbiguous` (**Review Focus 5**): "alambre" con ["Comprar alambre", "Alambre de cobre", "Pan"] da [0 1].
  - `TestCandidatesMax5`.
  - `TestListYNMas`: 13 ítems con max 10 terminan en "y 3 más".
  - `TestClip`.
  - `TestHuman`: con `now = mié 19/11/2026 10:00`, el jueves da "mañana 15:00" y el viernes 21 da "vie 21/11 15:00".
  - `TestParseWhenForms`.
  - `TestParsePriority`: "Alta" da 1, "3" da 3, "urgente" da error, "" da 5.
- [ ] **Step 2:** `GOTEST` → FAIL.
- [ ] **Step 3:** Implementar (stdlib; tildes con una tabla de reemplazo, sin `x/text`). Reemplazar el helper local de slug del Task 4.
- [ ] **Step 4:** `GOTEST` → PASS.
- [ ] **Step 5:** Commit `feat(nextcloud-mcp): text normalization, title matching, voice formatting, dates`.

### Task 6: Formato y parser de bitácora

**Files:** Create `bitacora/bitacora.go`; Test `bitacora/bitacora_test.go`

**Interfaces:**
- Consumes: `text.Slug`, `text.MonthName`, `text.Find`.
- Produces:
  - `Path(ctx string, day time.Time) string`: `/Bitacora/{Slug(ctx)}/YYYY/YYYY-MM-DD.md`.
  - `Header(ctx string, day time.Time) string`: `# Bitácora {ctx} — DD de mes de YYYY\n\n`. `ctx` se usa tal como lo dijo el usuario, sin slug (spec l145).
  - `EntryText(hhmm, titulo, texto string) string`: `## HH:MM — Título\n\nTexto.\n\n---\n`.
  - `CorrectionText(hhmm string, orig Entry, texto string) string`: el formato exacto de spec l145 (`## HH:MM — Corrección: T`, `> Corrige: HH:MM — T`, texto y `---`).
  - `type Entry struct{Time, Title, Text string; CorrectedAt string}`
  - `type Day struct{Date time.Time; Context string; Entries []Entry; Raw string; Structured bool}`
  - `Parse(content string) Day`.
    - Divide por encabezados `## HH:MM — ` y tolera `-` o `–` en lugar de `—`.
    - Una entrada `Corrección:` con `> Corrige: X — T` reemplaza el `Text` de la entrada original y fija `CorrectedAt`; si hay varias, gana la última. La corrección no aparece como entrada propia.
    - Una corrección huérfana (sin original en el archivo) queda como entrada normal.
    - Si no hay ningún encabezado reconocible, `Structured=false` y se conserva `Raw`.
  - `FindEntry(d Day, ref string) []int`. Si `ref` es `HH:MM`, busca por hora; si no, usa `text.Find` sobre los títulos.

- [ ] **Step 1: Tests que fallan:**
  - `TestEntryAndHeaderExact`: compara strings literales de spec l145.
  - `TestParseAppliesLastCorrection`: 2 correcciones de la misma entrada; gana la segunda y `CorrectedAt` es su hora.
  - `TestParseOrphanCorrectionKept`.
  - `TestParseHandEditedDashes`.
  - `TestParseUnstructured`: texto libre sin encabezados da `Structured=false`.
  - `TestFindEntryByTimeAndTitle`: incluye el caso ambiguo (2 entradas con la misma hora dan 2 índices).
- [ ] **Step 2:** `GOTEST` → FAIL.
- [ ] **Step 3:** Implementar.
- [ ] **Step 4:** `GOTEST` → PASS.
- [ ] **Step 5:** Commit `feat(nextcloud-mcp): bitácora file format and tolerant parser`.

### Task 7: Store de confirmaciones

**Files:** Create `confirm/store.go`; Test `confirm/store_test.go`

**Interfaces:**
- Produces:
  - `type Pending struct{Code, Action, Summary, Href, ETag string; Params map[string]string; IssuedAt time.Time}`
  - `New(now func() time.Time, rnd io.Reader) *Store`: en producción `time.Now` y `crypto/rand.Reader`. Es seguro para uso concurrente (mutex).
  - `(s) Issue(p Pending) (code string, err error)`.
    - El código son 4 dígitos (`0000`–`9999`) y es único entre los vivos; reintenta si choca.
    - Purga los vencidos. TTL `120 * time.Second`.
  - `(s) Take(code string) (Pending, error)`. Si no existe devuelve `ErrUnknown`; si pasaron más de 120 s, `ErrExpired`. En los dos casos el código se elimina (uso único).
  - `Prompt(summary, code string) string`. Devuelve exactamente `PENDIENTE: pregunta al usuario "{summary}". Solo si responde que sí en su próximo mensaje, llama confirmar con codigo={code}.` (spec l216).

- [ ] **Step 1: Tests que fallan:**
  - `TestIssueFourDigitsUnique`: con un `rnd` determinista que repite el mismo valor, el segundo `Issue` da un código distinto.
  - `TestStoreExpiresAndSingleUse` (**Review Focus 4**):
    - `Take` a los 119 s funciona; un segundo `Take` da `ErrUnknown`.
    - Otro código tomado a los 121 s da `ErrExpired` y, después, `ErrUnknown`.
  - `TestPromptExact`.
- [ ] **Step 2:** `GOTEST` → FAIL.
- [ ] **Step 3:** Implementar.
- [ ] **Step 4:** `GOTEST` → PASS.
- [ ] **Step 5:** Commit `feat(nextcloud-mcp): confirmation store (4-digit, 120s TTL, single use)`.

### Task 8: Herramientas

Son 19 herramientas en 6 archivos. Para no partir un registro compartido en tareas que no compilan solas, va todo en un solo task con un sub-ciclo TDD por grupo: test, FAIL, implementación, PASS y commit por grupo.

**Files:**
- Create: `tools/registry.go`, `tools/bitacora.go`, `tools/agenda.go`, `tools/tareas.go`, `tools/notas.go`, `tools/archivo.go`, `tools/confirmar.go`
- Test: `tools/helpers_test.go`, más un `*_test.go` por archivo (contra `nctest.Fake`)

**Interfaces:**
- Consumes: todo lo anterior.
- Produces:
  - `type Deps struct{NC *nc.Client; Confirm *confirm.Store; Now func() time.Time; Loc *time.Location}`
  - `type Tool struct{Def mcp.Tool; Handle server.ToolHandlerFunc}`
  - `All(d *Deps) []Tool`: las 19 herramientas, con los nombres y parámetros de spec l96–l140. Los parámetros obligatorios llevan `mcp.Required()`, y cada uno tiene una `mcp.Description` corta en español.
  - `ok(text) *mcp.CallToolResult` y `fail(msg) *mcp.CallToolResult` (`NewToolResultText` / `NewToolResultError`). Los handlers nunca devuelven un `error` de Go por fallas de dominio o de red. Siempre devuelven un texto de una línea, porque el fork solo reenvía texto.
  - `executors map[string]func(ctx context.Context, d *Deps, p confirm.Pending) (string, error)`: acciones `evento_cancelar`, `tarea_cambiar` y `tarea_borrar`.
  - `resolveProject(ctx, d, name string, create bool) (nc.Calendar, string /*msg si falla*/)`: matching por id y displayname normalizados entre los calendarios con `Todos`.
  - `resolveTodo(ctx, d, cal nc.Calendar, ref string, includeCompleted bool) (nc.CalObject, ical.Todo, string)`: por uid, nombre de archivo o título con `text.Find`. Con 0 coincidencias responde "No encontré…" más hasta 5 candidatos; con más de una lista título y fecha.
  - Helper de test: `call(t, tools []Tool, name string, args map[string]any) (text string, isErr bool)`. Arma un `mcp.CallToolRequest` con `Params.Name` y `Params.Arguments = args`.

- [ ] **Step 1: Bitácora** (`bitacora_anotar`, `bitacora_corregir`, `bitacora_leer`; spec l145–l190).
  Tests:
  - `TestAnotarCreatesFoldersHeaderEntry`: archivo nuevo con Header y Entry; MKCOL de `Bitacora/{ctx}/YYYY`; `hora` opcional pisa la hora actual.
  - `TestAnotarAppendsKeepsExisting`.
  - `TestAnotarSurvivesConcurrentWrite`: con `MutateBeforeNextPut`, el resultado conserva las dos escrituras.
  - `TestCorregirAppendsToOriginalDay`.
  - `TestCorregirNotFoundWritesNothing`: incluye el caso de una entrada ambigua.
  - `TestLeerOneDayFull`.
  - `TestLeerRangeSummaryGroupedPideDetalle`.
  - `TestLeerNoContextListsAllContexts`.
  - `TestLeerMax31Days`.
  - `TestLeerTextoDefaultsLast30Days`.
  - `TestLeerShowsCorregida`.
  - `TestLeerUnstructuredBlock`.

  Después: FAIL, implementación (4 GET paralelos con semáforo; los 404 se ignoran), PASS y commit `feat(nextcloud-mcp): bitácora tools`.
- [ ] **Step 2: Agenda y eventos** (`agenda`, `resumen_del_dia`, `evento_crear`, `evento_cancelar`; spec l96–l140 y l191).
  Tests:
  - `TestAgendaDefaultTodayAllEventCalendars`.
  - `TestAgendaExpandsRecurrence`.
  - `TestEventoCrearDefaultsOneHourAndSaysSo`.
  - `TestEventoCrearAllDay`.
  - `TestEventoCrearUnknownCalendarListsEventCalendars`.
  - `TestEventoCrearDuplicateSameDayBlocks`.
  - `TestEventoCrearForzar`.
  - `TestEventoCrearOverlapCreatesAndMentions`.
  - `TestEventoCancelarReturnsPendingNoDelete`: el objeto sigue en el fake.
  - `TestEventoCancelarRecurringWarnsSeries`: "(se borra la serie completa)".
  - `TestResumenDelDiaFilter`: el evento de hoy y las tareas vencidas, que vencen hoy o con PRIORITY de 1 a 4 aparecen; una tarea completada, una de prioridad 5 sin fecha y una de prioridad 0 no aparecen.

  Después: FAIL, implementación, PASS y commit `feat(nextcloud-mcp): agenda and event tools`.
- [ ] **Step 3: Tareas** (`tareas_pendientes`, `tarea_crear`, `tarea_completar`, `tarea_anotar`, `tarea_cambiar`, `tarea_borrar`).
  Tests:
  - `TestTareaCrearUnknownProjectLists`.
  - `TestTareaCrearCrearProyecto`: MKCALENDAR VTODO.
  - `TestTareaCrearDuplicateBlocksUnlessCompleted`.
  - `TestTareaCrearForzar`.
  - `TestTareaCrearDentroDe`: lleva `RELATED-TO;RELTYPE=PARENT` con el UID del padre; un padre ambiguo no crea nada.
  - `TestTareaCrearPriorityWords`.
  - `TestTareaCompletarSetsFieldsAndLogsBitacora`: STATUS, PERCENT-COMPLETE:100 y COMPLETED; la bitácora `{proyecto-id}` recibe `## HH:MM — Completada: T`.
  - `TestTareaCompletarBitacoraFailsKeepsCompletion`: el fake falla los PUT bajo `/Bitacora`. El texto es exactamente "Tarea completada; no pude anotarla en la bitácora: …" y la tarea sigue COMPLETED.
  - `TestTareaAnotarAppendsDatedDescription`.
  - `TestTareaCambiarPendingShowsBeforeAfter`.
  - `TestTareaBorrarAmbiguousNoPending` (**Review Focus 5**): el store queda vacío.
  - `TestTareasPendientesSinProyectoParallelMax4`: el fake cuenta la concurrencia máxima, que debe ser ≤4.

  Después: FAIL, implementación, PASS y commit `feat(nextcloud-mcp): task tools`.
- [ ] **Step 4: Notas, estado y archivos** (`nota_guardar`, `notas_buscar`, `estado_proyecto_anotar`, `estado_proyecto_leer`, `archivo_leer`; spec l200 y l207).
  Tests:
  - `TestNotaGuardarAppendsToExactTitle`: el formato exacto es `\n\n---\n**DD/MM/YYYY HH:MM**\n\n{texto}` y la respuesta es "Agregado a la nota existente 'X'".
  - `TestNotaGuardarCreatesNew`.
  - `TestNotaGuardarConflictRetries`.
  - `TestNotasBuscarOneReturnsContent`.
  - `TestNotasBuscarManyListsTitles`.
  - `TestEstadoAnotarNewFileHeaderAndSection`: `# Estado: {p}`, `## DD/MM/YYYY — Actualización` y la carpeta creada.
  - `TestEstadoLeerClipped`.
  - `TestArchivoLeerByPath`.
  - `TestArchivoLeerByNameSearch`: con un resultado lo lee; con varios lista rutas.
  - `TestArchivoLeerBinaryRefused`: un archivo no-texto responde "No puedo leer en voz alta un archivo {ext}".
  - `TestNoCredentialURLsInOutput`: recorre todas las respuestas de este grupo y verifica que ninguna contiene `NEXTCLOUD_URL` ni la password.

  Después: FAIL, implementación, PASS y commit `feat(nextcloud-mcp): notes, project status and file tools`.
- [ ] **Step 5: confirmar** (spec l216–l236).
  Tests:
  - `TestConfirmarExecutesTareaBorrar`.
  - `TestConfirmarExecutesEventoCancelar`.
  - `TestConfirmarExecutesTareaCambiar`.
  - `TestConfirmarAcceptsNumber` (**Review Focus 2**): `codigo` como `float64(4821)` y como `"4821"`.
  - `TestConfirmarETagChangedAborts`: se modifica el objeto entre el PENDIENTE y `confirmar`. No borra y responde "La tarea cambió desde que la pedí; repite la orden".
  - `TestConfirmarExpiredOrUnknown`: una línea, sin ejecutar nada.

  Implementación: `Take` → `executors[p.Action]` → re-GET de `p.Href`; si el ETag difiere de `p.ETag`, aborta; si no, DELETE o PUT con If-Match.

  Después: FAIL, implementación, PASS y commit `feat(nextcloud-mcp): confirmar executes pending actions`.

### Task 9: main.go

**Files:** Create `server/nextcloud-mcp/main.go`; Test `server/nextcloud-mcp/main_test.go`

**Interfaces:**
- Consumes: `tools.All`, `nc.New`, `confirm.New`.
- Produces: `run(env func(string) string, in io.Reader, out, errw io.Writer) int`. `main` hace `os.Exit(run(os.Getenv, os.Stdin, os.Stdout, os.Stderr))`.

- [ ] **Step 1: Tests que fallan:**
  - `TestRunMissingEnvExits`: devuelve un valor ≠0, stderr menciona `NEXTCLOUD_URL` y stdout queda vacío.
  - `TestRunListsTools`: con env apuntando a `nctest.Fake`, manda por `in` los mensajes `initialize`, `notifications/initialized` y `tools/list`. La respuesta tiene 19 tools con los nombres de spec l96–l140 y ninguna línea de stdout es log.
- [ ] **Step 2:** `GOTEST` → FAIL.
- [ ] **Step 3:** Implementar con `import _ "time/tzdata"`.
  - `Loc` sale de `time.LoadLocation(env("TZ"))`, con fallback a `America/Santiago`.
  - Usa `server.NewMCPServer("nextcloud", "1.0.0", server.WithToolCapabilities(false))` y `AddTool` por cada `tools.All`.
  - Sirve con `server.NewStdioServer(s)` más `SetErrorLogger(log.New(errw, "nextcloud-mcp ", log.LstdFlags))` y `.Listen(ctx, in, out)`.
- [ ] **Step 4:** `GOTEST` → PASS. Correr también `CGO_ENABLED=0 go build -o /tmp/x .` en el mismo contenedor → OK.
- [ ] **Step 5:** Commit `feat(nextcloud-mcp): stdio entrypoint with env validation`.

### Task 10: Guardia de confirmación en el fork

**Files:**
- Create: `server/xiaozhi-server-go/src/core/confirm_guard.go`
- Test: `server/xiaozhi-server-go/src/core/confirm_guard_test.go`
- Modify: `server/xiaozhi-server-go/src/core/connection.go`
  - Agregar el campo `confirmGuard confirmGuard` al struct en l67. El valor cero sirve.
  - Hook de la frase del usuario antes del `dialogueManager.Put` de l737-743.
  - Hook del dispatch MCP en l892-911.
- Modify: `server/xiaozhi-server-go/src/core/connection_handlemsg.go` ~l280-284: hook de la frase en el camino de imagen.

**Interfaces:**
- Produces, todo sin exportar y con un mutex adentro:
  - `(g *confirmGuard) OnToolResult(result string)`: si `result` empieza con `PENDIENTE:` y cumple `codigo=(\d{4})`, guarda el código como pendiente y **borra cualquier habilitación vigente**.
  - `(g *confirmGuard) OnUserUtterance(text string)`.
    - Un texto vacío o solo espacios se ignora.
    - Si hay un código pendiente, lo consume y habilita ese código si `containsCode(text, code)` o si `containsAffirmation(text) && !containsNegation(text)`.
    - Si no hay código pendiente, borra la habilitación vigente (solo vale para la respuesta a esa frase).
  - `(g *confirmGuard) Authorize(toolName string, args map[string]interface{}) bool`.
    - Si `!isConfirmTool(toolName)` devuelve true.
    - Si no, lee `codigo` (`float64` entero de 0 a 9999 formateado `%04d`, o un string de exactamente 4 dígitos) y autoriza solo si coincide con la habilitación. La habilitación se consume en cualquier caso.
  - `isConfirmTool(name)`: quita el prefijo `mcp_`; acepta `confirmar` o `confirm`.
  - `containsAffirmation`: tokens normalizados `si`, `dale`, `confirmo`, `hazlo`, `ok`, `okay`, y el bigrama `de acuerdo`.
  - `containsNegation`: tokens `no`, `nunca`, `cancela`, `cancelar`, `espera`.
  - `containsCode(text, code)`: mapea `cero`…`nueve` a dígitos y quita separadores entre dígitos consecutivos (espacios, `-`, `.`, `,`), y busca `code` en esas secuencias. "cuatro ocho dos uno", "48 21" y "4-8-2-1" cuentan.
- Wiring en `connection.go`:
  - Antes del `Put` de l737, si `text` no es el mensaje sintético `[SILENCE_TIMEOUT]` (l618), llamar `h.confirmGuard.OnUserUtterance(text)`. Las wake words ya salen antes, en l733-735.
  - En el dispatch MCP, envolver `ExecuteTool`:
    ```go
    var result interface{}; var err error
    if !h.confirmGuard.Authorize(functionName, arguments) {
        h.LogInfo("confirmar rechazado: el usuario no confirmó")
        result = "Rechazado: el usuario no confirmó"
    } else {
        result, err = h.mcpManager.ExecuteTool(ctx, functionName, arguments)
    }
    if s, ok := result.(string); ok { h.confirmGuard.OnToolResult(s) }
    ```
    Lo demás (manejo de `err` y de `ActionResponse`) queda igual.

- [ ] **Step 1: Tests que fallan** en `confirm_guard_test.go` (los nombres empiezan con `TestConfirmGuard` para el filtro de `FORKTEST`):
  - `TestConfirmGuardAffirmationEnablesOnce`: PENDIENTE, después "sí, dale", después `Authorize("mcp_confirmar", {"codigo":"4821"})` da true. Un segundo `Authorize` da false.
  - `TestConfirmGuardSelfConfirmRejected` (**Review Focus 3**): PENDIENTE seguido de `Authorize` sin ninguna frase da false.
  - `TestConfirmGuardNegationBlocks` (**Review Focus 1**): "no, no lo borres", "ok no" y "mejor no, espera" dan false. "no sé, 4821" da true.
  - `TestConfirmGuardSpokenDigits`: "cuatro ocho dos uno" y "48 21" dan true.
  - `TestConfirmGuardOnlyNextUtterance`: PENDIENTE, después "qué hora es" (sin afirmación), después "sí" y `Authorize` da false.
  - `TestConfirmGuardWrongCode`: habilitado 4821 y llamado con 1234 da false.
  - `TestConfirmGuardNumericCode` (**Review Focus 2**): `float64(4821)` da true; `float64(48.21)`, `"04821"`, `nil` y la clave ausente dan false sin pánico.
  - `TestConfirmGuardNonConfirmToolsPass`: `mcp_agenda` da true sin nada pendiente.
  - `TestConfirmGuardEmptyUtteranceIgnored`: con PENDIENTE, "" y "   " no consumen; el "sí" posterior sí habilita.
  - `TestConfirmGuardNewPendingClearsEnable`: un PENDIENTE nuevo después de una habilitación la anula.
- [ ] **Step 2:** `FORKTEST` → FAIL (no compila).
- [ ] **Step 3:** Implementar `confirm_guard.go` y el wiring de arriba en los tres sitios.
- [ ] **Step 4:** `FORKTEST` → PASS. Después, el mismo contenedor con `go build ./src && go vet ./src/core/` → OK, y `go test ./src/core/...` sigue verde.
- [ ] **Step 5:** Commit `feat(core): confirmation guard for MCP confirmar tools`.

### Task 11: Empaquetado (Dockerfile, settings MCP, compose)

**Files:**
- Modify: `server/Dockerfile`. Agregar una etapa `mcpbuilder` (`golang:1.24-alpine`, `WORKDIR /mcp`, copia `nextcloud-mcp/go.mod go.sum`, `go mod download`, copia `nextcloud-mcp/`, `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nextcloud-mcp .`). En el runtime: `COPY --from=mcpbuilder /out/nextcloud-mcp /app/nextcloud-mcp` y `COPY --chmod=644 xiaozhi/.mcp_server_settings.json /app/.mcp_server_settings.json`.
- Create: `server/xiaozhi/.mcp_server_settings.json` con el contenido exacto de spec l294–l307.
- Modify: `server/docker-compose.yml`, servicio xiaozhi `environment`: `NEXTCLOUD_URL: ${NEXTCLOUD_URL:-}`, `NEXTCLOUD_USER: ${NEXTCLOUD_USER:-}` y `NEXTCLOUD_PASSWORD: ${NEXTCLOUD_PASSWORD:-}`. `TZ` ya está.
- Modify: `server/.env.example`: las 3 variables con placeholder.

- [ ] **Step 1:** Confirmar que `utils.GetProjectDir()` resuelve a `/app` en el contenedor (leer la función). Si no, ajustar el destino del `COPY` del settings.
- [ ] **Step 2:** `docker build -t xiaozhi-server:mcp server/` → OK.
  `docker run --rm --entrypoint sh xiaozhi-server:mcp -c 'ls -l /app/nextcloud-mcp /app/.mcp_server_settings.json && /app/nextcloud-mcp; echo rc=$?'`
  → Esperado: los dos archivos existen, y stderr muestra "faltan NEXTCLOUD_URL…" con `rc` ≠0.
- [ ] **Step 3:** Arrancar sin `NEXTCLOUD_*`:
  `docker run --rm -e LAN_IP=1.2.3.4 -e LITELLM_MASTER_KEY=sk-x --entrypoint sh xiaozhi-server:mcp -c '/config/entrypoint.sh & sleep 8; kill %1'`.
  Pasar los logs por el filtro de secretos (`grep -viP '(?i)key|token|secret|password|bearer|sk-|postgresql://|密码|临时'`).
  → Esperado: el servidor arranca, el error del MCP aparece logueado y no hay pánico (la voz quedaría intacta).
- [ ] **Step 4:** `docker image rm xiaozhi-server:mcp`. `docker ps -a` no muestra contenedores nuevos.
- [ ] **Step 5:** Commit `feat(server): bundle nextcloud-mcp and MCP settings`. Escanear secretos del diff antes de commitear (`git diff --cached | grep -iP 'password|token|sk-'` → solo placeholders).

### Task 12: Bloque de prompt para la secretaria

**Files:** Modify `server/xiaozhi/config.template.yaml`, dentro de `prompt: |` (empieza en l49). Agregar al final del bloque el texto de reglas de spec l250–l265. **Este archivo tiene un cambio del usuario sin commitear** (la línea de "weita").

- [ ] **Step 1:** `git diff server/xiaozhi/config.template.yaml` para ver el cambio del usuario y no pisarlo.
- [ ] **Step 2:** Agregar el bloque, en español, sin markdown y con la misma indentación YAML que el prompt existente. Lleva:
  - cuándo usar cada herramienta
  - la lista de proyectos conocidos: joyeria, casa, mascotas, mesa-libre, bot-opticas, bot-ofertas, scraper-bancos, bot-secretaria, otros, tareas
  - tareas → Tasks; eventos con hora → calendario; preguntar el calendario si no lo dijo
  - una herramienta por turno
  - preguntar el contexto de la bitácora si no está claro; no anotar si no lo pide (salvo al completar)
  - corregir con `bitacora_corregir`; editar o borrar archivos y notas no es posible por voz, y hay que decirlo
  - ante `PENDIENTE:`, preguntar y esperar; nunca llamar `confirmar` sin un sí explícito
  - ante un aviso de duplicado, preguntar; usar `forzar` solo si insiste
- [ ] **Step 3:** Validar que el YAML parsea:
  `docker run --rm -v "$PWD/server/xiaozhi":/c:ro python:3.12-alpine sh -c 'pip install -q pyyaml && python -c "import yaml; yaml.safe_load(open(\"/c/config.template.yaml\"))" && echo OK'` → `OK`.
- [ ] **Step 4:** **No commitear el cambio del usuario sin permiso.** Preguntarle si se commitea junto (`feat(config): secretaria prompt rules` incluye su línea de weita) o si se separa con `git add -p` solo mi bloque. Hacer lo que diga.

### Task 13: Ajustes finales y documentación

**Files:** Modify `server/litellm/litellm-config.yaml`; Create `server/docs/nextcloud-mcp.md`; Modify `server/nextcloud-mcp/README.md` (crear).

- [ ] **Step 1:** En `litellm-config.yaml`: `max_tokens: 1000` en el modelo `deepseek-chat` (las respuestas con tool calls más el razonamiento no deben cortarse) y `log_level: INFO` si quedó en DEBUG.
- [ ] **Step 2:** En `server/nextcloud-mcp/README.md`: qué hace, las variables de entorno, cómo correr los tests (`GOTEST`) y la lista de herramientas con una línea cada una.
- [ ] **Step 3:** En `server/docs/nextcloud-mcp.md`:
  - el checklist E2E por voz de spec l308–l327, para que lo ejecute el usuario
  - el resultado del Task 1
  - la limitación de que el fork ejecuta solo `toolCall[0]`
  - una tabla para anotar la latencia medida de `resumen_del_dia` y de `bitacora_leer` (semana)
- [ ] **Step 4:** Commit `docs(nextcloud-mcp): README, E2E checklist, litellm tuning`. Escanear secretos y después `git push`, que despliega en Coolify al hacer redeploy (lo hace el usuario).
- [ ] **Step 5 (usuario + yo):**
  - El usuario crea la app password, pone las 3 variables en Coolify y redeploya.
  - Corre el E2E por voz y yo sigo los logs filtrados.
  - Captura de tráfico de salida del contenedor (`tcpdump` en el host por el usuario, o la lista de conexiones con `ss`): solo debe haber destinos Nextcloud, LiteLLM y Speaches.
  - Reiniciar el contenedor y verificar que la voz y las tools vuelven solas.
- [ ] **Step 6:** Borrar `<scratchpad>/.mk`, correr `graphify update .` y pasar a superpowers:finishing-a-development-branch.
