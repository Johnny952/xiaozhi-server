package nc_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/nc"
	"nextcloud-mcp/nctest"
)

func setup(t *testing.T) (*nc.Client, *nctest.Fake) {
	t.Helper()
	f := nctest.New(t, "ana")
	c := nc.New(f.URL, "ana", f.Pass)
	c.LockWait = time.Millisecond
	return c, f
}

func TestGetPutETag(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()

	etag, err := c.Put(ctx, "/Notas/a b.md", []byte("uno"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.LastPutHeader.Get("If-None-Match"); got != "*" {
		t.Fatalf("If-None-Match = %q, want *", got)
	}
	if etag == "" {
		t.Fatal("empty etag")
	}
	body, gotTag, err := c.Get(ctx, "/Notas/a b.md")
	if err != nil || string(body) != "uno" || gotTag != etag {
		t.Fatalf("Get = %q %q %v (etag %q)", body, gotTag, err, etag)
	}
	// Put con If-Match correcto
	etag2, err := c.Put(ctx, "/Notas/a b.md", []byte("dos"), etag)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.LastPutHeader.Get("If-Match"); got != etag {
		t.Fatalf("If-Match = %q, want %q", got, etag)
	}
	// ETag viejo -> 412 -> ErrConflict, un solo intento
	f.PutCount = 0
	_, err = c.Put(ctx, "/Notas/a b.md", []byte("tres"), etag)
	if !errors.Is(err, nc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if f.PutCount != 1 {
		t.Fatalf("PutCount = %d, want 1 (sin reintentos)", f.PutCount)
	}
	if string(f.Files["/Notas/a b.md"]) != "dos" {
		t.Fatal("file changed on failed put")
	}
	// Nuevo sobre existente -> 412
	if _, err = c.Put(ctx, "/Notas/a b.md", []byte("x"), ""); !errors.Is(err, nc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	_ = etag2

	if _, _, err = c.Get(ctx, "/no/existe"); !errors.Is(err, nc.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func appendLine(add string) func([]byte, bool) ([]byte, error) {
	return func(old []byte, exists bool) ([]byte, error) {
		return append(append([]byte{}, old...), add...), nil
	}
}

func TestAppendRetriesOn412(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()
	f.Files["/b.md"] = []byte("base\n")

	f.MutateBeforeNextPut("/b.md", []byte("base\nconcurrente\n"))
	if err := c.Append(ctx, "/b.md", appendLine("nuevo\n")); err != nil {
		t.Fatal(err)
	}
	if got := string(f.Files["/b.md"]); got != "base\nconcurrente\nnuevo\n" {
		t.Fatalf("got %q", got)
	}

	// 3 mutaciones seguidas: agota los reintentos
	f.PutCount = 0
	f.MutateBeforeNextPut("/b.md", []byte("m1"))
	f.MutateBeforeNextPut("/b.md", []byte("m2"))
	f.MutateBeforeNextPut("/b.md", []byte("m3"))
	err := c.Append(ctx, "/b.md", appendLine("x"))
	if !errors.Is(err, nc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if f.PutCount != 3 {
		t.Fatalf("PutCount = %d, want 3", f.PutCount)
	}
	if got := string(f.Files["/b.md"]); got != "m3" {
		t.Fatalf("file = %q, want m3 intact", got)
	}
}

func TestAppendCreatesWhenMissing(t *testing.T) {
	c, f := setup(t)
	var sawExists = true
	err := c.Append(context.Background(), "/n.md", func(old []byte, exists bool) ([]byte, error) {
		sawExists = exists
		return []byte("hola"), nil
	})
	if err != nil || sawExists || string(f.Files["/n.md"]) != "hola" {
		t.Fatalf("err=%v exists=%v file=%q", err, sawExists, f.Files["/n.md"])
	}
	if f.LastPutHeader.Get("If-None-Match") != "*" {
		t.Fatal("expected If-None-Match: *")
	}
}

func TestPut423RetriesOnce(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()
	f.FailNextPut(423)
	if _, err := c.Put(ctx, "/l.md", []byte("ok"), ""); err != nil {
		t.Fatal(err)
	}
	if f.PutCount != 2 || string(f.Files["/l.md"]) != "ok" {
		t.Fatalf("PutCount=%d file=%q", f.PutCount, f.Files["/l.md"])
	}
	f.PutCount = 0
	f.FailNextPut(423)
	f.FailNextPut(423)
	_, err := c.Put(ctx, "/l2.md", []byte("ok"), "")
	if !errors.Is(err, nc.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if f.PutCount != 2 {
		t.Fatalf("PutCount = %d, want 2", f.PutCount)
	}
}

func TestMkdirAllTolerates405(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()
	if err := c.MkdirAll(ctx, "/Bitacora/2026"); err != nil {
		t.Fatal(err)
	}
	if !f.Dirs["/Bitacora"] || !f.Dirs["/Bitacora/2026"] {
		t.Fatalf("dirs = %v", f.Dirs)
	}
	// ya existen -> 405 en cada MKCOL, sigue siendo OK
	if err := c.MkdirAll(ctx, "/Bitacora/2026"); err != nil {
		t.Fatal(err)
	}
	if err := c.MkdirAll(ctx, "/Bitacora/2026/Mi Carpeta"); err != nil {
		t.Fatal(err)
	}
}

func TestListParsesPropfind(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()
	f.Dirs["/Docs"] = true
	f.Dirs["/Docs/Sub carpeta"] = true
	f.Files["/Docs/mi archivo.txt"] = []byte("12345")
	f.Files["/Docs/otro.md"] = []byte("x")
	f.Files["/Docs/Sub carpeta/hondo.txt"] = []byte("no debe salir")

	es, err := c.List(ctx, "/Docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Fatalf("entries = %+v", es)
	}
	by := map[string]nc.Entry{}
	for _, e := range es {
		by[e.Path] = e
	}
	d := by["/Docs/Sub carpeta"]
	if !d.Dir || d.Name != "Sub carpeta" {
		t.Fatalf("dir = %+v", d)
	}
	a := by["/Docs/mi archivo.txt"]
	if a.Dir || a.Name != "mi archivo.txt" || a.Size != 5 || a.Modified.IsZero() {
		t.Fatalf("file = %+v", a)
	}
	if _, ok := by["/Docs/otro.md"]; !ok {
		t.Fatalf("missing otro.md: %+v", es)
	}
	if _, ok := by["/Docs"]; ok {
		t.Fatal("own folder must be excluded")
	}
}

func TestSearchName(t *testing.T) {
	c, f := setup(t)
	f.Files["/Docs/Informe Anual.pdf"] = []byte("a")
	f.Files["/informe-final.txt"] = []byte("b")
	f.Files["/otra cosa.txt"] = []byte("c")
	es, err := c.SearchName(context.Background(), "informe")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("entries = %+v", es)
	}
	paths := es[0].Path + "|" + es[1].Path
	if !strings.Contains(paths, "/Docs/Informe Anual.pdf") || !strings.Contains(paths, "/informe-final.txt") {
		t.Fatalf("paths = %s", paths)
	}
	for i := 0; i < 25; i++ {
		f.Files["/many/informe"+string(rune('a'+i%26))+string(rune('a'+i/26))] = []byte("x")
	}
	es, err = c.SearchName(context.Background(), "informe")
	if err != nil || len(es) != 20 {
		t.Fatalf("len=%d err=%v, want 20", len(es), err)
	}
}

func TestBasicAuthAndNoCredsInErrors(t *testing.T) {
	f := nctest.New(t, "ana")
	const pass = "s3cr3t-PASS"
	f.Pass = "otra"
	ctx := context.Background()

	check := func(label string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected error", label)
		}
		s := err.Error()
		if strings.Contains(s, pass) || strings.Contains(s, "ana:") || strings.Contains(s, "@") {
			t.Fatalf("%s: credentials leaked in %q", label, s)
		}
	}

	bad := nc.New(f.URL, "ana", pass)
	_, _, err := bad.Get(ctx, "/x")
	check("401 get", err)
	_, err = bad.Put(ctx, "/x", nil, "")
	check("401 put", err)
	check("401 mkdir", bad.MkdirAll(ctx, "/d"))
	_, err = bad.List(ctx, "/")
	check("401 list", err)
	_, err = bad.SearchName(ctx, "x")
	check("401 search", err)

	// credenciales embebidas en la URL base tampoco se filtran
	withUserinfo := nc.New(strings.Replace(f.URL, "http://", "http://ana:"+pass+"@", 1), "ana", pass)
	_, _, err = withUserinfo.Get(ctx, "/x")
	check("userinfo", err)

	// conexión rechazada
	f.Close()
	_, _, err = bad.Get(ctx, "/x")
	check("conn refused", err)

	// y la autenticación correcta funciona
	f2 := nctest.New(t, "ana")
	good := nc.New(f2.URL, "ana", f2.Pass)
	if _, err := good.Put(ctx, "/ok.txt", []byte("1"), ""); err != nil {
		t.Fatal(err)
	}
}

func TestSearchNameEscapesLikeWildcards(t *testing.T) {
	c, f := setup(t)
	f.Files["/100%_real.txt"] = []byte("a")
	f.Files["/100xyreal.txt"] = []byte("b")
	es, err := c.SearchName(context.Background(), "100%_")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Path != "/100%_real.txt" {
		t.Fatalf("entries = %+v", es)
	}
	if b := f.LastSearchBody(); !strings.Contains(b, `%100\%\_%`) {
		t.Fatalf("search body lacks escaped literal: %s", b)
	}
}
