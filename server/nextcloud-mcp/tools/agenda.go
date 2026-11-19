package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/ical"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

// maxAgendaDays is the widest range agenda accepts.
const maxAgendaDays = 31

// maxListItems caps how many items a spoken list reads out (spec: 10).
const maxListItems = 10

// calEvent is one (possibly expanded) event instance with its origin.
type calEvent struct {
	ical.Event
	Cal        nc.Calendar
	Href, ETag string
}

// calTodo is one task with its origin object.
type calTodo struct {
	ical.Todo
	Cal nc.Calendar
	Obj nc.CalObject
}

func agendaTools(d *Deps) []Tool {
	return []Tool{
		{
			Def: mcp.NewTool("agenda",
				mcp.WithDescription("Lee los eventos de todos los calendarios en un rango de hasta 31 días; por defecto hoy."),
				optStr("desde", "Primer día AAAA-MM-DD; solo con desde se lee ese día"),
				optStr("hasta", "Último día AAAA-MM-DD; solo con hasta se lee desde hoy"),
			),
			Handle: handler(d.agenda),
		},
		{
			Def: mcp.NewTool("evento_crear",
				mcp.WithDescription("Crea un evento en un calendario. Sin fin dura 1 hora. Avisa si se cruza con otro evento y no crea duplicados del mismo día salvo con forzar."),
				reqStr("calendario", "Nombre del calendario"),
				reqStr("titulo", "Título del evento"),
				reqStr("inicio", "Inicio AAAA-MM-DDTHH:MM, o AAAA-MM-DD para todo el día"),
				optStr("fin", "Fin AAAA-MM-DDTHH:MM, o último día AAAA-MM-DD si es de todo el día"),
				optBool("todo_el_dia", "Evento de día completo"),
				optStr("lugar", "Lugar del evento"),
				optStr("descripcion", "Descripción o notas"),
				optStr("repetir", "Repetición: diario, semanal, mensual o anual"),
				optBool("forzar", "Crear aunque ya exista un evento con el mismo título ese día"),
			),
			Handle: handler(d.eventoCrear),
		},
		{
			Def: mcp.NewTool("evento_cancelar",
				mcp.WithDescription("Pide borrar un evento. No borra nada: devuelve una pregunta de confirmación y un código para confirmar."),
				reqStr("evento", "Título del evento a borrar"),
				optStr("fecha", "Día del evento AAAA-MM-DD para distinguirlo"),
			),
			Handle: handler(d.eventoCancelar),
		},
		{
			Def: mcp.NewTool("resumen_del_dia",
				mcp.WithDescription("Resumen breve de hoy: eventos, recordatorios, tareas que vencen hoy, de prioridad alta y atrasadas."),
			),
			Handle: handler(d.resumenDelDia),
		},
	}
}

// parallel runs f(0..n-1) with at most maxParallel in flight. On the first
// error it cancels the context passed to f and starts no further jobs.
func parallel(ctx context.Context, n int, f func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		if ctx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := f(ctx, i); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return firstErr
}

func eventCalendars(cals []nc.Calendar) []nc.Calendar {
	var out []nc.Calendar
	for _, c := range cals {
		if c.Events {
			out = append(out, c)
		}
	}
	return out
}

// RecordatoriosList is the Nextcloud task list that holds reminders; the
// regular task tools ignore it.
const RecordatoriosList = "Recordatorios"

// todoCalendars returns the VTODO calendars, leaving out the Recordatorios
// list unless includeRecordatorios is set.
func todoCalendars(cals []nc.Calendar, includeRecordatorios bool) []nc.Calendar {
	var out []nc.Calendar
	for _, c := range cals {
		if !includeRecordatorios && text.Normalize(c.Name) == text.Normalize(RecordatoriosList) {
			continue
		}
		if c.Todos {
			out = append(out, c)
		}
	}
	return out
}

func calNames(cals []nc.Calendar) []string {
	out := make([]string, len(cals))
	for i, c := range cals {
		out[i] = c.Name
	}
	return out
}

// findCalendar matches name against calendar IDs and display names.
func findCalendar(cals []nc.Calendar, name string) (nc.Calendar, bool) {
	n := text.Normalize(name)
	for _, c := range cals {
		if text.Normalize(c.ID) == n || text.Normalize(c.Name) == n {
			return c, true
		}
	}
	return nc.Calendar{}, false
}

