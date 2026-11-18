package tools

import (
	"net/http"
	"strings"
	"testing"
)

const stamp = "\n\n---\n**18/11/2026 10:00**\n\n"

func TestNotaGuardarAppendsToExactTitle(t *testing.T) {
	d, f := newDeps(t)
	f.AddNote("Compras del mes", "otra")
	n := f.AddNote("Compras", "pan")
	out, isErr := call(t, All(d), "nota_guardar", map[string]any{"titulo": "compras", "texto": "leche"})
	if isErr {
		t.Fatal(out)
	}
	if out != "Agregado a la nota existente 'Compras'" {
		t.Errorf("text: %q", out)
	}
	if got := f.Note(n.ID).Content; got != "pan"+stamp+"leche" {
		t.Errorf("content: %q", got)
	}
	if got := f.Note(1).Content; got != "otra" {
		t.Errorf("other note touched: %q", got)
	}
}

func TestNotaGuardarCreatesNew(t *testing.T) {
	d, f := newDeps(t)
	f.AddNote("Compras del mes", "otra")
	out, isErr := call(t, All(d), "nota_guardar", map[string]any{"titulo": "Compras", "texto": "leche"})
	if isErr {
		t.Fatal(out)
	}
	n := f.Note(2)
	if n == nil || n.Title != "Compras" || n.Content != "leche" {
		t.Fatalf("note: %+v", n)
	}
	if f.Note(1).Content != "otra" {
		t.Error("existing note changed")
	}
	mustContain(t, out, "Compras")
}

func TestNotaGuardarConflictRetries(t *testing.T) {
	d, f := newDeps(t)
	n := f.AddNote("Ideas", "uno")
	f.MutateNoteBeforeNextPut(n.ID, "uno\n\notro escribió")
	out, isErr := call(t, All(d), "nota_guardar", map[string]any{"titulo": "Ideas", "texto": "dos"})
	if isErr {
		t.Fatal(out)
	}
	if got := f.Note(n.ID).Content; got != "uno\n\notro escribió"+stamp+"dos" {
		t.Errorf("content: %q", got)
	}
}

