package tools

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"nextcloud-mcp/bitacora"
	"nextcloud-mcp/confirm"
	"nextcloud-mcp/ical"
)

func TestTareaCrearUnknownProjectLists(t *testing.T) {
	d, f := newDeps(t)
	f.AddCalendar("mesa-libre", "Mesa Libre", "VTODO")
	f.AddCalendar("personal", "Personal", "VEVENT", "VTODO")
	f.AddCalendar("cumples", "Cumpleaños", "VEVENT")
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{"proyecto": "Joyería", "titulo": "Comprar alambre"})
	if !isErr {
		t.Errorf("want error: %s", out)
	}
	mustContain(t, out, "No existe el proyecto Joyería", "Mesa Libre", "Personal")
	mustNotContain(t, out, "Cumpleaños")
	if f.Cal("joyeria") != nil {
		t.Error("must not create the project without crear_proyecto")
	}
}

func TestTareaCrearCrearProyecto(t *testing.T) {
	d, f := newDeps(t)
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{
		"proyecto": "Joyería", "titulo": "Comprar alambre", "crear_proyecto": true,
	})
	if isErr {
		t.Fatal(out)
	}
	c := f.Cal("joyeria")
	if c == nil {
		t.Fatal("calendar joyeria not created")
	}
	if c.Name != "Joyería" || len(c.Comps) != 1 || c.Comps[0] != "VTODO" {
		t.Errorf("calendar: %+v", c)
	}
	if len(c.Objects) != 1 {
		t.Fatalf("objects: %v", c.Objects)
	}
	for _, data := range c.Objects {
		mustContain(t, data, "SUMMARY:Comprar alambre", "PRIORITY:5")
	}
	mustContain(t, out, "Comprar alambre", "Joyería")
}

func TestTareaCrearDuplicateBlocksUnlessCompleted(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["a.ics"] = todoICS("a", "Comprar alambre", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{"proyecto": "joyeria", "titulo": "comprar Alambre"})
	if !isErr {
		t.Errorf("duplicate must block: %s", out)
	}
	mustContain(t, out, "Comprar alambre", "forzar")
	if len(f.Cal("joyeria").Objects) != 1 {
		t.Error("nothing must be created")
	}

	c2 := f.AddCalendar("casa", "Casa", "VTODO")
	c2.Objects["b.ics"] = todoICS("b", "Regar", nil, false, 5, "COMPLETED")
	out, isErr = call(t, All(d), "tarea_crear", map[string]any{"proyecto": "Casa", "titulo": "Regar"})
	if isErr {
		t.Fatalf("completed duplicate must not block: %s", out)
	}
	if len(f.Cal("casa").Objects) != 2 {
		t.Error("task not created")
	}
}

func TestTareaCrearForzar(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["a.ics"] = todoICS("a", "Comprar alambre", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{
		"proyecto": "joyeria", "titulo": "Comprar alambre", "forzar": true,
	})
	if isErr {
		t.Fatal(out)
	}
	if len(f.Cal("joyeria").Objects) != 2 {
		t.Error("forzar must create")
	}
}

func TestTareaCrearDentroDe(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["padre.ics"] = todoICS("padre-uid", "Feria de diciembre", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{
		"proyecto": "joyeria", "titulo": "Llevar mantel", "dentro_de": "feria",
	})
	if isErr {
		t.Fatal(out)
	}
	child := onlyObject(t, f.Cal("joyeria").Objects, "padre.ics")
	mustContain(t, child, "RELATED-TO;RELTYPE=PARENT:padre-uid", "SUMMARY:Llevar mantel")

	// Ambiguous parent: nothing is created.
	c.Objects["p2.ics"] = todoICS("p2", "Feria de verano", nil, false, 5, "")
	before := len(f.Cal("joyeria").Objects)
	out, isErr = call(t, All(d), "tarea_crear", map[string]any{
		"proyecto": "joyeria", "titulo": "Llevar caja", "dentro_de": "feria",
	})
	if !isErr {
		t.Errorf("ambiguous parent must fail: %s", out)
	}
	mustContain(t, out, "Feria de diciembre", "Feria de verano")
	if len(f.Cal("joyeria").Objects) != before {
		t.Error("nothing must be created with an ambiguous parent")
	}

	// Missing parent: nothing is created.
	out, isErr = call(t, All(d), "tarea_crear", map[string]any{
		"proyecto": "joyeria", "titulo": "Llevar caja", "dentro_de": "congreso",
	})
	if !isErr {
		t.Errorf("missing parent must fail: %s", out)
	}
	mustContain(t, out, "No encontré")
	if len(f.Cal("joyeria").Objects) != before {
		t.Error("nothing must be created with a missing parent")
	}
}

