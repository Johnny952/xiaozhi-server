package tools

import (
	"errors"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/ical"
)

func TestConfirmarExecutesTareaBorrar(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["a.ics"] = todoICS("a", "Comprar alambre fino", nil, false, 5, "")
	c.Objects["b.ics"] = todoICS("b", "Comprar alambre grueso", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "grueso"})
	if isErr {
		t.Fatal(out)
	}
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Comprar alambre grueso")
	mustNotContain(t, out, "PENDIENTE", "¿")
	if _, ok := f.Cal("joyeria").Objects["b.ics"]; ok {
		t.Error("task not deleted")
	}
	if _, ok := f.Cal("joyeria").Objects["a.ics"]; !ok {
		t.Error("other task deleted")
	}
	if f.LastDeleteHeader().Get("If-Match") == "" {
		t.Error("delete must carry If-Match")
	}
	// The code is single use.
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr || strings.Contains(out, "\n") {
		t.Errorf("reused code must fail in one line: %q", out)
	}
}

func TestConfirmarExecutesEventoCancelar(t *testing.T) {
	d, f := newDeps(t)
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["dentista-1.ics"] = veventICS("dentista-1", "Dentista", "20261119T120000Z", "20261119T130000Z")
	p.Objects["otro.ics"] = veventICS("otro", "Cine", "20261120T220000Z", "20261120T235900Z")
	out, isErr := call(t, All(d), "evento_cancelar", map[string]any{"evento": "dentista"})
	if isErr {
		t.Fatal(out)
	}
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Dentista")
	if _, ok := f.Cal("personal").Objects["dentista-1.ics"]; ok {
		t.Error("event not deleted")
	}
	if len(f.Cal("personal").Objects) != 1 {
		t.Error("other event deleted")
	}
}

func TestConfirmarExecutesTareaCambiar(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("casa", "Casa", "VTODO")
	c.Objects["t1.ics"] = todoICS("t1", "Arreglar llave", day(2026, 11, 20), true, 5, "")
	out, isErr := call(t, All(d), "tarea_cambiar", map[string]any{
		"proyecto": "casa", "tarea": "llave", "nuevo_titulo": "Cambiar llave", "vence": "2026-11-25", "prioridad": "alta",
	})
	if isErr {
		t.Fatal(out)
	}
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Arreglar llave")
	ics := f.Cal("casa").Objects["t1.ics"]
	if len(f.Cal("casa").Objects) != 1 {
		t.Fatalf("objects: %v", f.Cal("casa").Objects)
	}
	got := ical.ParseVTodo(ics, testLoc)
	if got.Summary != "Cambiar llave" || got.Priority != 1 || got.UID != "t1" {
		t.Errorf("todo: %+v", got)
	}
	if got.Due == nil || !got.DueIsDate || !got.Due.Equal(*day(2026, 11, 25)) {
		t.Errorf("due: %v %v", got.Due, got.DueIsDate)
	}
	mustContain(t, ics, "DUE;VALUE=DATE:20261125")
	if n := strings.Count(ics, "DUE"); n != 1 {
		t.Errorf("want one DUE, got %d:\n%s", n, ics)
	}

	// Timed due replaces the date due; untouched fields stay.
	out, isErr = call(t, All(d), "tarea_cambiar", map[string]any{"proyecto": "casa", "tarea": "llave", "vence": "2026-11-26T15:00"})
	if isErr {
		t.Fatal(out)
	}
	if out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"}); isErr {
		t.Fatal(out)
	}
	ics = f.Cal("casa").Objects["t1.ics"]
	got = ical.ParseVTodo(ics, testLoc)
	want := time.Date(2026, 11, 26, 15, 0, 0, 0, testLoc)
	if got.Due == nil || got.DueIsDate || !got.Due.Equal(want) || got.Summary != "Cambiar llave" || got.Priority != 1 {
		t.Errorf("todo: %+v due %v", got, got.Due)
	}
	if n := strings.Count(ics, "DUE"); n != 1 {
		t.Errorf("want one DUE, got %d:\n%s", n, ics)
	}
}

