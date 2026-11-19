package tools

import (
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/ical"
)

// recNow is Tuesday 2026-11-17 09:00 local.
var recNow = time.Date(2026, 11, 17, 9, 0, 0, 0, testLoc)

func recICS(uid, summary string, due time.Time, status string) string {
	return ical.MakeVTodo(ical.Todo{UID: uid, Summary: summary, Due: &due, Status: status, Alarm: true}, recNow)
}

func TestRecordatorioCrearCreaListaYAlarm(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	out, isErr := call(t, All(d), "recordatorio_crear", map[string]any{"texto": "llamar al proveedor", "cuando": "2026-11-17T15:00"})
	if isErr {
		t.Fatal(out)
	}
	c := f.Cal("recordatorios")
	if c == nil || c.Name != "Recordatorios" {
		t.Fatalf("calendar not created: %+v", c)
	}
	if len(c.Objects) != 1 {
		t.Fatalf("objects: %v", c.Objects)
	}
	for _, data := range c.Objects {
		mustContain(t, data, "SUMMARY:llamar al proveedor", "DUE:20261117T180000Z", "BEGIN:VALARM")
	}
	mustContain(t, out, "llamar al proveedor", "hoy 15:00")
}

func TestRecordatorioCrearSinHoraPideHora(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	out, isErr := call(t, All(d), "recordatorio_crear", map[string]any{"texto": "llamar", "cuando": "2026-11-18"})
	if !isErr || strings.Contains(out, "\n") {
		t.Errorf("want one-line error: %q", out)
	}
	mustContain(t, out, "hora")
	if f.Cal("recordatorios") != nil {
		t.Error("must not create the list or any reminder")
	}
}

func TestRecordatorioCrearEnElPasado(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	out, isErr := call(t, All(d), "recordatorio_crear", map[string]any{"texto": "llamar", "cuando": "2026-11-17T08:00"})
	if !isErr {
		t.Errorf("want error: %s", out)
	}
	mustContain(t, out, "pasó")
	if f.Cal("recordatorios") != nil {
		t.Error("must not create anything")
	}
}

func TestRecordatorioListarOrdenadoFuturos(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	c := f.AddCalendar("recordatorios", "Recordatorios", "VTODO")
	h := func(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, testLoc) }
	c.Objects["p.ics"] = recICS("p", "Ya pasó", h(17, 8), "")
	c.Objects["x.ics"] = recICS("x", "Cancelado", h(18, 9), "CANCELLED")
	c.Objects["b.ics"] = recICS("b", "Segundo", h(19, 10), "")
	c.Objects["a.ics"] = recICS("a", "Primero", h(17, 15), "")
	out, isErr := call(t, All(d), "recordatorio_listar", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Primero", "Segundo")
	mustNotContain(t, out, "Ya pasó", "Cancelado")
	if !(strings.Index(out, "Primero") < strings.Index(out, "Segundo")) {
		t.Errorf("not sorted by due: %s", out)
	}
}

func TestRecordatorioCancelarUnico(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	c := f.AddCalendar("recordatorios", "Recordatorios", "VTODO")
	c.Objects["a.ics"] = recICS("a", "Llamar al proveedor", time.Date(2026, 11, 17, 15, 0, 0, 0, testLoc), "")
	out, isErr := call(t, All(d), "recordatorio_cancelar", map[string]any{"texto": "proveedor"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, c.Objects["a.ics"], "STATUS:CANCELLED")
	mustContain(t, out, "Llamar al proveedor")
	mustNotContain(t, out, "confirm")
}

func TestRecordatorioCancelarAmbiguo(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	c := f.AddCalendar("recordatorios", "Recordatorios", "VTODO")
	c.Objects["a.ics"] = recICS("a", "Llamar al proveedor", time.Date(2026, 11, 17, 15, 0, 0, 0, testLoc), "")
	c.Objects["b.ics"] = recICS("b", "Llamar al dentista", time.Date(2026, 11, 18, 11, 0, 0, 0, testLoc), "")
	out, isErr := call(t, All(d), "recordatorio_cancelar", map[string]any{"texto": "llamar"})
	if !isErr {
		t.Errorf("want error: %s", out)
	}
	mustContain(t, out, "proveedor", "dentista")
	mustNotContain(t, c.Objects["a.ics"], "STATUS:CANCELLED")
	mustNotContain(t, c.Objects["b.ics"], "STATUS:CANCELLED")
}

func TestRecordatorioCancelarPorUID(t *testing.T) {
	d, f := newDeps(t)
	d.Now = func() time.Time { return recNow }
	c := f.AddCalendar("recordatorios", "Recordatorios", "VTODO")
	c.Objects["a.ics"] = recICS("uid-a", "Llamar al proveedor", time.Date(2026, 11, 17, 15, 0, 0, 0, testLoc), "")
	c.Objects["b.ics"] = recICS("uid-b", "Llamar al dentista", time.Date(2026, 11, 18, 11, 0, 0, 0, testLoc), "")
	out, isErr := call(t, All(d), "recordatorio_cancelar", map[string]any{"uid": "uid-b"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, c.Objects["b.ics"], "STATUS:CANCELLED")
	mustNotContain(t, c.Objects["a.ics"], "STATUS:CANCELLED")
}
