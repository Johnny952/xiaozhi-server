package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/ical"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

func recordatorioTools(d *Deps) []Tool {
	return []Tool{
		{
			Def: mcp.NewTool("recordatorio_crear",
				mcp.WithDescription("Crea un recordatorio con aviso a una hora. Úsala cuando el usuario pide que le recuerdes algo a una hora o en un momento concreto. La fecha debe incluir la hora."),
				reqStr("texto", "Qué hay que recordar"),
				reqStr("cuando", "Fecha y hora AAAA-MM-DDTHH:MM, en el futuro"),
			),
			Handle: handler(d.recordatorioCrear),
		},
		{
			Def: mcp.NewTool("recordatorio_listar",
				mcp.WithDescription("Lista los recordatorios futuros pendientes, ordenados por hora. Úsala cuando el usuario pregunta qué recordatorios tiene."),
			),
			Handle: handler(d.recordatorioListar),
		},
		{
			Def: mcp.NewTool("recordatorio_cancelar",
				mcp.WithDescription("Cancela un recordatorio, sin pedir confirmación. Indica parte del texto o el uid. Si varios coinciden, los lista y no cancela."),
				optStr("texto", "Texto (o parte) del recordatorio a cancelar"),
				optStr("uid", "UID exacto del recordatorio"),
			),
			Handle: handler(d.recordatorioCancelar),
		},
		{
			// Called by the server itself when the button is pressed; the
			// server must not offer it to the LLM.
			Def: mcp.NewTool("recordatorios_pendientes",
				mcp.WithDescription("Uso interno del servidor, no la llames. Devuelve lo que hay que decir al abrir la conversación: recordatorios vencidos sin leer y el resumen del día; vacío si no hay nada."),
			),
			Handle: handler(d.recordatoriosPendientes),
		},
	}
}

// Marks written on reminder VTODOs. The scheduler (MarkNotified) and the
// reading path (MarkSpoken) share them.
const (
	MarkNotified = "X-WEITA-NOTIFIED"
	MarkSpoken   = "X-WEITA-SPOKEN"
)

// staleAfter is how long after DUE a never-spoken reminder is read as
// "Tenías pendiente".
const staleAfter = 24 * time.Hour

// findRecordatorios returns the Recordatorios list if it exists.
func (d *Deps) findRecordatorios(ctx context.Context) (nc.Calendar, bool, error) {
	all, err := d.NC.Calendars(ctx)
	if err != nil {
		return nc.Calendar{}, false, err
	}
	c, found := findCalendar(todoCalendars(all, true), RecordatoriosList)
	return c, found, nil
}

// ensureRecordatorios finds the Recordatorios list or creates it.
func ensureRecordatorios(ctx context.Context, d *Deps) (nc.Calendar, error) {
	cal, found, err := d.findRecordatorios(ctx)
	if err != nil || found {
		return cal, err
	}
	cal, err = d.NC.MkCalendarTodos(ctx, RecordatoriosList)
	if errors.Is(err, nc.ErrCollision) {
		all, lerr := d.NC.Calendars(ctx)
		if lerr != nil {
			return nc.Calendar{}, lerr
		}
		if c, ok := findCollision(all, RecordatoriosList); ok {
			return c, nil
		}
	}
	return cal, err
}

// OpenRecordatorios returns the not-closed reminders of the list (none if the
// list does not exist yet), with href, etag and parsed VTODO.
func OpenRecordatorios(ctx context.Context, d *Deps) ([]calTodo, error) {
	cal, found, err := d.findRecordatorios(ctx)
	if err != nil || !found {
		return nil, err
	}
	todos, err := d.fetchTodos(ctx, []nc.Calendar{cal})
	if err != nil {
		return nil, err
	}
	var out []calTodo
	for _, t := range todos {
		if !isCompleted(t.Todo) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (d *Deps) recordatorioCrear(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "texto", "cuando"); r != nil {
		return r
	}
	texto := str(req, "texto")
	due, isDate, err := text.ParseWhen(str(req, "cuando"), d.loc())
	if err != nil {
		return fail(err.Error())
	}
	if isDate {
		return fail("Falta la hora del recordatorio. Pregúntale a qué hora lo quiere.")
	}
	now := d.now()
	if !due.After(now) {
		return fail("Esa hora ya pasó. Pídele una hora en el futuro.")
	}
	cal, err := ensureRecordatorios(ctx, d)
	if err != nil {
		return failErr("No pude preparar la lista de recordatorios", err)
	}
	todo := ical.Todo{UID: newUID(), Summary: texto, Due: &due, Alarm: true}
	if _, err := d.NC.PutObject(ctx, cal.ID, todo.UID, ical.MakeVTodo(todo, now), ""); err != nil {
		return failErr("No pude crear el recordatorio", err)
	}
	return ok("Listo, te recordaré '" + texto + "' " + text.Human(due, now, true) + ".")
}

func (d *Deps) recordatorioListar(ctx context.Context, _ mcp.CallToolRequest) *mcp.CallToolResult {
	todos, err := OpenRecordatorios(ctx, d)
	if err != nil {
		return failErr("No pude leer los recordatorios", err)
	}
	now := d.now()
	var future []calTodo
	for _, t := range todos {
		if t.Due != nil && t.Due.After(now) {
			future = append(future, t)
		}
	}
	if len(future) == 0 {
		return ok("No tienes recordatorios pendientes.")
	}
	sort.SliceStable(future, func(i, j int) bool { return future[i].Due.Before(*future[j].Due) })
	lines := make([]string, len(future))
	for i, t := range future {
		lines[i] = text.Human(*t.Due, now, !t.DueIsDate) + " — " + t.Summary
	}
	return ok(fmt.Sprintf("Recordatorios pendientes (%d): %s.", len(lines), text.List(lines, maxListItems)))
}

func (d *Deps) recordatorioCancelar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	texto, uid := str(req, "texto"), str(req, "uid")
	if texto == "" && uid == "" {
		return fail("Dime qué recordatorio cancelar: texto o uid.")
	}
	todos, err := OpenRecordatorios(ctx, d)
	if err != nil {
		return failErr("No pude leer los recordatorios", err)
	}
	var idx []int
	if uid != "" {
		for i, t := range todos {
			if t.UID == uid {
				idx = []int{i}
				break
			}
		}
	} else {
		titles := make([]string, len(todos))
		for i, t := range todos {
			titles[i] = t.Summary
		}
		idx = text.Find(titles, texto)
	}
	switch {
	case len(idx) == 0:
		return fail("No encontré ese recordatorio.")
	case len(idx) > 1:
		opts := make([]string, len(idx))
		for k, i := range idx {
			t := todos[i]
			opts[k] = t.Summary
			if t.Due != nil {
				opts[k] += " (" + text.Human(*t.Due, d.now(), !t.DueIsDate) + ")"
			}
		}
		return fail("Hay varios recordatorios que coinciden: " + text.List(opts, maxListItems) + ". Dime cuál.")
	}
	t := todos[idx[0]]
	data := ical.SetVTodoField(t.Obj.Data, "STATUS", "CANCELLED")
	if _, err := d.NC.PutObject(ctx, t.Cal.ID, objUID(t.Obj.Href), data, t.Obj.ETag); err != nil {
		return failErr("No pude cancelar el recordatorio", err)
	}
	return ok("Listo, cancelé el recordatorio '" + t.Summary + "'.")
}

