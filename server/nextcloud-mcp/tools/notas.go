package tools

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

// projectsRoot holds one folder per project with its estado.md.
const projectsRoot = "/Proyectos"

func notaTools(d *Deps) []Tool {
	return []Tool{
		{
			Def: mcp.NewTool("nota_guardar",
				mcp.WithDescription("Guarda texto en una nota de Nextcloud. Si ya existe una nota con ese título exacto, agrega el texto al final con fecha y hora; si no, crea la nota. Nunca borra contenido."),
				reqStr("titulo", "Título de la nota."),
				reqStr("texto", "Texto a guardar."),
			),
			Handle: handler(d.notaGuardar),
		},
		{
			Def: mcp.NewTool("notas_buscar",
				mcp.WithDescription("Busca notas por título o contenido. Si hay una sola coincidencia la lee; si hay varias, lista los títulos."),
				reqStr("texto", "Texto o título a buscar."),
			),
			Handle: handler(d.notasBuscar),
		},
		{
			Def: mcp.NewTool("estado_proyecto_anotar",
				mcp.WithDescription("Agrega una actualización con fecha al archivo de estado de un proyecto (Proyectos/<proyecto>/estado.md). Solo agrega, nunca borra."),
				reqStr("proyecto", "Nombre del proyecto."),
				reqStr("texto", "Texto de la actualización."),
				optStr("seccion", "Título de la sección (por defecto 'Actualización')."),
			),
			Handle: handler(d.estadoAnotar),
		},
		{
			Def: mcp.NewTool("estado_proyecto_leer",
				mcp.WithDescription("Lee el archivo de estado de un proyecto."),
				reqStr("proyecto", "Nombre del proyecto."),
			),
			Handle: handler(d.estadoLeer),
		},
	}
}

func (d *Deps) notaGuardar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "titulo", "texto"); r != nil {
		return r
	}
	titulo, texto := str(req, "titulo"), str(req, "texto")
	want := text.Normalize(titulo)
	block := "\n\n---\n**" + d.now().Format("02/01/2006 15:04") + "**\n\n" + texto
	for attempt := 0; attempt < 3; attempt++ {
		notes, err := d.NC.Notes(ctx)
		if err != nil {
			return failErr("No pude leer las notas", err)
		}
		sort.Slice(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
		var found *nc.Note
		for i := range notes {
			if text.Normalize(notes[i].Title) == want {
				found = &notes[i]
				break
			}
		}
		if found == nil {
			n, err := d.NC.CreateNote(ctx, titulo, texto)
			if err != nil {
				return failErr("No pude crear la nota", err)
			}
			t := n.Title
			if t == "" {
				t = titulo
			}
			return ok(fmt.Sprintf("Creé la nota '%s'", t))
		}
		// Re-read: list entries may omit or truncate content.
		n, err := d.NC.Note(ctx, found.ID)
		if err != nil {
			return failErr("No pude leer la nota", err)
		}
		err = d.NC.UpdateNoteContent(ctx, n, strings.TrimRight(n.Content, "\n")+block)
		if err == nil {
			return ok(fmt.Sprintf("Agregado a la nota existente '%s'", n.Title))
		}
		if !errors.Is(err, nc.ErrConflict) {
			return failErr("No pude actualizar la nota", err)
		}
	}
	return fail("La nota '" + titulo + "' cambió varias veces mientras la editaba; inténtalo de nuevo.")
}

func (d *Deps) notasBuscar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "texto"); r != nil {
		return r
	}
	q := str(req, "texto")
	nq := text.Normalize(q)
	if nq == "" {
		return fail("La búsqueda no es válida.")
	}
	notes, err := d.NC.Notes(ctx)
	if err != nil {
		return failErr("No pude leer las notas", err)
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
	var hits, exact []nc.Note
	for _, n := range notes {
		nt := text.Normalize(n.Title)
		if nt == nq {
			exact = append(exact, n)
		}
		if strings.Contains(nt, nq) || strings.Contains(text.Normalize(n.Content), nq) {
			hits = append(hits, n)
		}
	}
	if len(exact) == 1 {
		hits = exact
	}
	switch len(hits) {
	case 0:
		return ok("No encontré notas con '" + q + "'.")
	case 1:
		n, err := d.NC.Note(ctx, hits[0].ID)
		if err != nil {
			return failErr("No pude leer la nota", err)
		}
		return ok("Nota '" + n.Title + "':\n" + text.Clip(n.Content, clipMax))
	}
	titles := make([]string, len(hits))
	for i, n := range hits {
		titles[i] = n.Title
	}
	return ok(fmt.Sprintf("Encontré %d notas: %s. Dime cuál quieres.", len(hits), text.List(titles, 10)))
}

// projectDir returns the existing project folder matching name (case and
// accent insensitive), or "" when there is none.
func (d *Deps) projectDir(ctx context.Context, name string) (string, error) {
	es, err := d.NC.List(ctx, projectsRoot)
	if err != nil {
		if errors.Is(err, nc.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	want := text.Normalize(name)
	for _, e := range es {
		if e.Dir && text.Normalize(e.Name) == want {
			return path.Join(projectsRoot, e.Name), nil
		}
	}
	return "", nil
}

func (d *Deps) estadoAnotar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto", "texto"); r != nil {
		return r
	}
	proyecto, texto := str(req, "proyecto"), str(req, "texto")
	folder := strings.TrimSpace(strings.ReplaceAll(proyecto, "/", "-"))
	if text.Normalize(folder) == "" || folder == "." || folder == ".." {
		return fail("El nombre del proyecto no es válido.")
	}
	seccion := str(req, "seccion")
	if seccion == "" {
		seccion = "Actualización"
	}
	dir, err := d.projectDir(ctx, proyecto)
	if err != nil {
		return failErr("No pude revisar la carpeta de proyectos", err)
	}
	if dir == "" {
		dir = path.Join(projectsRoot, folder)
		if err := d.NC.MkdirAll(ctx, dir); err != nil {
			return failErr("No pude crear la carpeta del proyecto", err)
		}
	}
	block := "## " + fmtDate(d.now()) + " — " + seccion + "\n\n" + texto + "\n"
	err = d.NC.Append(ctx, path.Join(dir, "estado.md"), func(old []byte, exists bool) ([]byte, error) {
		if !exists {
			return []byte(appendBlock("# Estado: "+proyecto+"\n", block)), nil
		}
		return []byte(appendBlock(string(old), block)), nil
	})
	if err != nil {
		return failErr("No pude anotar el estado", err)
	}
	return ok(fmt.Sprintf("Anoté en el estado de %s: %s", path.Base(dir), seccion))
}

func (d *Deps) estadoLeer(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto"); r != nil {
		return r
	}
	proyecto := str(req, "proyecto")
	dir, err := d.projectDir(ctx, proyecto)
	if err != nil {
		return failErr("No pude revisar la carpeta de proyectos", err)
	}
	if dir == "" {
		return fail("No hay estado anotado para el proyecto " + proyecto + ".")
	}
	b, _, err := d.NC.Get(ctx, path.Join(dir, "estado.md"))
	if errors.Is(err, nc.ErrNotFound) {
		return fail("No hay estado anotado para el proyecto " + proyecto + ".")
	}
	if err != nil {
		return failErr("No pude leer el estado", err)
	}
	return ok(text.Clip(string(b), clipMax))
}
