package tools

import (
	"strings"
	"testing"
)

func TestArchivoLeerByPath(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Documentos"] = true
	f.Files["/Documentos/lista.md"] = []byte("# Lista\n\n- pan")
	for _, p := range []string{"/Documentos/lista.md", "Documentos/lista.md"} {
		out, isErr := call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": p})
		if isErr {
			t.Fatal(out)
		}
		mustContain(t, out, "# Lista\n\n- pan")
	}
	f.Files["/Documentos/largo.txt"] = []byte(strings.Repeat("abc ", 1000))
	out, _ := call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "/Documentos/largo.txt"})
	mustContain(t, out, "recortado")
	if len(out) > clipMax+200 {
		t.Errorf("not clipped: %d", len(out))
	}

	out, isErr := call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "/Documentos/no.md"})
	if !isErr {
		t.Errorf("missing must fail: %s", out)
	}
	mustContain(t, out, "No existe")
}

func TestArchivoLeerByNameSearch(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Documentos"], f.Dirs["/Contactos"] = true, true
	f.Files["/Documentos/receta.txt"] = []byte("harina")
	out, isErr := call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "receta"})
	if isErr {
		t.Fatal(out)
	}
	mustContain(t, out, "harina", "/Documentos/receta.txt")

	f.Files["/Contactos/telefonos.md"] = []byte("a")
	f.Files["/Documentos/telefonos viejos.md"] = []byte("b")
	out, _ = call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "telefonos"})
	mustContain(t, out, "/Contactos/telefonos.md", "/Documentos/telefonos viejos.md")

	out, isErr = call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "inexistente"})
	if !isErr {
		t.Errorf("no match must fail: %s", out)
	}
	mustContain(t, out, "No encontré")
}

func TestArchivoLeerBinaryRefused(t *testing.T) {
	d, f := newDeps(t)
	f.Dirs["/Documentos"] = true
	f.Files["/Documentos/factura.pdf"] = []byte("%PDF-1.4 \x00\x01")
	out, isErr := call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "/Documentos/factura.pdf"})
	if !isErr {
		t.Errorf("binary must fail: %s", out)
	}
	mustContain(t, out, "No puedo leer en voz alta un archivo PDF")
	mustNotContain(t, out, "%PDF")

	out, _ = call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "factura"})
	mustContain(t, out, "No puedo leer en voz alta un archivo PDF")

	// No extension, binary content.
	f.Files["/Documentos/blob"] = []byte{0xff, 0x00, 0xfe}
	out, isErr = call(t, All(d), "archivo_leer", map[string]any{"ruta_o_nombre": "/Documentos/blob"})
	if !isErr {
		t.Errorf("binary content must fail: %s", out)
	}
	mustContain(t, out, "No puedo leer en voz alta")
}
