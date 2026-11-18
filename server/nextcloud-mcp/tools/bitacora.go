package tools

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/bitacora"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

// maxLogDays is the widest range bitacora_leer accepts.
const maxLogDays = 31

var (
	horaRe    = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	timeRefRe = regexp.MustCompile(`^(\d{1,2}:\d{2})\s*[—–-]?\s*(.+)$`)
)

func bitacoraTools(d *Deps) []Tool {
	return []Tool{
		{
			Def: mcp.NewTool("bitacora_anotar",
				mcp.WithDescription("Anota una entrada en la bitácora de hoy de un contexto (casa, trabajo, un proyecto). Solo agrega, nunca borra."),
				reqStr("contexto", "Bitácora donde anotar, por ejemplo casa o trabajo"),
				reqStr("titulo", "Título corto de la entrada"),
				reqStr("texto", "Lo que hay que anotar"),
				optStr("hora", "Hora HH:MM si no es ahora"),
			),
			Handle: handler(d.bitacoraAnotar),
		},
		{
			Def: mcp.NewTool("bitacora_corregir",
				mcp.WithDescription("Corrige una entrada de la bitácora agregando una corrección en el día original; el texto original se conserva."),
				reqStr("contexto", "Bitácora de la entrada"),
				reqStr("fecha", "Día de la entrada, AAAA-MM-DD"),
				reqStr("entrada", "Hora HH:MM o título de la entrada a corregir"),
				reqStr("texto", "Texto corregido"),
			),
			Handle: handler(d.bitacoraCorregir),
		},
		{
			Def: mcp.NewTool("bitacora_leer",
				mcp.WithDescription("Lee la bitácora: un día completo o un resumen de un rango de hasta 31 días. Sin contexto lee todas."),
				optStr("contexto", "Bitácora a leer; vacío para todas"),
				optStr("desde", "Primer día AAAA-MM-DD; por defecto hoy"),
				optStr("hasta", "Último día AAAA-MM-DD; por defecto hoy"),
				optStr("texto", "Filtra entradas que contengan este texto; sin fechas busca en los últimos 30 días"),
			),
			Handle: handler(d.bitacoraLeer),
		},
	}
}

func (d *Deps) bitacoraAnotar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "contexto", "titulo", "texto"); r != nil {
		return r
	}
	contexto, titulo, texto := str(req, "contexto"), str(req, "titulo"), str(req, "texto")
	if text.Slug(contexto) == "" {
		return fail("El contexto de la bitácora no es válido.")
	}
	now := d.now()
	hhmm := now.Format("15:04")
	if h := str(req, "hora"); h != "" {
		v, ok := normHora(h)
		if !ok {
			return fail("Hora no válida: " + h + " (usa HH:MM).")
		}
		hhmm = v
	}
	p := bitacora.Path(contexto, now)
	if err := d.NC.MkdirAll(ctx, path.Dir(p)); err != nil {
		return failErr("No pude crear la carpeta de la bitácora", err)
	}
	entry := bitacora.EntryText(hhmm, titulo, texto)
	err := d.NC.Append(ctx, p, func(old []byte, exists bool) ([]byte, error) {
		if !exists {
			return []byte(bitacora.Header(contexto, now) + entry), nil
		}
		return []byte(appendBlock(string(old), entry)), nil
	})
	if err != nil {
		return failErr("No pude anotar en la bitácora", err)
	}
	return ok(fmt.Sprintf("Anotado en la bitácora %s a las %s: %s", contexto, hhmm, titulo))
}

// appendBlock adds block after old, separated by one blank line.
func appendBlock(old, block string) string {
	old = strings.TrimRight(old, "\n")
	if old == "" {
		return block
	}
	return old + "\n\n" + block
}

// normHora validates H:MM / HH:MM and zero-pads it.
func normHora(s string) (string, bool) {
	m := horaRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if h > 23 || mi > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", h, mi), true
}