func TestNotasBuscarOneReturnsContent(t *testing.T) {
	d, f := newDeps(t)
	f.AddNote("Receta pan", "harina, agua y sal")
	f.AddNote("Compras", "leche")
	out, isErr := call(t, All(d), "notas_buscar", map[string]any{"texto": "harina"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Receta pan", "harina, agua y sal")
	mustNotContain(t, out, "leche")

	f.AddNote("Largo", strings.Repeat("palabra ", 600))
	out, _ = call(t, All(d), "notas_buscar", map[string]any{"texto": "largo"})
	if len(out) > clipMax+200 {
		t.Errorf("not clipped: %d", len(out))
	}
	mustContain(t, out, "recortado")
}

func TestNotasBuscarManyListsTitles(t *testing.T) {
	d, f := newDeps(t)
	f.AddNote("Receta pan", "harina y agua")
	f.AddNote("Receta pizza", "harina, tomate")
	f.AddNote("Compras", "leche")
	out, isErr := call(t, All(d), "notas_buscar", map[string]any{"texto": "harina"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "Receta pan", "Receta pizza")
	mustNotContain(t, out, "tomate", "Compras")

	out, isErr = call(t, All(d), "notas_buscar", map[string]any{"texto": "zanahoria"})
	if isErr {
		t.Errorf("no results is not an error: %s", out)
	}
	mustContain(t, out, "No encontré")
}

func TestEstadoAnotarNewFileHeaderAndSection(t *testing.T) {
	d, f := newDeps(t)
	out, isErr := call(t, All(d), "estado_proyecto_anotar", map[string]any{"proyecto": "Mesa Libre", "texto": "Patas pintadas"})
	if isErr {
		t.Fatal(out)
	}
	p := "/Proyectos/Mesa Libre/estado.md"
	if !f.Dirs["/Proyectos"] || !f.Dirs["/Proyectos/Mesa Libre"] {
		t.Errorf("folders: %v", f.Dirs)
	}
	want := "# Estado: Mesa Libre\n\n## 18/11/2026 — Actualización\n\nPatas pintadas\n"
	if got := string(f.Files[p]); got != want {
		t.Errorf("file:\n%q\nwant:\n%q", got, want)
	}

	// Existing folder found case/accent-insensitively; content kept; section used.
	out, isErr = call(t, All(d), "estado_proyecto_anotar", map[string]any{"proyecto": "mesa libre", "texto": "Barniz", "seccion": "Acabado"})
	if isErr {
		t.Fatal(out)
	}
	got := string(f.Files[p])
	if !strings.HasPrefix(got, strings.TrimRight(want, "\n")) {
		t.Errorf("lost content:\n%s", got)
	}
	mustContain(t, got, "\n\n## 18/11/2026 — Acabado\n\nBarniz\n")
	if _, ok := f.Files["/Proyectos/mesa libre/estado.md"]; ok {
		t.Error("created a second folder")
	}
}

func TestEstadoLeerClipped(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Proyectos"], f.Dirs["/Proyectos/Casa"] = true, true
	f.Files["/Proyectos/Casa/estado.md"] = []byte("# Estado: Casa\n\n" + strings.Repeat("texto ", 500))
	out, isErr := call(t, All(d), "estado_proyecto_leer", map[string]any{"proyecto": "casa"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "# Estado: Casa", "recortado")
	if len(out) > clipMax+200 {
		t.Errorf("not clipped: %d", len(out))
	}

	out, isErr = call(t, All(d), "estado_proyecto_leer", map[string]any{"proyecto": "Jardín"})
	if !isErr {
		t.Errorf("missing project must fail: %s", out)
	}
}

func TestNoCredentialURLsInOutput(t *testing.T) {
	d, f := newDeps(t)
	f.AddNote("Compras", "pan")
	f.Dirs["/Proyectos"], f.Dirs["/Proyectos/Casa"] = true, true
	f.Files["/Proyectos/Casa/estado.md"] = []byte("# Estado: Casa")
	f.Dirs["/Documentos"] = true
	f.Files["/Documentos/a.txt"] = []byte("hola")
	type c struct {
		name string
		args map[string]any
	}
	cases := []c{
		{"nota_guardar", map[string]any{"titulo": "Compras", "texto": "x"}},
		{"nota_guardar", map[string]any{"titulo": "Nueva", "texto": "x"}},
		{"notas_buscar", map[string]any{"texto": "pan"}},
		{"notas_buscar", map[string]any{"texto": "nada"}},
		{"estado_proyecto_anotar", map[string]any{"proyecto": "Casa", "texto": "x"}},
		{"estado_proyecto_leer", map[string]any{"proyecto": "Casa"}},
		{"estado_proyecto_leer", map[string]any{"proyecto": "Otro"}},
		{"archivo_leer", map[string]any{"ruta_o_nombre": "/Documentos/a.txt"}},
		{"archivo_leer", map[string]any{"ruta_o_nombre": "/Documentos/no.txt"}},
		{"archivo_leer", map[string]any{"ruta_o_nombre": "a.txt"}},
		{"archivo_leer", map[string]any{"ruta_o_nombre": "zzz"}},
	}
	check := func(out string) {
		t.Helper()
		mustNotContain(t, out, f.URL, strings.TrimPrefix(f.URL, "http://"), f.Pass, "remote.php")
	}
	for _, tc := range cases {
		out, _ := call(t, All(d), tc.name, tc.args)
		check(out)
	}
	// Server failures go through the error path too.
	fail500 := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	f.Handle("/index.php", fail500)
	f.Handle("/remote.php", fail500)
	for _, tc := range cases {
		out, isErr := call(t, All(d), tc.name, tc.args)
		if !isErr {
			t.Errorf("%s with server down must fail: %s", tc.name, out)
		}
		check(out)
	}
}
