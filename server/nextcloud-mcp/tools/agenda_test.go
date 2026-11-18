package tools

import (
	"errors"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/ical"
)

// veventICS builds a raw VCALENDAR with one VEVENT; start/end are iCal
// values (e.g. 20261118T130000Z or 20261120 for all-day).
func veventICS(uid, summary, start, end string, extra ...string) string {
	prop := func(name, v string) string {
		if len(v) == 8 {
			return name + ";VALUE=DATE:" + v
		}
		return name + ":" + v
	}
	l := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "BEGIN:VEVENT", "UID:" + uid, "SUMMARY:" + summary,
		prop("DTSTART", start), prop("DTEND", end)}
	l = append(l, extra...)
	l = append(l, "END:VEVENT", "END:VCALENDAR")
	return strings.Join(l, "\r\n") + "\r\n"
}

func todoICS(uid, summary string, due *time.Time, dueIsDate bool, prio int, status string) string {
	td := ical.Todo{UID: uid, Summary: summary, Due: due, DueIsDate: dueIsDate, Priority: prio, Status: status}
	if status == "COMPLETED" {
		c := testNow.Add(-time.Hour)
		td.Completed = &c
	}
	return ical.MakeVTodo(td, testNow)
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, testLoc)
	return &t
}

func TestAgendaDefaultTodayAllEventCalendars(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	w := f.AddCalendar("trabajo", "Trabajo", "VEVENT", "VTODO")
	f.AddCalendar("compras", "Compras", "VTODO")
	w.Objects["r.ics"] = veventICS("r", "Reunión", "20261118T180000Z", "20261118T190000Z")
	p.Objects["d.ics"] = veventICS("d", "Dentista", "20261118T130000Z", "20261118T140000Z")
	p.Objects["m.ics"] = veventICS("m", "Cumpleaños", "20261119T130000Z", "20261119T140000Z")

	out, isErr := call(t, All(d), "agenda", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "hoy 10:00 — Dentista [Personal]", "hoy 15:00 — Reunión [Trabajo]")
	mustNotContain(t, out, "Cumpleaños")
	if strings.Index(out, "Dentista") > strings.Index(out, "Reunión") {
		t.Errorf("not chronological:\n%s", out)
	}
}

func TestAgendaExpandsRecurrence(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	// What the server returns for an expanded weekly series: one object,
	// several VEVENT instances.
	p.Objects["yoga.ics"] = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
		"BEGIN:VEVENT\r\nUID:yoga\r\nSUMMARY:Yoga\r\nDTSTART:20261118T220000Z\r\nDTEND:20261118T230000Z\r\nRECURRENCE-ID:20261118T220000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:yoga\r\nSUMMARY:Yoga\r\nDTSTART:20261119T220000Z\r\nDTEND:20261119T230000Z\r\nRECURRENCE-ID:20261119T220000Z\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	out, isErr := call(t, All(d), "agenda", map[string]any{"desde": "2026-11-18", "hasta": "2026-11-19"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "hoy 19:00 — Yoga", "mañana 19:00 — Yoga")
	if !strings.Contains(f.LastReportBody(), "expand") {
		t.Errorf("REPORT must ask the server to expand: %s", f.LastReportBody())
	}
}

func onlyObject(t *testing.T, objs map[string]string, skip ...string) string {
	t.Helper()
	var found []string
outer:
	for n, data := range objs {
		for _, s := range skip {
			if n == s {
				continue outer
			}
		}
		found = append(found, data)
	}
	if len(found) != 1 {
		t.Fatalf("want 1 new object, got %d", len(found))
	}
	return found[0]
}

func TestEventoCrearDefaultsOneHourAndSaysSo(t *testing.T) {
	d, f := newDeps(t)
	f.AddCalendar("personal", "Personal", "VEVENT")
	out, isErr := call(t, All(d), "evento_crear", map[string]any{
		"calendario": "personal", "titulo": "Dentista", "inicio": "2026-11-19T15:00", "lugar": "Centro",
	})
	if isErr {
		t.Fatal(out)
	}
	data := onlyObject(t, f.Cal("personal").Objects)
	mustContain(t, data, "DTSTART;TZID=America/Santiago:20261119T150000", "DTEND;TZID=America/Santiago:20261119T160000",
		"SUMMARY:Dentista", "LOCATION:Centro", "BEGIN:VTIMEZONE")
	mustContain(t, out, "Dentista", "Personal", "mañana 15:00", "1 hora")
}

