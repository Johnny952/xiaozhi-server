package tools

import (
	"strings"
	"testing"

	"nextcloud-mcp/bitacora"
)

const casaToday = "/Bitacora/casa/2026/2026-11-18.md"

func TestAnotarCreatesFoldersHeaderEntry(t *testing.T) {
	d, f := newDeps(t)
	out, isErr := call(t, All(d), "bitacora_anotar", map[string]any{
		"contexto": "Casa", "titulo": "Riego", "texto": "Regué el jardín", "hora": "8:30",
	})
	if isErr {
		t.Fatalf("error: %s", out)
	}
	for _, dir := range []string{"/Bitacora", "/Bitacora/casa", "/Bitacora/casa/2026"} {
		if !f.Dirs[dir] {
			t.Errorf("folder %s not created", dir)
		}
	}
	want := bitacora.Header("Casa", testNow) + bitacora.EntryText("08:30", "Riego", "Regué el jardín")
	if got := string(f.Files[casaToday]); got != want {
		t.Errorf("file:\n%q\nwant:\n%q", got, want)
	}
	mustContain(t, out, "Casa", "08:30", "Riego")

	// Without hora, the current time is used.
	call(t, All(d), "bitacora_anotar", map[string]any{"contexto": "Casa", "titulo": "Luz", "texto": "x"})
	mustContain(t, string(f.Files[casaToday]), "## 10:00 — Luz")
}

func TestAnotarAppendsKeepsExisting(t *testing.T) {
	d, f := newDeps(t)
	orig := bitacora.Header("Casa", testNow) + bitacora.EntryText("07:00", "Café", "Hice café")
	f.Files[casaToday] = []byte(orig)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"], f.Dirs["/Bitacora/casa/2026"] = true, true, true
	if out, isErr := call(t, All(d), "bitacora_anotar", map[string]any{
		"contexto": "Casa", "titulo": "Riego", "texto": "Regué",
	}); isErr {
		t.Fatal(out)
	}
	got := string(f.Files[casaToday])
	if !strings.HasPrefix(got, orig) {
		t.Errorf("existing content lost:\n%s", got)
	}
	day := bitacora.Parse(got)
	if len(day.Entries) != 2 || day.Entries[1].Title != "Riego" {
		t.Errorf("entries: %+v", day.Entries)
	}
}

func TestAnotarSurvivesConcurrentWrite(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"], f.Dirs["/Bitacora/casa/2026"] = true, true, true
	f.Files[casaToday] = []byte(bitacora.Header("Casa", testNow) + bitacora.EntryText("07:00", "Café", "uno"))
	other := bitacora.Header("Casa", testNow) + bitacora.EntryText("07:00", "Café", "uno") +
		"\n" + bitacora.EntryText("09:00", "Otro", "escrito por otro")
	f.MutateBeforeNextPut(casaToday, []byte(other))
	if out, isErr := call(t, All(d), "bitacora_anotar", map[string]any{
		"contexto": "Casa", "titulo": "Mío", "texto": "dos",
	}); isErr {
		t.Fatal(out)
	}
	got := string(f.Files[casaToday])
	mustContain(t, got, "## 09:00 — Otro", "## 10:00 — Mío", "## 07:00 — Café")
}

func TestCorregirAppendsToOriginalDay(t *testing.T) {
	d, f := newDeps(t)
	p := "/Bitacora/casa/2026/2026-11-16.md"
	day := testNow.AddDate(0, 0, -2)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"], f.Dirs["/Bitacora/casa/2026"] = true, true, true
	f.Files[p] = []byte(bitacora.Header("Casa", day) + bitacora.EntryText("08:00", "Riego", "malo") +
		"\n" + bitacora.EntryText("09:00", "Compras", "pan"))
	out, isErr := call(t, All(d), "bitacora_corregir", map[string]any{
		"contexto": "Casa", "fecha": "2026-11-16", "entrada": "riego", "texto": "Regué solo el patio",
	})
	if isErr {
		t.Fatal(out)
	}
	if _, ok := f.Files[casaToday]; ok {
		t.Error("correction must not go to today's file")
	}
	parsed := bitacora.Parse(string(f.Files[p]))
	if len(parsed.Entries) != 2 || parsed.Entries[0].Text != "Regué solo el patio" || parsed.Entries[0].CorrectedAt != "10:00" {
		t.Errorf("entries: %+v", parsed.Entries)
	}

	// time + title reference
	out, isErr = call(t, All(d), "bitacora_corregir", map[string]any{
		"contexto": "Casa", "fecha": "2026-11-16", "entrada": "9:00 — Compras", "texto": "pan y leche",
	})
	if isErr {
		t.Fatal(out)
	}
	parsed = bitacora.Parse(string(f.Files[p]))
	if parsed.Entries[1].Text != "pan y leche" {
		t.Errorf("entries: %+v", parsed.Entries)
	}
}