func (d *Deps) bitacoraCorregir(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "contexto", "fecha", "entrada", "texto"); r != nil {
		return r
	}
	contexto, fecha, ref, texto := str(req, "contexto"), str(req, "fecha"), str(req, "entrada"), str(req, "texto")
	if text.Slug(contexto) == "" {
		return fail("El contexto de la bitácora no es válido.")
	}
	day, _, err := text.ParseWhen(fecha, d.loc())
	if err != nil {
		return fail(err.Error())
	}
	p := bitacora.Path(contexto, day)
	body, _, err := d.NC.Get(ctx, p)
	if errors.Is(err, nc.ErrNotFound) {
		return fail(fmt.Sprintf("No hay bitácora %s del %s.", contexto, fmtDate(day)))
	}
	if err != nil {
		return failErr("No pude leer la bitácora", err)
	}
	parsed := bitacora.Parse(string(body))
	idx := findLogEntry(parsed, ref)
	switch len(idx) {
	case 0:
		titles := make([]string, len(parsed.Entries))
		for i, e := range parsed.Entries {
			titles[i] = e.Time + " — " + e.Title
		}
		msg := fmt.Sprintf("No encontré la entrada '%s' en la bitácora %s del %s.", ref, contexto, fmtDate(day))
		if c := text.Candidates(titles, ref, 5); len(c) > 0 {
			msg += " Entradas: " + strings.Join(c, "; ") + "."
		} else if len(titles) > 0 {
			msg += " Entradas: " + text.List(titles, 5) + "."
		}
		return fail(msg)
	case 1:
	default:
		items := make([]string, len(idx))
		for i, j := range idx {
			items[i] = parsed.Entries[j].Time + " — " + parsed.Entries[j].Title
		}
		return fail("Hay varias entradas que coinciden: " + text.List(items, 10) + ". Dime la hora exacta.")
	}
	orig := parsed.Entries[idx[0]]
	hhmm := d.now().Format("15:04")
	block := bitacora.CorrectionText(hhmm, orig, texto)
	err = d.NC.Append(ctx, p, func(old []byte, exists bool) ([]byte, error) {
		if !exists {
			return nil, errors.New("la bitácora ya no existe")
		}
		return []byte(appendBlock(string(old), block)), nil
	})
	if err != nil {
		return failErr("No pude guardar la corrección", err)
	}
	return ok(fmt.Sprintf("Corregida la entrada %s — %s de la bitácora %s del %s.", orig.Time, orig.Title, contexto, fmtDate(day)))
}

// findLogEntry accepts "HH:MM", a title, or "HH:MM — título".
func findLogEntry(day bitacora.Day, ref string) []int {
	ref = strings.TrimSpace(ref)
	if m := timeRefRe.FindStringSubmatch(ref); m != nil && !horaRe.MatchString(ref) {
		byTime := bitacora.FindEntry(day, m[1])
		if len(byTime) <= 1 {
			if len(byTime) == 1 {
				return byTime
			}
		} else {
			sub := bitacora.Day{Entries: make([]bitacora.Entry, len(byTime))}
			for i, j := range byTime {
				sub.Entries[i] = day.Entries[j]
			}
			var out []int
			for _, k := range bitacora.FindEntry(sub, m[2]) {
				out = append(out, byTime[k])
			}
			return out
		}
		return bitacora.FindEntry(day, m[2])
	}
	return bitacora.FindEntry(day, ref)
}

type logDay struct {
	date time.Time
	slug string
	day  bitacora.Day
}