func TestTareaCrearPriorityWords(t *testing.T) {
	cases := map[string]string{"alta": "PRIORITY:1", "media": "PRIORITY:5", "baja": "PRIORITY:9", "3": "PRIORITY:3"}
	for word, want := range cases {
		d, f := newDeps(t)
		f.AddCalendar("casa", "Casa", "VTODO")
		out, isErr := call(t, All(d), "tarea_crear", map[string]any{
			"proyecto": "casa", "titulo": "Tarea " + word, "prioridad": word, "vence": "2026-11-20",
		})
		if isErr {
			t.Fatalf("%s: %s", word, out)
		}
		data := onlyObject(t, f.Cal("casa").Objects)
		mustContain(t, data, want, "DUE;VALUE=DATE:20261120")
	}
	d, f := newDeps(t)
	f.AddCalendar("casa", "Casa", "VTODO")
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{"proyecto": "casa", "titulo": "X", "prioridad": "urgentísima"})
	if !isErr {
		t.Errorf("bad priority must fail: %s", out)
	}
	if len(f.Cal("casa").Objects) != 0 {
		t.Error("nothing must be created")
	}
}

func TestTareaCompletarSetsFieldsAndLogsBitacora(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("mesa-libre", "Mesa Libre", "VTODO")
	c.Objects["t1.ics"] = todoICS("t1", "Pintar patas", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_completar", map[string]any{"proyecto": "Mesa Libre", "tarea": "pintar"})
	if isErr {
		t.Fatal(out)
	}
	data := f.Cal("mesa-libre").Objects["t1.ics"]
	mustContain(t, data, "STATUS:COMPLETED", "PERCENT-COMPLETE:100", "COMPLETED:"+ical.ToUTC(testNow))
	if got := ical.ParseVTodo(data, testLoc); got.Status != "COMPLETED" {
		t.Errorf("status: %q", got.Status)
	}
	p := "/Bitacora/mesa-libre/2026/2026-11-18.md"
	want := bitacora.Header("mesa-libre", testNow) + bitacora.EntryText("10:00", "Completada: Pintar patas", "Mesa Libre")
	if got := string(f.Files[p]); got != want {
		t.Errorf("bitácora:\n%q\nwant:\n%q", got, want)
	}
	mustContain(t, out, "Pintar patas")
}

func TestTareaCompletarBitacoraFailsKeepsCompletion(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("mesa-libre", "Mesa Libre", "VTODO")
	c.Objects["t1.ics"] = todoICS("t1", "Pintar patas", nil, false, 5, "")
	f.Handle("/remote.php/dav/files/user/Bitacora", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	out, isErr := call(t, All(d), "tarea_completar", map[string]any{"proyecto": "mesa-libre", "tarea": "Pintar patas"})
	if isErr {
		t.Errorf("completion succeeded, must not be an error: %s", out)
	}
	if !strings.HasPrefix(out, "Tarea completada; no pude anotarla en la bitácora: ") {
		t.Errorf("text: %q", out)
	}
	mustContain(t, f.Cal("mesa-libre").Objects["t1.ics"], "STATUS:COMPLETED")
}

func TestTareaAnotarAppendsDatedDescription(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("casa", "Casa", "VTODO")
	c.Objects["t1.ics"] = todoICS("t1", "Arreglar llave", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_anotar", map[string]any{"proyecto": "casa", "tarea": "llave", "texto": "Compré el repuesto"})
	if isErr {
		t.Fatal(out)
	}
	got := ical.ParseVTodo(f.Cal("casa").Objects["t1.ics"], testLoc)
	if got.Description != "[18/11/2026 10:00] Compré el repuesto" {
		t.Errorf("description: %q", got.Description)
	}
	out, isErr = call(t, All(d), "tarea_anotar", map[string]any{"proyecto": "casa", "tarea": "llave", "texto": "Listo"})
	if isErr {
		t.Fatal(out)
	}
	got = ical.ParseVTodo(f.Cal("casa").Objects["t1.ics"], testLoc)
	if got.Description != "[18/11/2026 10:00] Compré el repuesto\n[18/11/2026 10:00] Listo" {
		t.Errorf("description: %q", got.Description)
	}
	if got.Summary != "Arreglar llave" {
		t.Errorf("summary changed: %q", got.Summary)
	}
}

func TestTareaCambiarPendingShowsBeforeAfter(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("casa", "Casa", "VTODO")
	orig := todoICS("t1", "Arreglar llave", day(2026, 11, 20), true, 5, "")
	c.Objects["t1.ics"] = orig
	out, isErr := call(t, All(d), "tarea_cambiar", map[string]any{
		"proyecto": "casa", "tarea": "llave", "nuevo_titulo": "Cambiar llave", "vence": "2026-11-25", "prioridad": "alta",
	})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "PENDIENTE", "codigo=4821", "Arreglar llave → Cambiar llave",
		"20/11/2026 → 25/11/2026", "media → alta")
	if f.Cal("casa").Objects["t1.ics"] != orig {
		t.Error("must not change before confirmar")
	}
	p, err := d.Confirm.Take("4821")
	if err != nil {
		t.Fatal(err)
	}
	if p.Action != "tarea_cambiar" || !strings.HasSuffix(p.Href, "/casa/t1.ics") || p.ETag == "" ||
		p.Params["nuevo_titulo"] != "Cambiar llave" || p.Params["vence"] != "2026-11-25" || p.Params["prioridad"] != "alta" {
		t.Errorf("pending: %+v", p)
	}

	// No change requested: error, no pending.
	out, isErr = call(t, All(d), "tarea_cambiar", map[string]any{"proyecto": "casa", "tarea": "llave"})
	if !isErr {
		t.Errorf("want error: %s", out)
	}
	if _, err := d.Confirm.Take("4821"); !errors.Is(err, confirm.ErrUnknown) {
		t.Errorf("store must be empty, got %v", err)
	}
}

func TestTareaBorrarAmbiguousNoPending(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("joyeria", "Joyería", "VTODO")
	c.Objects["a.ics"] = todoICS("a", "Comprar alambre fino", day(2026, 11, 20), true, 5, "")
	c.Objects["b.ics"] = todoICS("b", "Comprar alambre grueso", nil, false, 5, "")
	out, isErr := call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "alambre"})
	if !isErr {
		t.Errorf("ambiguous must fail: %s", out)
	}
	mustContain(t, out, "Comprar alambre fino", "20/11/2026", "Comprar alambre grueso")
	mustNotContain(t, out, "PENDIENTE")
	out, isErr = call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "perlas"})
	if !isErr {
		t.Errorf("missing must fail: %s", out)
	}
	mustContain(t, out, "No encontré")
	if _, err := d.Confirm.Take("4821"); !errors.Is(err, confirm.ErrUnknown) {
		t.Errorf("store must be empty, got %v", err)
	}
	if len(f.Cal("joyeria").Objects) != 2 {
		t.Error("nothing deleted")
	}

	// A unique match issues the confirmation and still deletes nothing.
	out, isErr = call(t, All(d), "tarea_borrar", map[string]any{"proyecto": "joyeria", "tarea": "grueso"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "PENDIENTE", "Comprar alambre grueso", "Joyería")
	p, err := d.Confirm.Take("4821")
	if err != nil || p.Action != "tarea_borrar" || !strings.HasSuffix(p.Href, "/joyeria/b.ics") || p.ETag == "" {
		t.Errorf("pending: %+v %v", p, err)
	}
	if len(f.Cal("joyeria").Objects) != 2 {
		t.Error("nothing deleted before confirmar")
	}
}