func TestCorregirNotFoundWritesNothing(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"], f.Dirs["/Bitacora/casa/2026"] = true, true, true
	orig := bitacora.Header("Casa", testNow) + bitacora.EntryText("08:00", "Riego jardín", "a") +
		"\n" + bitacora.EntryText("09:00", "Riego patio", "b")
	f.Files[casaToday] = []byte(orig)
	before := f.PutCount

	out, isErr := call(t, All(d), "bitacora_corregir", map[string]any{
		"contexto": "Casa", "fecha": "2026-11-18", "entrada": "compras", "texto": "x",
	})
	if !isErr {
		t.Errorf("want error, got %s", out)
	}
	mustContain(t, out, "No encontré")

	out, isErr = call(t, All(d), "bitacora_corregir", map[string]any{
		"contexto": "Casa", "fecha": "2026-11-18", "entrada": "riego", "texto": "x",
	})
	if !isErr {
		t.Errorf("ambiguous must not write: %s", out)
	}
	mustContain(t, out, "08:00 — Riego jardín", "09:00 — Riego patio")

	out, isErr = call(t, All(d), "bitacora_corregir", map[string]any{
		"contexto": "Casa", "fecha": "2026-11-10", "entrada": "riego", "texto": "x",
	})
	if !isErr {
		t.Errorf("missing day: %s", out)
	}
	if f.PutCount != before || string(f.Files[casaToday]) != orig {
		t.Error("nothing must be written")
	}
}

func TestLeerOneDayFull(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"] = true, true
	f.Files[casaToday] = []byte(bitacora.Header("Casa", testNow) + bitacora.EntryText("08:00", "Riego", "Regué todo el jardín con calma"))
	out, isErr := call(t, All(d), "bitacora_leer", map[string]any{"contexto": "Casa"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "08:00", "Riego", "Regué todo el jardín con calma", "hoy")
	mustNotContain(t, out, "##", "pide el detalle")
}

func TestLeerRangeSummaryGroupedPideDetalle(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"] = true, true
	f.Files["/Bitacora/casa/2026/2026-11-17.md"] = []byte(bitacora.Header("Casa", testNow.AddDate(0, 0, -1)) +
		bitacora.EntryText("09:00", "Ayer B", "cuerpo largo que no se lee") + "\n" + bitacora.EntryText("07:00", "Ayer A", "x"))
	f.Files[casaToday] = []byte(bitacora.Header("Casa", testNow) + bitacora.EntryText("08:00", "Hoy", "y"))
	out, isErr := call(t, All(d), "bitacora_leer", map[string]any{
		"contexto": "Casa", "desde": "2026-11-16", "hasta": "2026-11-18",
	})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "ayer · 07:00 — Ayer A [Casa]", "ayer · 09:00 — Ayer B [Casa]", "hoy · 08:00 — Hoy [Casa]", "pide el detalle de un día")
	mustNotContain(t, out, "cuerpo largo")
	if strings.Index(out, "Ayer A") > strings.Index(out, "Ayer B") || strings.Index(out, "Ayer B") > strings.Index(out, "Hoy [") {
		t.Errorf("not chronological:\n%s", out)
	}
}

func TestLeerNoContextListsAllContexts(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"], f.Dirs["/Bitacora/trabajo"] = true, true, true
	f.Files[casaToday] = []byte(bitacora.Header("Casa", testNow) + bitacora.EntryText("08:00", "Riego", "a"))
	f.Files["/Bitacora/trabajo/2026/2026-11-18.md"] = []byte(bitacora.Header("Trabajo", testNow) + bitacora.EntryText("09:00", "Reunión", "b"))
	out, isErr := call(t, All(d), "bitacora_leer", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Riego", "Reunión", "Casa", "Trabajo")
}

func TestLeerMax31Days(t *testing.T) {
	d, _ := newDeps(t)
	out, isErr := call(t, All(d), "bitacora_leer", map[string]any{
		"contexto": "Casa", "desde": "2026-09-01", "hasta": "2026-11-18",
	})
	if !isErr {
		t.Errorf("want error: %s", out)
	}
	mustContain(t, out, "31")
}

func TestLeerTextoDefaultsLast30Days(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"] = true, true
	f.Files["/Bitacora/casa/2026/2026-10-25.md"] = []byte(bitacora.Header("Casa", testNow.AddDate(0, 0, -24)) +
		bitacora.EntryText("08:00", "Filtro", "cambié el filtro de la piscina") + "\n" + bitacora.EntryText("09:00", "Otra", "nada"))
	f.Files["/Bitacora/casa/2026/2026-10-01.md"] = []byte(bitacora.Header("Casa", testNow.AddDate(0, 0, -48)) +
		bitacora.EntryText("08:00", "Filtro viejo", "piscina antigua"))
	out, isErr := call(t, All(d), "bitacora_leer", map[string]any{"contexto": "Casa", "texto": "piscina"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Filtro")
	mustNotContain(t, out, "Otra", "Filtro viejo")
}

func TestLeerShowsCorregida(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"] = true, true
	orig := bitacora.Entry{Time: "08:00", Title: "Riego"}
	f.Files[casaToday] = []byte(bitacora.Header("Casa", testNow) + bitacora.EntryText("08:00", "Riego", "malo") +
		"\n" + bitacora.CorrectionText("09:30", orig, "bueno"))
	out, _ := call(t, All(d), "bitacora_leer", map[string]any{"contexto": "Casa"})
	mustContain(t, out, "(corregida 09:30)", "bueno")
	mustNotContain(t, out, "malo")
}

func TestLeerUnstructuredBlock(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Bitacora"], f.Dirs["/Bitacora/casa"] = true, true
	f.Files[casaToday] = []byte("notas sueltas escritas a mano\nsin formato")
	out, isErr := call(t, All(d), "bitacora_leer", map[string]any{"contexto": "Casa"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "notas sueltas escritas a mano\nsin formato")
}