func (d *Deps) bitacoraLeer(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	contexto, desdeS, hastaS, filtro := str(req, "contexto"), str(req, "desde"), str(req, "hasta"), str(req, "texto")
	today := dayStart(d.now())
	desde, hasta := today, today
	var err error
	if desdeS != "" {
		if desde, _, err = text.ParseWhen(desdeS, d.loc()); err != nil {
			return fail(err.Error())
		}
		desde = dayStart(desde)
	}
	if hastaS != "" {
		if hasta, _, err = text.ParseWhen(hastaS, d.loc()); err != nil {
			return fail(err.Error())
		}
		hasta = dayStart(hasta)
	}
	switch {
	case desdeS == "" && hastaS == "" && filtro != "":
		desde = today.AddDate(0, 0, -29)
	case desdeS != "" && hastaS == "":
		if desde.After(today) {
			hasta = desde
		}
	case desdeS == "" && hastaS != "":
		desde = hasta
	}
	if desde.After(hasta) {
		return fail("La fecha desde es posterior a hasta.")
	}
	var dates []time.Time
	for t := desde; !t.After(hasta); t = t.AddDate(0, 0, 1) {
		dates = append(dates, t)
		if len(dates) > maxLogDays {
			return fail(fmt.Sprintf("Puedo leer como máximo %d días de bitácora; acota el rango.", maxLogDays))
		}
	}

	var slugs []string
	if contexto != "" {
		s := text.Slug(contexto)
		if s == "" {
			return fail("El contexto de la bitácora no es válido.")
		}
		slugs = []string{s}
	} else {
		entries, err := d.NC.List(ctx, "/Bitacora")
		if errors.Is(err, nc.ErrNotFound) {
			return fail("No hay bitácoras todavía.")
		}
		if err != nil {
			return failErr("No pude listar las bitácoras", err)
		}
		for _, e := range entries {
			if e.Dir {
				slugs = append(slugs, e.Name)
			}
		}
		sort.Strings(slugs)
	}

	days, err := d.fetchLogDays(ctx, slugs, dates)
	if err != nil {
		return failErr("No pude leer la bitácora", err)
	}
	now := d.now()
	single := len(dates) == 1
	var blocks, lines []string
	for _, ld := range days {
		name := ld.day.Context
		if name == "" {
			name = ld.slug
		}
		when := text.Human(ld.date, now, false)
		if !ld.day.Structured {
			raw := strings.TrimSpace(ld.day.Raw)
			if raw == "" || (filtro != "" && !strings.Contains(text.Normalize(raw), text.Normalize(filtro))) {
				continue
			}
			if single {
				blocks = append(blocks, fmt.Sprintf("Bitácora %s, %s:\n%s", name, when, raw))
			} else {
				lines = append(lines, fmt.Sprintf("%s · notas sin formato [%s]", when, name))
			}
			continue
		}
		entries := append([]bitacora.Entry(nil), ld.day.Entries...)
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time < entries[j].Time })
		var parts []string
		for _, e := range entries {
			if filtro != "" && !strings.Contains(text.Normalize(e.Title+" "+e.Text), text.Normalize(filtro)) {
				continue
			}
			if single {
				head := e.Time + " — " + e.Title
				if e.CorrectedAt != "" {
					head += " (corregida " + e.CorrectedAt + ")"
				}
				parts = append(parts, head+": "+strings.TrimSpace(e.Text))
			} else {
				lines = append(lines, fmt.Sprintf("%s · %s — %s [%s]", when, e.Time, e.Title, name))
			}
		}
		if single && len(parts) > 0 {
			blocks = append(blocks, fmt.Sprintf("Bitácora %s, %s:\n%s", name, when, strings.Join(parts, "\n")))
		}
	}
	if single {
		if len(blocks) == 0 {
			return ok(fmt.Sprintf("No hay entradas en la bitácora para %s.", text.Human(dates[0], now, false)))
		}
		return ok(text.Clip(strings.Join(blocks, "\n\n"), clipMax))
	}
	if len(lines) == 0 {
		return ok(fmt.Sprintf("No hay entradas en la bitácora entre el %s y el %s.", fmtDate(desde), fmtDate(hasta)))
	}
	return ok(text.Clip(strings.Join(lines, "\n"), clipMax) + "\nSi quieres, pide el detalle de un día.")
}

// fetchLogDays GETs every (slug, date) file with at most maxParallel requests
// in flight. Missing files are skipped; results are sorted by date, slug.
func (d *Deps) fetchLogDays(ctx context.Context, slugs []string, dates []time.Time) ([]logDay, error) {
	type job struct {
		slug string
		date time.Time
	}
	var jobs []job
	for _, s := range slugs {
		for _, t := range dates {
			jobs = append(jobs, job{s, t})
		}
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		out      []logDay
		firstErr error
		sem      = make(chan struct{}, maxParallel)
	)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			body, _, err := d.NC.Get(ctx, bitacora.Path(j.slug, j.date))
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, nc.ErrNotFound) {
				return
			}
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out = append(out, logDay{date: j.date, slug: j.slug, day: bitacora.Parse(string(body))})
		}(j)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(out, func(i, k int) bool {
		if !out[i].date.Equal(out[k].date) {
			return out[i].date.Before(out[k].date)
		}
		return out[i].slug < out[k].slug
	})
	return out, nil
}
