package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/nctest"
)

// resNow is Tuesday 2025-11-18 07:30 local (the spec example says "martes 18").
var resNow = time.Date(2025, 11, 18, 7, 30, 0, 0, testLoc)

// resFixture is a fake with an events+tasks calendar, a "Casa" task list and a
// Recordatorios list, with Now fixed to resNow.
type resFixture struct {
	d    *Deps
	f    *nctest.Fake
	ev   *nctest.FakeCal
	todo *nctest.FakeCal
	rec  *nctest.FakeCal
	n    int
}

func newResFixture(t *testing.T) *resFixture {
	d, f := newDeps(t)
	d.Now = func() time.Time { return resNow }
	return &resFixture{
		d:    d,
		f:    f,
		ev:   f.AddCalendar("personal", "Personal", "VEVENT"),
		todo: f.AddCalendar("casa", "Casa", "VTODO"),
		rec:  f.AddCalendar("recordatorios", "Recordatorios", "VTODO"),
	}
}

func (x *resFixture) key() string { x.n++; return fmt.Sprintf("k%d", x.n) }

func utc(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

func at(day, hour, min int) time.Time {
	return time.Date(2025, 11, day, hour, min, 0, 0, testLoc)
}

func (x *resFixture) event(title string, start time.Time) {
	k := x.key()
	x.ev.Objects[k+".ics"] = veventICS(k, title, utc(start), utc(start.Add(time.Hour)))
}

func (x *resFixture) allDay(title string, day int) {
	k := x.key()
	x.ev.Objects[k+".ics"] = veventICS(k, title, fmt.Sprintf("202511%02d", day), fmt.Sprintf("202511%02d", day+1))
}

func (x *resFixture) reminder(title string, due time.Time, status string) {
	k := x.key()
	x.rec.Objects[k+".ics"] = recICS(k, title, due, status)
}

// task adds a task to the cal; day 0 means no due date.
func (x *resFixture) task(cal *nctest.FakeCal, title string, day, prio int, status string) {
	k := x.key()
	var due *time.Time
	if day != 0 {
		due = day2(day)
	}
	cal.Objects[k+".ics"] = todoICS(k, title, due, true, prio, status)
}

func day2(d int) *time.Time {
	t := time.Date(2025, 11, d, 0, 0, 0, 0, testLoc)
	return &t
}

func (x *resFixture) run(t *testing.T) string {
	t.Helper()
	out, err := ResumenManana(context.Background(), x.d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestResumenEjemploSpec(t *testing.T) {
	x := newResFixture(t)
	x.event("dentista", at(18, 10, 0))
	x.reminder("llamar al proveedor", at(18, 15, 0), "")
	x.task(x.todo, "pagar la luz", 18, 5, "")
	x.task(x.todo, "preparar presupuesto", 0, 2, "")
	x.task(x.todo, "enviar factura", 17, 5, "")
	want := "Hoy martes 18: a las 10, dentista; a las 15, recordatorio de llamar al proveedor. Vence hoy: pagar la luz. Prioridad alta: preparar presupuesto. Quedaron atrasadas: enviar factura, de ayer. ¿La reagendo o la cancelo?"
	if got := x.run(t); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestResumenNadaAgendado(t *testing.T) {
	x := newResFixture(t)
	if got := x.run(t); got != "Hoy no tienes nada agendado." {
		t.Errorf("got %q", got)
	}
}

func TestResumenSinListaRecordatorios(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return resNow }
	f.AddCalendar("personal", "Personal", "VEVENT")
	out, err := ResumenManana(context.Background(), d)
	if err != nil || out != "Hoy no tienes nada agendado." {
		t.Errorf("got %q, %v", out, err)
	}
	if f.Cal("recordatorios") != nil {
		t.Error("must not create the list")
	}
}

func TestResumenOmiteSeccionesVacias(t *testing.T) {
	x := newResFixture(t)
	x.task(x.todo, "enviar factura", 17, 5, "")
	got := x.run(t)
	mustContain(t, got, "Quedaron atrasadas: enviar factura, de ayer. ¿La reagendo o la cancelo?")
	mustNotContain(t, got, "Vence hoy", "Prioridad alta", "recordatorio", "a las")
}

func TestResumenTodoElDiaPrimero(t *testing.T) {
	x := newResFixture(t)
	x.event("dentista", at(18, 10, 0))
	x.allDay("feriado", 18)
	got := x.run(t)
	mustContain(t, got, "Hoy martes 18: todo el día, feriado; a las 10, dentista.")
}

func TestResumenHoraConMinutos(t *testing.T) {
	x := newResFixture(t)
	x.event("yoga", at(18, 1, 0))
	x.event("cita", at(18, 9, 30))
	mustContain(t, x.run(t), "a la 1, yoga; a las 9 y 30, cita.")
}

func TestResumenLimites(t *testing.T) {
	x := newResFixture(t)
	for i := 0; i < 7; i++ {
		x.event(fmt.Sprintf("evento%d", i), at(18, 8+i, 0))
	}
	for i := 0; i < 6; i++ {
		x.task(x.todo, fmt.Sprintf("vieja%d", i), 10+i, 5, "")
	}
	for i := 0; i < 4; i++ {
		x.task(x.todo, fmt.Sprintf("urgente%d", i), 0, 1, "")
	}
	got := x.run(t)
	mustContain(t, got, "evento4", "y 2 más", "vieja4", "y 1 más")
	mustNotContain(t, got, "evento5", "vieja5")
	if n := strings.Count(got, "urgente"); n != 3 {
		t.Errorf("want 3 urgent, got %d:\n%s", n, got)
	}
}

func TestResumenAtrasadasPlural(t *testing.T) {
	x := newResFixture(t)
	x.task(x.todo, "uno", 17, 5, "")
	x.task(x.todo, "dos", 16, 5, "") // domingo
	x.task(x.todo, "tres", 3, 5, "")
	got := x.run(t)
	mustContain(t, got, "uno, de ayer", "dos, del domingo", "tres, del 3 de noviembre", "¿Las reagendo o las cancelo?")
	if !(strings.Index(got, "tres") < strings.Index(got, "dos") && strings.Index(got, "dos") < strings.Index(got, "uno")) {
		t.Errorf("want most overdue first:\n%s", got)
	}
}

func TestResumenAtrasadasFechasRelativas(t *testing.T) {
	for _, c := range []struct {
		day  int
		want string
	}{
		{17, "x, de ayer"},
		{14, "x, del viernes"},
		{12, "x, del miércoles"}, // 6 days back
		{11, "x, del 11 de noviembre"},
	} {
		x := newResFixture(t)
		x.task(x.todo, "x", c.day, 5, "")
		mustContain(t, x.run(t), c.want)
	}
}

func TestResumenPrioridadAlta(t *testing.T) {
	x := newResFixture(t)
	x.task(x.todo, "sin fecha", 0, 1, "")
	x.task(x.todo, "futura", 25, 4, "")
	x.task(x.todo, "media", 0, 5, "")
	x.task(x.todo, "baja", 0, 9, "")
	x.task(x.todo, "sin prio", 0, 0, "")
	x.task(x.todo, "hoy alta", 18, 1, "")
	x.task(x.todo, "vencida alta", 17, 1, "")
	got := x.run(t)
	mustContain(t, got, "Prioridad alta: sin fecha; futura.", "Vence hoy: hoy alta.")
	mustNotContain(t, got, "media", "baja", "sin prio")
	if strings.Count(got, "hoy alta") != 1 || strings.Count(got, "vencida alta") != 1 {
		t.Errorf("duplicated across sections:\n%s", got)
	}
}

func TestResumenListaSoloSiDuplicado(t *testing.T) {
	x := newResFixture(t)
	x.task(x.todo, "comprar pan", 18, 5, "")
	x.task(x.todo, "pagar luz", 18, 5, "")
	other := x.f.AddCalendar("trabajo", "Trabajo", "VTODO")
	x.task(other, "comprar pan", 18, 5, "")
	got := x.run(t)
	mustContain(t, got, "comprar pan [Casa]", "comprar pan [Trabajo]", "pagar luz")
	mustNotContain(t, got, "pagar luz [")
}

func TestResumenExcluyeCompletadosYCancelados(t *testing.T) {
	x := newResFixture(t)
	x.task(x.todo, "hecha", 18, 5, "COMPLETED")
	x.task(x.todo, "cancelada", 17, 5, "CANCELLED")
	x.reminder("recordatorio hecho", at(18, 9, 0), "COMPLETED")
	x.reminder("recordatorio cancelado", at(18, 10, 0), "CANCELLED")
	if got := x.run(t); got != "Hoy no tienes nada agendado." {
		t.Errorf("got %q", got)
	}
}

func TestResumenRecordatoriosNoSeCuentanComoTareas(t *testing.T) {
	x := newResFixture(t)
	x.reminder("llamar", at(18, 15, 0), "")
	x.reminder("de ayer", at(17, 15, 0), "")
	got := x.run(t)
	mustContain(t, got, "Hoy martes 18: a las 15, recordatorio de llamar.")
	mustNotContain(t, got, "Vence hoy", "atrasadas", "de ayer")
}

func TestResumenDelDiaDelega(t *testing.T) {
	x := newResFixture(t)
	x.event("dentista", at(18, 10, 0))
	out, isErr := call(t, All(x.d), "resumen_del_dia", map[string]any{})
	if isErr || out != "Hoy martes 18: a las 10, dentista." {
		t.Errorf("got %q (err=%v)", out, isErr)
	}
}