func TestEventoCrearAllDay(t *testing.T) {
	d, f := newDeps(t)
	f.AddCalendar("personal", "Personal", "VEVENT")
	out, isErr := call(t, All(d), "evento_crear", map[string]any{
		"calendario": "Personal", "titulo": "Feriado", "inicio": "2026-11-20",
	})
	if isErr {
		t.Fatal(out)
	}
	data := onlyObject(t, f.Cal("personal").Objects)
	mustContain(t, data, "DTSTART;VALUE=DATE:20261120", "DTEND;VALUE=DATE:20261121")
	mustContain(t, out, "todo el día")
	mustNotContain(t, out, "1 hora")

	// todo_el_dia with a datetime and a multi-day end (inclusive last day).
	f.Cal("personal").Objects = map[string]string{}
	out, isErr = call(t, All(d), "evento_crear", map[string]any{
		"calendario": "Personal", "titulo": "Viaje", "inicio": "2026-11-21T09:00", "fin": "2026-11-23", "todo_el_dia": true,
	})
	if isErr {
		t.Fatal(out)
	}
	data = onlyObject(t, f.Cal("personal").Objects)
	mustContain(t, data, "DTSTART;VALUE=DATE:20261121", "DTEND;VALUE=DATE:20261124")
}

func TestEventoCrearUnknownCalendarListsEventCalendars(t *testing.T) {
	d, f := newDeps(t)
	f.AddCalendar("personal", "Personal", "VEVENT")
	f.AddCalendar("trabajo", "Trabajo", "VEVENT", "VTODO")
	f.AddCalendar("compras", "Compras", "VTODO")
	out, isErr := call(t, All(d), "evento_crear", map[string]any{
		"calendario": "Gimnasio", "titulo": "Pesas", "inicio": "2026-11-19T15:00",
	})
	if !isErr {
		t.Fatalf("want error: %s", out)
	}
	mustContain(t, out, "No encontré", "Gimnasio", "Personal", "Trabajo")
	mustNotContain(t, out, "Compras")
	if f.PutCount != 0 {
		t.Error("nothing must be written")
	}
}

func TestEventoCrearDuplicateSameDayBlocks(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["d.ics"] = veventICS("d", "Dentista", "20261119T120000Z", "20261119T130000Z")
	out, isErr := call(t, All(d), "evento_crear", map[string]any{
		"calendario": "personal", "titulo": "dentista", "inicio": "2026-11-19T18:00",
	})
	if !isErr {
		t.Fatalf("want block: %s", out)
	}
	mustContain(t, out, "Ya existe", "Dentista", "forzar")
	if len(p.Objects) != 1 {
		t.Errorf("must not create, objects: %d", len(f.Cal("personal").Objects))
	}
}

func TestEventoCrearForzar(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["d.ics"] = veventICS("d", "Dentista", "20261119T120000Z", "20261119T130000Z")
	out, isErr := call(t, All(d), "evento_crear", map[string]any{
		"calendario": "personal", "titulo": "dentista", "inicio": "2026-11-19T18:00", "forzar": true,
	})
	if isErr {
		t.Fatal(out)
	}
	if len(f.Cal("personal").Objects) != 2 {
		t.Errorf("forzar must create")
	}
}

func TestEventoCrearOverlapCreatesAndMentions(t *testing.T) {
	d, f := newDeps(t)
	f.AddCalendar("personal", "Personal", "VEVENT")
	w := f.AddCalendar("trabajo", "Trabajo", "VEVENT")
	w.Objects["r.ics"] = veventICS("r", "Reunión", "20261119T180000Z", "20261119T190000Z") // 15:00-16:00 local
	out, isErr := call(t, All(d), "evento_crear", map[string]any{
		"calendario": "personal", "titulo": "Dentista", "inicio": "2026-11-19T15:30", "fin": "2026-11-19T16:30",
	})
	if isErr {
		t.Fatal(out)
	}
	if len(f.Cal("personal").Objects) != 1 {
		t.Error("overlap must still create")
	}
	mustContain(t, out, "Reunión", "15:00")
	mustNotContain(t, out, "1 hora")
}

