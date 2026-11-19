package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/text"
)

// Section limits of the morning summary (spec: Resumen de la mañana).
const (
	maxResumenEventos       = 5
	maxResumenRecordatorios = 5
	maxResumenVencenHoy     = 5
	maxResumenPrioridad     = 3
	maxResumenAtrasadas     = 5
)

var weekdayNames = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// ResumenManana builds the short spoken summary of today: events, reminders,
// tasks due today, high priority tasks and overdue tasks.
func ResumenManana(ctx context.Context, d *Deps) (string, error) {
	now := d.now()
	today := dayStart(now)
	cals, err := d.NC.Calendars(ctx)
	if err != nil {
		return "", fmt.Errorf("leer los calendarios: %w", err)
	}
	var (
		evs   []calEvent
		todos []calTodo
		recs  []calTodo
	)
	err = parallel(ctx, 3, func(ctx context.Context, i int) (err error) {
		switch i {
		case 0:
			evs, err = d.fetchEvents(ctx, eventCalendars(cals), today, today.AddDate(0, 0, 1), true)
		case 1:
			todos, err = d.fetchTodos(ctx, todoCalendars(cals, false))
		default:
			recs, err = openRecordatorios(ctx, d)
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("leer la agenda: %w", err)
	}
	return composeResumen(now, evs, todos, recs), nil
}

// composeResumen is the pure part: it renders already fetched data.
func composeResumen(now time.Time, evs []calEvent, todos, recs []calTodo) string {
	today := dayStart(now)
	tomorrow := today.AddDate(0, 0, 1)

	var dueToday, high, overdue []calTodo
	for _, t := range todos {
		if isCompleted(t.Todo) {
			continue
		}
		switch {
		case t.Due != nil && t.Due.Before(today):
			overdue = append(overdue, t)
		case t.Due != nil && t.Due.Before(tomorrow):
			dueToday = append(dueToday, t)
		case t.Priority >= 1 && t.Priority <= 4:
			high = append(high, t)
		}
	}
	byDue := func(l []calTodo) {
		sort.SliceStable(l, func(i, j int) bool { return l[i].Due.Before(*l[j].Due) })
	}
	byDue(dueToday)
	byDue(overdue)
	sort.SliceStable(high, func(i, j int) bool { return high[i].Priority < high[j].Priority })

	names := titleNames(dueToday, high, overdue)

	head := "Hoy " + weekdayNames[now.Weekday()] + fmt.Sprintf(" %d", now.Day())
	var agenda []string
	for _, s := range []string{eventLines(evs, today, tomorrow), recordatorioLines(recs, today, tomorrow)} {
		if s != "" {
			agenda = append(agenda, s)
		}
	}
	parts := []string{head + "."}
	if len(agenda) > 0 {
		parts[0] = head + ": " + strings.Join(agenda, "; ") + "."
	}
	if len(dueToday) > 0 {
		parts = append(parts, "Vence hoy: "+text.List(mapTodos(dueToday, names), maxResumenVencenHoy)+".")
	}
	if len(high) > 0 {
		parts = append(parts, "Prioridad alta: "+text.List(mapTodos(high, names), maxResumenPrioridad)+".")
	}
	if len(overdue) > 0 {
		lines := mapTodos(overdue, func(t calTodo) string { return names(t) + ", " + relativeDay(*t.Due, today) })
		parts = append(parts, "Quedaron atrasadas: "+text.List(lines, maxResumenAtrasadas)+".")
		if len(overdue) == 1 {
			parts = append(parts, "¿La reagendo o la cancelo?")
		} else {
			parts = append(parts, "¿Las reagendo o las cancelo?")
		}
	}
	if len(parts) == 1 && len(agenda) == 0 {
		return "Hoy no tienes nada agendado."
	}
	return strings.Join(parts, " ")
}

// eventLines renders today's events, all-day first, or "" if there are none.
func eventLines(evs []calEvent, today, tomorrow time.Time) string {
	var l []calEvent
	for _, e := range evs {
		if e.AllDay || (!e.Start.Before(today) && e.Start.Before(tomorrow)) {
			l = append(l, e)
		}
	}
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].AllDay != l[j].AllDay {
			return l[i].AllDay
		}
		return l[i].Start.Before(l[j].Start)
	})
	lines := make([]string, len(l))
	for i, e := range l {
		if e.AllDay {
			lines[i] = "todo el día, " + e.Summary
		} else {
			lines[i] = spokenHour(e.Start) + ", " + e.Summary
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return text.List(lines, maxResumenEventos)
}

// recordatorioLines renders the open reminders due today, by time, or "".
func recordatorioLines(recs []calTodo, today, tomorrow time.Time) string {
	var l []calTodo
	for _, r := range recs {
		if r.Due != nil && !r.Due.Before(today) && r.Due.Before(tomorrow) {
			l = append(l, r)
		}
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].Due.Before(*l[j].Due) })
	lines := make([]string, len(l))
	for i, r := range l {
		lines[i] = spokenHour(*r.Due) + ", recordatorio de " + r.Summary
	}
	if len(lines) == 0 {
		return ""
	}
	return text.List(lines, maxResumenRecordatorios)
}

// titleNames returns each task's spoken title: the bare summary, plus the
// list name in brackets only when another listed task has the same title.
func titleNames(lists ...[]calTodo) func(calTodo) string {
	count := map[string]int{}
	for _, l := range lists {
		for _, t := range l {
			count[text.Normalize(t.Summary)]++
		}
	}
	return func(t calTodo) string {
		if count[text.Normalize(t.Summary)] > 1 {
			return t.Summary + " [" + t.Cal.Name + "]"
		}
		return t.Summary
	}
}

func mapTodos(l []calTodo, f func(calTodo) string) []string {
	out := make([]string, len(l))
	for i, t := range l {
		out[i] = f(t)
	}
	return out
}

// spokenHour renders a time of day as "a las 10", "a las 9 y 30", "a la 1".
func spokenHour(t time.Time) string {
	s := "a las "
	if t.Hour() == 1 {
		s = "a la "
	}
	s += fmt.Sprint(t.Hour())
	if t.Minute() != 0 {
		s += fmt.Sprintf(" y %d", t.Minute())
	}
	return s
}

// relativeDay names a past due date: "de ayer", "del lunes" (2-6 days back)
// or "del 3 de noviembre".
func relativeDay(due, today time.Time) string {
	dd := dayStart(due.In(today.Location()))
	days := int(today.Sub(dd).Round(24*time.Hour) / (24 * time.Hour))
	switch {
	case days <= 1:
		return "de ayer"
	case days <= 6:
		return "del " + weekdayNames[dd.Weekday()]
	}
	return fmt.Sprintf("del %d de %s", dd.Day(), text.MonthName(dd.Month()))
}

func (d *Deps) resumenDelDia(ctx context.Context, _ mcp.CallToolRequest) *mcp.CallToolResult {
	out, err := ResumenManana(ctx, d)
	if err != nil {
		return failErr("No pude armar el resumen", err)
	}
	return ok(out)
}
