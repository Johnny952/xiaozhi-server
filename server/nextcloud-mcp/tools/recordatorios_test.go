package tools

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/ical"
	"nextcloud-mcp/nctest"
	"nextcloud-mcp/state"
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

// ---- recordatorios_pendientes ----

func pendFixture(t *testing.T, now time.Time, hora string) (*Deps, *nctest.FakeCal, *state.Store) {
	t.Helper()
	d, f := newDeps(t)
	d.Now = func() time.Time { return now }
	d.ResumenHora = hora
	st := state.Open(filepath.Join(t.TempDir(), "recordatorios-state.json"))
	d.State = st
	f.AddCalendar("personal", "Personal", "VEVENT")
	return d, f.AddCalendar("recordatorios", "Recordatorios", "VTODO"), st
}

func addRec(c *nctest.FakeCal, uid, summary string, due time.Time) {
	c.Objects[uid+".ics"] = ical.MakeVTodo(ical.Todo{UID: uid, Summary: summary, Due: &due, Alarm: true}, due.Add(-time.Hour))
}

func pendientes(t *testing.T, d *Deps) string {
	t.Helper()
	out, isErr := call(t, All(d), "recordatorios_pendientes", nil)
	if isErr {
		t.Fatal(out)
	}
	return out
}

func TestPendientesLeeVencidosYMarca(t *testing.T) {
	now := at(18, 8, 0)
	d, c, _ := pendFixture(t, now, "")
	addRec(c, "a", "llamar al proveedor", now.Add(-10*time.Minute))
	out := pendientes(t, d)
	mustContain(t, out, "llamar al proveedor")
	mustNotContain(t, out, "Tenías pendiente")
	mustContain(t, c.Objects["a.ics"], "X-WEITA-SPOKEN:", "STATUS:COMPLETED", "COMPLETED:")
}

func TestPendientesNoLeeFuturos(t *testing.T) {
	now := at(18, 8, 0)
	d, c, _ := pendFixture(t, now, "")
	addRec(c, "a", "mas tarde", now.Add(2*time.Hour))
	if out := pendientes(t, d); out != "" {
		t.Errorf("want empty, got %q", out)
	}
	mustNotContain(t, c.Objects["a.ics"], "X-WEITA-SPOKEN")
}

func TestPendientesViejoTeniasPendiente(t *testing.T) {
	now := at(18, 8, 0)
	d, c, _ := pendFixture(t, now, "")
	addRec(c, "a", "pagar el seguro", now.Add(-30*time.Hour))
	out := pendientes(t, d)
	if !strings.HasPrefix(out, "Tenías pendiente") {
		t.Errorf("want Tenías pendiente prefix: %q", out)
	}
	mustContain(t, out, "pagar el seguro")
}

func TestPendientesNoRepite(t *testing.T) {
	now := at(18, 8, 0)
	d, c, _ := pendFixture(t, now, "")
	addRec(c, "a", "llamar", now.Add(-10*time.Minute))
	if out := pendientes(t, d); out == "" {
		t.Fatal("first call must speak")
	}
	if out := pendientes(t, d); out != "" {
		t.Errorf("second call must be empty, got %q", out)
	}
}

func TestPendientesYaHabladoNoSeRepite(t *testing.T) {
	now := at(18, 8, 0)
	d, c, _ := pendFixture(t, now, "")
	addRec(c, "a", "llamar", now.Add(-10*time.Minute))
	c.Objects["a.ics"] = ical.SetVTodoField(c.Objects["a.ics"], "X-WEITA-SPOKEN", "1")
	if out := pendientes(t, d); out != "" {
		t.Errorf("want empty, got %q", out)
	}
}

func TestPendientesIncluyeResumenUnaVez(t *testing.T) {
	now := at(18, 8, 0)
	d, c, st := pendFixture(t, now, "07:30")
	addRec(c, "a", "llamar al proveedor", now.Add(-10*time.Minute))
	out := pendientes(t, d)
	i, j := strings.Index(out, "llamar al proveedor"), strings.Index(out, "Hoy no tienes nada agendado")
	if i < 0 || j < 0 || i > j {
		t.Errorf("want reminders first, then summary: %q", out)
	}
	got, err := st.Get()
	if err != nil || got.ResumenLeido != "2025-11-18" {
		t.Errorf("resumen_leido: %+v %v", got, err)
	}
	if out := pendientes(t, d); out != "" {
		t.Errorf("second call must be empty, got %q", out)
	}
}

func TestPendientesResumenAntesDeHora(t *testing.T) {
	d, _, st := pendFixture(t, at(18, 7, 0), "07:30")
	if out := pendientes(t, d); out != "" {
		t.Errorf("want empty before the hour, got %q", out)
	}
	if got, _ := st.Get(); got.ResumenLeido != "" {
		t.Errorf("must not mark: %+v", got)
	}
}

func TestPendientesResumenSinLosRecienLeidos(t *testing.T) {
	now := at(18, 8, 0)
	d, c, _ := pendFixture(t, now, "07:30")
	addRec(c, "a", "llamar al proveedor", now.Add(-10*time.Minute))
	addRec(c, "b", "sacar la basura", now.Add(5*time.Hour))
	out := pendientes(t, d)
	if n := strings.Count(out, "llamar al proveedor"); n != 1 {
		t.Errorf("reminder spoken %d times: %q", n, out)
	}
	mustContain(t, out, "sacar la basura")
}

func TestPendientesNadaDevuelveVacio(t *testing.T) {
	d, _ := newDeps(t)
	d.State = state.Open(filepath.Join(t.TempDir(), "s.json"))
	out, isErr := call(t, All(d), "recordatorios_pendientes", nil)
	if isErr || out != "" {
		t.Errorf("want empty non-error, got %q err=%v", out, isErr)
	}
}