func TestEventoCancelarReturnsPendingNoDelete(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["dentista-1.ics"] = veventICS("dentista-1", "Dentista", "20261119T120000Z", "20261119T130000Z")
	p.Objects["otro.ics"] = veventICS("otro", "Cine", "20261120T220000Z", "20261120T235900Z")
	out, isErr := call(t, All(d), "evento_cancelar", map[string]any{"evento": "dentista"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "PENDIENTE", "codigo=4821", "Dentista", "19/11/2026")
	mustNotContain(t, out, "serie completa")
	if _, ok := f.Cal("personal").Objects["dentista-1.ics"]; !ok {
		t.Fatal("must not delete before confirmar")
	}
	pend, err := d.Confirm.Take("4821")
	if err != nil {
		t.Fatal(err)
	}
	if pend.Action != "evento_cancelar" || !strings.HasSuffix(pend.Href, "/personal/dentista-1.ics") || pend.ETag == "" {
		t.Errorf("pending: %+v", pend)
	}
}

func TestEventoCancelarRecurringWarnsSeries(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["yoga.ics"] = veventICS("yoga", "Yoga", "20261118T220000Z", "20261118T230000Z", "RRULE:FREQ=WEEKLY;BYDAY=WE")
	out, isErr := call(t, All(d), "evento_cancelar", map[string]any{"evento": "yoga", "fecha": "2026-11-18"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "PENDIENTE", "Yoga", "(se borra la serie completa)")
}

func TestEventoCancelarAmbiguousOrMissingNoPending(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["a.ics"] = veventICS("a", "Dentista control", "20261119T120000Z", "20261119T130000Z")
	p.Objects["b.ics"] = veventICS("b", "Dentista limpieza", "20261126T120000Z", "20261126T130000Z")
	out, isErr := call(t, All(d), "evento_cancelar", map[string]any{"evento": "dentista"})
	if !isErr {
		t.Errorf("ambiguous must not issue: %s", out)
	}
	mustContain(t, out, "Dentista control", "Dentista limpieza")
	mustNotContain(t, out, "PENDIENTE")
	out, isErr = call(t, All(d), "evento_cancelar", map[string]any{"evento": "peluquería"})
	if !isErr {
		t.Errorf("missing must fail: %s", out)
	}
	mustContain(t, out, "No encontré")
	if _, err := d.Confirm.Take("4821"); !errors.Is(err, confirm.ErrUnknown) {
		t.Errorf("store must be empty, got %v", err)
	}
	if len(f.Cal("personal").Objects) != 2 {
		t.Error("nothing deleted")
	}
}

func TestResumenDelDiaFilter(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT", "VTODO")
	c := f.AddCalendar("casa", "Casa", "VTODO")
	p.Objects["ev.ics"] = veventICS("ev", "Dentista", "20261118T130000Z", "20261118T140000Z")
	p.Objects["ev2.ics"] = veventICS("ev2", "Cumpleaños", "20261119T130000Z", "20261119T140000Z")
	p.Objects["t1.ics"] = todoICS("t1", "Pagar luz", day(2026, 11, 15), true, 0, "")
	c.Objects["t2.ics"] = todoICS("t2", "Comprar pan", day(2026, 11, 18), true, 5, "")
	c.Objects["t3.ics"] = todoICS("t3", "Llamar banco", nil, false, 2, "")
	c.Objects["t4.ics"] = todoICS("t4", "Hecha ya", day(2026, 11, 17), true, 1, "COMPLETED")
	c.Objects["t5.ics"] = todoICS("t5", "Algún día", nil, false, 5, "")
	c.Objects["t6.ics"] = todoICS("t6", "Sin prioridad", nil, false, 0, "")
	c.Objects["t7.ics"] = todoICS("t7", "Futura", day(2026, 11, 25), true, 9, "")

	out, isErr := call(t, All(d), "resumen_del_dia", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Dentista", "Pagar luz", "Comprar pan", "Llamar banco", "[Casa]")
	mustNotContain(t, out, "Cumpleaños", "Hecha ya", "Algún día", "Sin prioridad", "Futura")
}