func TestTareasPendientesFiltersCompleted(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("casa", "Casa", "VTODO")
	c.Objects["a.ics"] = todoICS("a", "Regar", day(2026, 11, 19), true, 1, "")
	c.Objects["b.ics"] = todoICS("b", "Barrer", nil, false, 5, "COMPLETED")
	j := f.AddCalendar("joyeria", "Joyería", "VTODO")
	j.Objects["c.ics"] = todoICS("c", "Comprar alambre", nil, false, 5, "")
	out, isErr := call(t, All(d), "tareas_pendientes", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Regar", "Comprar alambre", "Casa", "Joyería")
	mustNotContain(t, out, "Barrer")

	out, _ = call(t, All(d), "tareas_pendientes", map[string]any{"proyecto": "casa"})
	mustContain(t, out, "Regar")
	mustNotContain(t, out, "Comprar alambre")

	out, _ = call(t, All(d), "tareas_pendientes", map[string]any{"texto": "alambre"})
	mustContain(t, out, "Comprar alambre")
	mustNotContain(t, out, "Regar")

	out, isErr = call(t, All(d), "tareas_pendientes", map[string]any{"proyecto": "jardín"})
	if !isErr {
		t.Errorf("unknown project must fail: %s", out)
	}
	mustContain(t, out, "No existe el proyecto", "Casa", "Joyería")
}

func TestTareasPendientesSinProyectoParallelMax4(t *testing.T) {
	d, f := newDeps(t)
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"} {
		f.AddCalendar(id, strings.ToUpper(id), "VTODO")
	}
	var mu sync.Mutex
	inFlight, maxSeen, total := 0, 0, 0
	f.Handle("/remote.php/dav/calendars/user/p", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		total++
		if inFlight > maxSeen {
			maxSeen = inFlight
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(`<d:multistatus xmlns:d="DAV:"/>`))
	})
	out, isErr := call(t, All(d), "tareas_pendientes", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mu.Lock()
	defer mu.Unlock()
	if total != 8 {
		t.Errorf("want 8 calendar queries, got %d", total)
	}
	if maxSeen > maxParallel || maxSeen < 2 {
		t.Errorf("max concurrency %d, want 2..%d", maxSeen, maxParallel)
	}
}