// recordatoriosPendientes speaks the due reminders not yet spoken, marking
// them, and then the morning summary once per day after ResumenHora. The
// marking comes first so the summary leaves out what was just read. With
// nothing to say it answers "" (not an error) so the server stays silent.
func (d *Deps) recordatoriosPendientes(ctx context.Context, _ mcp.CallToolRequest) *mcp.CallToolResult {
	todos, err := OpenRecordatorios(ctx, d)
	if err != nil {
		return failErr("No pude leer los recordatorios", err)
	}
	now := d.now()
	var due []calTodo
	for _, t := range todos {
		if t.Due == nil || t.DueIsDate || t.Due.After(now) {
			continue
		}
		if _, _, spoken := ical.Field(t.Obj.Data, MarkSpoken); spoken {
			continue
		}
		due = append(due, t)
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].Due.Before(*due[j].Due) })

	var parts []string
	for _, t := range due {
		if !d.marcarHablado(ctx, t, now) {
			continue // not marked, so it is read on the next press
		}
		if now.Sub(*t.Due) > staleAfter {
			parts = append(parts, "Tenías pendiente: "+t.Summary+".")
		} else {
			parts = append(parts, "Recordatorio: "+t.Summary+".")
		}
	}
	if r := d.resumenPendiente(ctx, now); r != "" {
		parts = append(parts, r)
	}
	return ok(strings.Join(parts, " "))
}

// marcarHablado marks a reminder as spoken and completed. If the scheduler
// wrote its NOTIFIED mark between our read and this PUT (412), it re-reads that
// one VTODO once and retries, so the reminder is not delayed to the next press.
func (d *Deps) marcarHablado(ctx context.Context, t calTodo, now time.Time) bool {
	obj := t.Obj
	for attempt := 0; attempt < 2; attempt++ {
		data := ical.SetVTodoField(obj.Data, MarkSpoken, "1")
		data = ical.SetVTodoField(data, "STATUS", "COMPLETED")
		data = ical.SetVTodoField(data, "COMPLETED", ical.ToUTC(now))
		_, err := d.NC.PutObject(ctx, t.Cal.ID, objUID(obj.Href), data, obj.ETag)
		if err == nil {
			return true
		}
		if !errors.Is(err, nc.ErrConflict) || attempt == 1 {
			return false
		}
		fresh, gerr := d.NC.GetObject(ctx, obj.Href)
		if gerr != nil {
			return false
		}
		if _, _, spoken := ical.Field(fresh.Data, MarkSpoken); spoken {
			return false
		}
		obj = fresh
	}
	return false
}

// resumenPendiente returns the morning summary when it is due and not yet
// read today, and marks it read. Any failure just skips it.
func (d *Deps) resumenPendiente(ctx context.Context, now time.Time) string {
	if d.State == nil || d.ResumenHora == "" {
		return ""
	}
	at, err := time.ParseInLocation("15:04", d.ResumenHora, d.loc())
	if err != nil {
		return ""
	}
	y, m, dd := now.Date()
	if now.Before(time.Date(y, m, dd, at.Hour(), at.Minute(), 0, 0, d.loc())) {
		return ""
	}
	today := now.Format("2006-01-02")
	st, err := d.State.Get()
	if err != nil || st.ResumenLeido == today {
		return ""
	}
	r, err := ResumenManana(ctx, d)
	if err != nil {
		return ""
	}
	if err := d.State.Set("resumen_leido", today); err != nil {
		return "" // would repeat on every press; better silent
	}
	return r
}