func TestConfirmarAcceptsNumber(t *testing.T) {
	for _, code := range []any{float64(4821), "4821", " 4821 "} {
		d, f := newDeps(t)
		c := f.AddCalendar("joyeria", "Joyería", "VTODO")
		c.Objects["b.ics"] = todoICS("b", "Comprar alambre", nil, false, 5, "")
		if out, isErr := call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "alambre"}); isErr {
			t.Fatal(out)
		}
		out, isErr := call(t, All(d), "confirmar", map[string]any{"codigo": code})
		if isErr {
			t.Errorf("code %#v: %s", code, out)
		}
		if len(f.Cal("joyeria").Objects) != 0 {
			t.Errorf("code %#v: not deleted", code)
		}
	}
	// Small numbers keep their leading zeros: 482 is "0482", not "4821".
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["b.ics"] = todoICS("b", "Comprar alambre", nil, false, 5, "")
	call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "alambre"})
	for _, bad := range []any{float64(482), "48211", "abcd", float64(4821.5), ""} {
		out, isErr := call(t, All(d), "confirmar", map[string]any{"codigo": bad})
		if !isErr || strings.Contains(out, "\n") {
			t.Errorf("code %#v must fail in one line: %q", bad, out)
		}
	}
	if len(f.Cal("joyeria").Objects) != 1 {
		t.Error("bad codes must not execute")
	}
}

func TestConfirmarETagChangedAborts(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["b.ics"] = todoICS("b", "Comprar alambre", nil, false, 5, "")
	if out, isErr := call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "alambre"}); isErr {
		t.Fatal(out)
	}
	changed := todoICS("b", "Comprar alambre de plata", nil, false, 5, "")
	f.Cal("joyeria").Objects["b.ics"] = changed
	out, isErr := call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr {
		t.Errorf("must abort: %s", out)
	}
	mustContain(t, out, "La tarea cambió desde que la pedí; repite la orden")
	if f.Cal("joyeria").Objects["b.ics"] != changed {
		t.Error("changed task must stay untouched")
	}

	// Same for a task change and for an event.
	c.Objects["t.ics"] = todoICS("t", "Pulir anillo", nil, false, 5, "")
	call(t, All(d), "tarea_cambiar", map[string]any{"proyecto": "joyeria", "tarea": "pulir", "prioridad": "alta"})
	edited := todoICS("t", "Pulir anillo grande", nil, false, 5, "")
	f.Cal("joyeria").Objects["t.ics"] = edited
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr || f.Cal("joyeria").Objects["t.ics"] != edited {
		t.Errorf("change must abort: %s", out)
	}
	mustContain(t, out, "cambió desde que la pedí")

	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["e.ics"] = veventICS("e", "Dentista", "20261119T120000Z", "20261119T130000Z")
	call(t, All(d), "evento_cancelar", map[string]any{"evento": "dentista"})
	moved := veventICS("e", "Dentista", "20261119T140000Z", "20261119T150000Z")
	f.Cal("personal").Objects["e.ics"] = moved
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr || f.Cal("personal").Objects["e.ics"] != moved {
		t.Errorf("event must abort: %s", out)
	}
	mustContain(t, out, "El evento cambió desde que lo pedí; repite la orden")

	// Deleted meanwhile: one-line error, nothing else happens.
	call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "plata"})
	delete(f.Cal("joyeria").Objects, "b.ics")
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr || strings.Contains(out, "\n") {
		t.Errorf("gone must fail in one line: %q", out)
	}
	mustNotContain(t, out, f.URL, "remote.php")
}

func TestConfirmarExpiredOrUnknown(t *testing.T) {
	d, f := newDeps(t)
	now := testNow
	d.Confirm = confirm.New(func() time.Time { return now }, strings.NewReader(strings.Repeat("\x12\xD5", 100)))
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["b.ics"] = todoICS("b", "Comprar alambre", nil, false, 5, "")

	out, isErr := call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr || strings.Contains(out, "\n") {
		t.Errorf("unknown must fail in one line: %q", out)
	}
	mustContain(t, out, "4821", "repite la orden")

	call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "alambre"})
	now = now.Add(confirm.TTL + time.Second)
	out, isErr = call(t, All(d), "confirmar", map[string]any{"codigo": "4821"})
	if !isErr || strings.Contains(out, "\n") {
		t.Errorf("expired must fail in one line: %q", out)
	}
	mustContain(t, out, "repite la orden")
	if len(f.Cal("joyeria").Objects) != 1 {
		t.Error("expired code must not execute")
	}
	if _, err := d.Confirm.Take("4821"); !errors.Is(err, confirm.ErrUnknown) {
		t.Errorf("expired entry must be gone, got %v", err)
	}
}