func TestEmptyRefMutatesNothing(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("casa", "Casa", "VTODO")
	c.Objects["a.ics"] = todoICS("a", "Regar", nil, false, 5, "")
	p := f.AddCalendar("personal", "Personal", "VEVENT")
	p.Objects["e.ics"] = veventICS("e", "Dentista", "20261119T120000Z", "20261119T130000Z")
	before := f.Cal("casa").Objects["a.ics"]
	for _, ref := range []string{"¿?", "", "  ...  "} {
		for _, tc := range []struct {
			tool string
			args map[string]any
		}{
			{"tarea_completar", map[string]any{"proyecto": "Casa", "tarea": ref}},
			{"tarea_anotar", map[string]any{"proyecto": "Casa", "tarea": ref, "texto": "x"}},
			{"evento_cancelar", map[string]any{"evento": ref}},
		} {
			out, isErr := call(t, All(d), tc.tool, tc.args)
			if ref == "" {
				continue // missing-arg path, not the normalization path
			}
			if !isErr || !strings.Contains(out, "Dime cuál") {
				t.Errorf("%s %q: want 'Dime cuál' error, got %v %q", tc.tool, ref, isErr, out)
			}
		}
	}
	if f.Cal("casa").Objects["a.ics"] != before {
		t.Error("task was mutated")
	}
	if _, err := d.Confirm.Take("4821"); err == nil {
		t.Error("no pending confirmation expected")
	}
}

func TestTareaCrearProyectoCollisionReusesCalendar(t *testing.T) {
	d, f := newDeps(t)
	f.AddCalendar("joyeria", "Taller de joyas", "VTODO") // slug taken, name differs
	out, isErr := call(t, All(d), "tarea_crear", map[string]any{
		"proyecto": "Joyería", "titulo": "Comprar alambre", "crear_proyecto": true,
	})
	if isErr {
		t.Fatal(out)
	}
	if n := len(f.Cal("joyeria").Objects); n != 1 {
		t.Fatalf("objects in existing calendar: %d (%s)", n, out)
	}
}

func TestListsCappedAtTen(t *testing.T) {
	d, f := newDeps(t)
	c := f.AddCalendar("casa", "Casa", "VTODO")
	for i := 0; i < 13; i++ {
		id := string(rune('a' + i))
		c.Objects[id+".ics"] = todoICS(id, "Tarea "+id, nil, false, 5, "")
	}
	out, isErr := call(t, All(d), "tareas_pendientes", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "y 3 más")
}

func TestNewUIDFormat(t *testing.T) {
	u := newUID()
	n, ok := strings.CutSuffix(u, "@xiaozhi-nextcloud")
	if !ok || n == "" || strings.Trim(n, "0123456789") != "" {
		t.Errorf("uid %q", u)
	}
}
