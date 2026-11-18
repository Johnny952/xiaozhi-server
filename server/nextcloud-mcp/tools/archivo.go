package tools

import (
	"bytes"
	"context"
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

// textExts are extensions read aloud without sniffing. Files without an
// extension are sniffed (valid UTF-8, no NUL); any other extension is refused
// before download.
var textExts = map[string]bool{
	".md": true, ".txt": true, ".markdown": true, ".csv": true, ".json": true,
	".yaml": true, ".yml": true, ".org": true, ".log": true, ".ics": true,
	".vcf": true, ".xml": true, ".html": true,
}

// maxReadBytes bounds what archivo_leer downloads; the answer is clipped to
// clipMax characters anyway.
const maxReadBytes = 64 << 10

func archivoTools(d *Deps) []Tool {
	return []Tool{{
		Def: mcp.NewTool("archivo_leer",
			mcp.WithDescription("Lee un archivo de texto de Nextcloud por ruta (con '/') o buscándolo por nombre. Solo lee; no modifica nada."),
			reqStr("ruta_o_nombre", "Ruta del archivo (p. ej. /Documentos/lista.md) o parte de su nombre."),
		),
		Handle: handler(d.archivoLeer),
	}}
}

func (d *Deps) archivoLeer(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "ruta_o_nombre"); r != nil {
		return r
	}
	q := str(req, "ruta_o_nombre")
	var p string
	if strings.Contains(q, "/") {
		p = path.Clean("/" + strings.TrimLeft(q, "/"))
	} else {
		es, err := d.NC.SearchName(ctx, q)
		if err != nil {
			return failErr("No pude buscar el archivo", err)
		}
		var files []nc.Entry
		for _, e := range es {
			if !e.Dir {
				files = append(files, e)
			}
		}
		switch len(files) {
		case 0:
			return fail("No encontré ningún archivo llamado " + q + ".")
		case 1:
			p = files[0].Path
		default:
			var exact []nc.Entry
			for _, e := range files {
				if strings.EqualFold(e.Name, q) {
					exact = append(exact, e)
				}
			}
			if len(exact) == 1 {
				p = exact[0].Path
				break
			}
			paths := make([]string, len(files))
			for i, e := range files {
				paths[i] = e.Path
			}
			return fail("Hay varios archivos que coinciden: " + text.List(paths, 10) + ". Dime cuál (con la ruta).")
		}
	}
	ext := strings.ToLower(path.Ext(p))
	if ext != "" && !textExts[ext] {
		return fail("No puedo leer en voz alta un archivo " + strings.ToUpper(strings.TrimPrefix(ext, ".")) + ".")
	}
	b, _, err := d.NC.GetPrefix(ctx, p, maxReadBytes)
	if errors.Is(err, nc.ErrNotFound) {
		return fail("No existe el archivo " + p + ".")
	}
	if err != nil {
		return failErr("No pude leer el archivo", err)
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return fail("No puedo leer en voz alta el archivo " + p + ": no es texto.")
	}
	return ok(p + ":\n" + text.Clip(string(b), clipMax))
}