// fetchEvents queries every calendar in [from,to) and keeps the instances
// that really overlap the range, sorted by start.
func (d *Deps) fetchEvents(ctx context.Context, cals []nc.Calendar, from, to time.Time, expand bool) ([]calEvent, error) {
	var mu sync.Mutex
	var out []calEvent
	err := parallel(ctx, len(cals), func(ctx context.Context, i int) error {
		objs, err := d.NC.Query(ctx, cals[i].ID, "VEVENT", &from, &to, expand)
		if err != nil {
			return err
		}
		var local []calEvent
		for _, o := range objs {
			for _, e := range ical.ParseVEvents(o.Data, d.loc()) {
				if !eventOverlaps(e, from, to) {
					continue
				}
				local = append(local, calEvent{Event: e, Cal: cals[i], Href: o.Href, ETag: o.ETag})
			}
		}
		mu.Lock()
		out = append(out, local...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEvents(out)
	return out, nil
}

func eventOverlaps(e ical.Event, from, to time.Time) bool {
	if !e.Start.Before(to) {
		return false
	}
	return e.End.After(from) || !e.Start.Before(from)
}

func sortEvents(evs []calEvent) {
	sort.SliceStable(evs, func(i, j int) bool {
		if !evs[i].Start.Equal(evs[j].Start) {
			return evs[i].Start.Before(evs[j].Start)
		}
		return evs[i].Summary < evs[j].Summary
	})
}

// fetchTodos reads every task of cals (no range), in parallel.
func (d *Deps) fetchTodos(ctx context.Context, cals []nc.Calendar) ([]calTodo, error) {
	var mu sync.Mutex
	var out []calTodo
	err := parallel(ctx, len(cals), func(ctx context.Context, i int) error {
		objs, err := d.NC.Query(ctx, cals[i].ID, "VTODO", nil, nil, false)
		if err != nil {
			return err
		}
		local := make([]calTodo, 0, len(objs))
		for _, o := range objs {
			local = append(local, calTodo{Todo: ical.ParseVTodo(o.Data, d.loc()), Cal: cals[i], Obj: o})
		}
		mu.Lock()
		out = append(out, local...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cal.Name != out[j].Cal.Name {
			return out[i].Cal.Name < out[j].Cal.Name
		}
		return out[i].Summary < out[j].Summary
	})
	return out, nil
}

func (d *Deps) fmtEvent(e calEvent) string {
	if e.AllDay {
		return text.Human(e.Start, d.now(), false) + " (todo el día) — " + e.Summary + " [" + e.Cal.Name + "]"
	}
	return text.Human(e.Start, d.now(), true) + " — " + e.Summary + " [" + e.Cal.Name + "]"
}

func newUID() string {
	return fmt.Sprintf("%d@xiaozhi-nextcloud", time.Now().UnixNano())
}

func parseDay(s string, loc *time.Location) (time.Time, error) {
	t, _, err := text.ParseWhen(s, loc)
	if err != nil {
		return time.Time{}, err
	}
	return dayStart(t), nil
}

func (d *Deps) agenda(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	today := dayStart(d.now())
	from, last := today, today
	desde, hasta := str(req, "desde"), str(req, "hasta")
	var err error
	if desde != "" {
		if from, err = parseDay(desde, d.loc()); err != nil {
			return fail(err.Error())
		}
		last = from
	}
	if hasta != "" {
		if last, err = parseDay(hasta, d.loc()); err != nil {
			return fail(err.Error())
		}
	}
	if last.Before(from) {
		return fail("La fecha hasta es anterior a desde.")
	}
	to := last.AddDate(0, 0, 1)
	if days := int(to.Sub(from).Hours()/24 + 0.5); days > maxAgendaDays {
		return fail(fmt.Sprintf("El rango es de %d días; el máximo es %d.", days, maxAgendaDays))
	}
	cals, err := d.NC.Calendars(ctx)
	if err != nil {
		return failErr("No pude leer los calendarios", err)
	}
	evs, err := d.fetchEvents(ctx, eventCalendars(cals), from, to, true)
	if err != nil {
		return failErr("No pude leer la agenda", err)
	}
	label := "de " + text.Human(from, d.now(), false)
	if !last.Equal(from) {
		label = "del " + fmtDate(from) + " al " + fmtDate(last)
	}
	if len(evs) == 0 {
		return ok("No hay eventos en la agenda " + label + ".")
	}
	lines := make([]string, len(evs))
	for i, e := range evs {
		lines[i] = d.fmtEvent(e)
	}
	return ok("Agenda " + label + ": " + text.List(lines, maxListItems))
}

var repetirRules = map[string]string{
	"diario": "FREQ=DAILY", "diaria": "FREQ=DAILY", "semanal": "FREQ=WEEKLY",
	"mensual": "FREQ=MONTHLY", "anual": "FREQ=YEARLY",
}

func parseRepetir(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if r, found := repetirRules[text.Normalize(s)]; found {
		return r, nil
	}
	if up := strings.ToUpper(strings.TrimSpace(s)); strings.HasPrefix(up, "FREQ=") && !strings.ContainsAny(up, "\r\n") {
		return up, nil
	}
	return "", fmt.Errorf("Repetición no válida: %s (usa diario, semanal, mensual o anual)", s)
}

func (d *Deps) eventoCrear(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "calendario", "titulo", "inicio"); r != nil {
		return r
	}
	titulo := str(req, "titulo")
	start, isDate, err := text.ParseWhen(str(req, "inicio"), d.loc())
	if err != nil {
		return fail(err.Error())
	}
	allDay := isDate || flag(req, "todo_el_dia")
	rrule, err := parseRepetir(str(req, "repetir"))
	if err != nil {
		return fail(err.Error())
	}
	var end time.Time
	defaultHour := false
	if fin := str(req, "fin"); fin != "" {
		e, finIsDate, err := text.ParseWhen(fin, d.loc())
		if err != nil {
			return fail(err.Error())
		}
		if allDay {
			end = dayStart(e).AddDate(0, 0, 1) // fin is the inclusive last day
			if !end.After(dayStart(start)) {
				return fail("El fin es anterior al inicio.")
			}
		} else {
			if finIsDate {
				return fail("Indica la hora de fin (AAAA-MM-DDTHH:MM).")
			}
			end = e
			if !end.After(start) {
				return fail("El fin debe ser posterior al inicio.")
			}
		}
	} else if allDay {
		end = dayStart(start).AddDate(0, 0, 1)
	} else {
		end, defaultHour = start.Add(time.Hour), true
	}
	if allDay {
		start = dayStart(start)
	}

	all, err := d.NC.Calendars(ctx)
	if err != nil {
		return failErr("No pude leer los calendarios", err)
	}
	evCals := eventCalendars(all)
	cal, found := findCalendar(evCals, str(req, "calendario"))
	if !found {
		return fail("No encontré el calendario " + str(req, "calendario") + ". Calendarios: " + strings.Join(calNames(evCals), ", ") + ".")
	}

	day := dayStart(start)
	sameDay, err := d.fetchEvents(ctx, evCals, day, day.AddDate(0, 0, 1), true)
	if err != nil {
		return failErr("No pude revisar la agenda", err)
	}
	if !flag(req, "forzar") {
		for _, e := range sameDay {
			if text.Normalize(e.Summary) == text.Normalize(titulo) {
				return fail("Ya existe '" + e.Summary + "' ese día: " + d.fmtEvent(e) + ". Si igual quieres crearlo, repite con forzar.")
			}
		}
	}
	var overlaps []string
	if !allDay {
		for _, e := range sameDay {
			if !e.AllDay && e.Start.Before(end) && e.End.After(start) {
				overlaps = append(overlaps, d.fmtEvent(e))
			}
		}
	}

	ev := ical.Event{UID: newUID(), Summary: titulo, Location: str(req, "lugar"), Description: str(req, "descripcion"),
		RRule: rrule, Start: start, End: end, AllDay: allDay}
	if _, err := d.NC.PutObject(ctx, cal.ID, ev.UID, ical.MakeVEvent(ev, d.loc(), d.now()), ""); err != nil {
		return failErr("No pude crear el evento", err)
	}
	var b strings.Builder
	if allDay {
		fmt.Fprintf(&b, "Creé '%s' en %s, %s, todo el día", titulo, cal.Name, text.Human(start, d.now(), false))
		if last := end.AddDate(0, 0, -1); last.After(start) {
			b.WriteString(" hasta el " + fmtDate(last))
		}
		b.WriteString(".")
	} else {
		fmt.Fprintf(&b, "Creé '%s' en %s, %s a %s.", titulo, cal.Name, text.Human(start, d.now(), true), end.Format("15:04"))
	}
	if defaultHour {
		b.WriteString(" Dura 1 hora porque no dijiste hasta cuándo.")
	}
	if rrule != "" {
		b.WriteString(" Se repite (" + rrule + ").")
	}
	if len(overlaps) > 0 {
		b.WriteString(" Ojo, se cruza con: " + text.List(overlaps, 5) + ".")
	}
	return ok(b.String())
}

func (d *Deps) eventoCancelar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "evento"); r != nil {
		return r
	}
	ref := str(req, "evento")
	if text.Normalize(ref) == "" {
		return fail("No entendí qué evento quieres cancelar. Dime cuál.")
	}
	today := dayStart(d.now())
	from, to := today.AddDate(0, 0, -30), today.AddDate(0, 0, 366)
	if f := str(req, "fecha"); f != "" {
		day, err := parseDay(f, d.loc())
		if err != nil {
			return fail(err.Error())
		}
		from, to = day, day.AddDate(0, 0, 1)
	}
	all, err := d.NC.Calendars(ctx)
	if err != nil {
		return failErr("No pude leer los calendarios", err)
	}
	// No expand: the stored object (with its RRULE) is what gets deleted.
	evs, err := d.fetchEvents(ctx, eventCalendars(all), from, to, false)
	if err != nil {
		return failErr("No pude leer la agenda", err)
	}
	var uniq []calEvent
	seen := map[string]bool{}
	for _, e := range evs {
		if !seen[e.Href] {
			seen[e.Href] = true
			uniq = append(uniq, e)
		}
	}
	titles := make([]string, len(uniq))
	for i, e := range uniq {
		titles[i] = e.Summary
	}
	idx := text.Find(titles, ref)
	for i, e := range uniq { // accept a UID or file name too
		if len(idx) == 0 && (e.UID == ref || strings.TrimSuffix(lastPath(e.Href), ".ics") == ref) {
			idx = []int{i}
		}
	}
	switch {
	case len(idx) == 0:
		msg := "No encontré el evento " + ref + "."
		if c := text.Candidates(titles, ref, 5); len(c) > 0 {
			msg += " Parecidos: " + strings.Join(c, ", ") + "."
		}
		return fail(msg)
	case len(idx) > 1:
		var opts []string
		for _, i := range idx {
			opts = append(opts, d.fmtEvent(uniq[i]))
		}
		return fail("Hay varios eventos que coinciden: " + text.List(opts, 10) + ". Dime cuál, con su fecha.")
	}
	e := uniq[idx[0]]
	obj, err := d.NC.GetObject(ctx, e.Href)
	if err != nil {
		return failErr("No pude leer el evento", err)
	}
	summary := fmt.Sprintf("¿Borro el evento '%s' del %s de %s", e.Summary, fmtDate(e.Start.In(d.loc())), e.Cal.Name)
	if e.RRule != "" {
		summary += " (se borra la serie completa)"
	}
	summary += "?"
	return d.issue(confirm.Pending{Action: "evento_cancelar", Summary: summary, Href: obj.Href, ETag: obj.ETag})
}

// issue stores a pending action and returns the confirmation prompt.
func (d *Deps) issue(p confirm.Pending) *mcp.CallToolResult {
	code, err := d.Confirm.Issue(p)
	if err != nil {
		return failErr("No pude preparar la confirmación", err)
	}
	return ok(confirm.Prompt(quoteSafe(p.Summary), code))
}

func lastPath(p string) string {
	if i := strings.LastIndex(strings.TrimRight(p, "/"), "/"); i >= 0 {
		return strings.TrimRight(p, "/")[i+1:]
	}
	return p
}

func isCompleted(t ical.Todo) bool {
	return strings.EqualFold(t.Status, "COMPLETED") || strings.EqualFold(t.Status, "CANCELLED") || t.Completed != nil
}
