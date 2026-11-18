package tools

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/bitacora"
	"nextcloud-mcp/confirm"
	"nextcloud-mcp/ical"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

func tareaTools(d *Deps) []Tool {
	return []Tool{
		{
			Def: mcp.NewTool("tareas_pendientes",
				mcp.WithDescription("Lista las tareas no completadas de un proyecto o de todos los proyectos."),
				optStr("proyecto", "Nombre del proyecto (lista de tareas); sin él se leen todos"),
				optStr("texto", "Filtra las tareas cuyo título contenga este texto"),
			),
			Handle: handler(d.tareasPendientes),
		},
		{
			Def: mcp.NewTool("tarea_crear",
				mcp.WithDescription("Crea una tarea en un proyecto. No crea duplicados de una tarea pendiente con el mismo título salvo con forzar. Si el proyecto no existe solo lo crea con crear_proyecto."),
				reqStr("proyecto", "Nombre del proyecto (lista de tareas)"),
				reqStr("titulo", "Título de la tarea"),
				optStr("vence", "Vencimiento AAAA-MM-DD o AAAA-MM-DDTHH:MM"),
				optStr("prioridad", "Prioridad: alta, media, baja o un número de 0 a 9"),
				optStr("descripcion", "Descripción o notas de la tarea"),
				optStr("dentro_de", "Título de la tarea padre, para crearla como subtarea"),
				optBool("crear_proyecto", "Crear el proyecto si no existe"),
				optBool("forzar", "Crear aunque ya exista una tarea pendiente con el mismo título"),
			),
			Handle: handler(d.tareaCrear),
		},
		{
			Def: mcp.NewTool("tarea_completar",
				mcp.WithDescription("Marca una tarea como hecha y lo anota en la bitácora del proyecto."),
				reqStr("proyecto", "Nombre del proyecto (lista de tareas)"),
				reqStr("tarea", "Título de la tarea"),
			),
			Handle: handler(d.tareaCompletar),
		},
		{
			Def: mcp.NewTool("tarea_anotar",
				mcp.WithDescription("Agrega un texto con fecha y hora al final de la descripción de una tarea."),
				reqStr("proyecto", "Nombre del proyecto (lista de tareas)"),
				reqStr("tarea", "Título de la tarea"),
				reqStr("texto", "Texto a agregar"),
			),
			Handle: handler(d.tareaAnotar),
		},
		{
			Def: mcp.NewTool("tarea_cambiar",
				mcp.WithDescription("Pide cambiar el título, el vencimiento o la prioridad de una tarea. No cambia nada: devuelve una pregunta de confirmación y un código para confirmar."),
				reqStr("proyecto", "Nombre del proyecto (lista de tareas)"),
				reqStr("tarea", "Título actual de la tarea"),
				optStr("nuevo_titulo", "Título nuevo"),
				optStr("vence", "Vencimiento nuevo AAAA-MM-DD o AAAA-MM-DDTHH:MM"),
				optStr("prioridad", "Prioridad nueva: alta, media, baja o un número de 0 a 9"),
			),
			Handle: handler(d.tareaCambiar),
		},
		{
			Def: mcp.NewTool("tarea_borrar",
				mcp.WithDescription("Pide borrar una tarea. No borra nada: devuelve una pregunta de confirmación y un código para confirmar."),
				reqStr("proyecto", "Nombre del proyecto (lista de tareas)"),
				reqStr("tarea", "Título de la tarea a borrar"),
			),
			Handle: handler(d.tareaBorrar),
		},
	}
}

// findProject looks name up among the task calendars. On a miss it returns
// the voice error listing the existing projects.
func (d *Deps) findProject(ctx context.Context, name string) (nc.Calendar, bool, *mcp.CallToolResult) {
	all, err := d.NC.Calendars(ctx)
	if err != nil {
		return nc.Calendar{}, false, failErr("No pude leer los proyectos", err)
	}
	cals := todoCalendars(all)
	if c, found := findCalendar(cals, name); found {
		return c, true, nil
	}
	msg := "No existe el proyecto " + name + "."
	if len(cals) > 0 {
		msg += " Proyectos: " + strings.Join(calNames(cals), ", ") + "."
	}
	return nc.Calendar{}, false, fail(msg)
}

// resolveTodo finds exactly one task of cal by title, UID or file name. With
// preferOpen, pending tasks win over completed ones with the same match.
func (d *Deps) resolveTodo(ctx context.Context, cal nc.Calendar, ref, what string, preferOpen bool) (calTodo, *mcp.CallToolResult) {
	todos, err := d.fetchTodos(ctx, []nc.Calendar{cal})
	if err != nil {
		return calTodo{}, failErr("No pude leer las tareas", err)
	}
	return d.pickTodo(todos, ref, what, preferOpen)
}

func (d *Deps) pickTodo(todos []calTodo, ref, what string, preferOpen bool) (calTodo, *mcp.CallToolResult) {
	titles := make([]string, len(todos))
	for i, t := range todos {
		titles[i] = t.Summary
	}
	idx := text.Find(titles, ref)
	if len(idx) == 0 {
		for i, t := range todos {
			if t.UID == ref || strings.TrimSuffix(lastPath(t.Obj.Href), ".ics") == ref {
				idx = []int{i}
				break
			}
		}
	}
	if preferOpen && len(idx) > 1 {
		var open []int
		for _, i := range idx {
			if !isCompleted(todos[i].Todo) {
				open = append(open, i)
			}
		}
		if len(open) > 0 {
			idx = open
		}
	}
	switch {
	case len(idx) == 0:
		msg := "No encontré " + what + " " + ref + "."
		if c := text.Candidates(titles, ref, 5); len(c) > 0 {
			msg += " Parecidas: " + strings.Join(c, ", ") + "."
		}
		return calTodo{}, fail(msg)
	case len(idx) > 1:
		opts := make([]string, len(idx))
		for k, i := range idx {
			opts[k] = d.fmtTodo(todos[i], false)
		}
		return calTodo{}, fail("Hay varias tareas que coinciden: " + text.List(opts, 10) + ". Dime cuál.")
	}
	return todos[idx[0]], nil
}

// fmtDue formats a due date for voice and for disambiguation.
func (d *Deps) fmtDue(t ical.Todo) string {
	if t.Due == nil {
		return "sin fecha"
	}
	if t.DueIsDate {
		return fmtDate(*t.Due)
	}
	return fmtDate(t.Due.In(d.loc())) + " " + t.Due.In(d.loc()).Format("15:04")
}

func (d *Deps) fmtTodo(t calTodo, withProject bool) string {
	s := t.Summary
	if withProject {
		s += " [" + t.Cal.Name + "]"
	}
	if t.Due != nil {
		s += " — vence " + d.fmtDue(t.Todo)
	}
	if w := text.PriorityWord(t.Priority); w == "alta" {
		s += ", prioridad alta"
	}
	if isCompleted(t.Todo) {
		s += " (completada)"
	}
	return s
}

func prioWord(p int) string {
	if w := text.PriorityWord(p); w != "" {
		return w
	}
	return "sin prioridad"
}

// objUID is the object name the task is stored under, for PUT.
func objUID(href string) string { return strings.TrimSuffix(lastPath(href), ".ics") }

func (d *Deps) tareasPendientes(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	var cals []nc.Calendar
	scope := ""
	if p := str(req, "proyecto"); p != "" {
		c, _, r := d.findProject(ctx, p)
		if r != nil {
			return r
		}
		cals, scope = []nc.Calendar{c}, " en "+c.Name
	} else {
		all, err := d.NC.Calendars(ctx)
		if err != nil {
			return failErr("No pude leer los proyectos", err)
		}
		cals = todoCalendars(all)
	}
	todos, err := d.fetchTodos(ctx, cals)
	if err != nil {
		return failErr("No pude leer las tareas", err)
	}
	q := text.Normalize(str(req, "texto"))
	var lines []string
	for _, t := range todos {
		if isCompleted(t.Todo) || (q != "" && !strings.Contains(text.Normalize(t.Summary), q)) {
			continue
		}
		lines = append(lines, d.fmtTodo(t, scope == ""))
	}
	if q != "" {
		scope += " con '" + str(req, "texto") + "'"
	}
	if len(lines) == 0 {
		return ok("No hay tareas pendientes" + scope + ".")
	}
	return ok(fmt.Sprintf("Tareas pendientes%s (%d): %s.", scope, len(lines), text.List(lines, 20)))
}

func (d *Deps) tareaCrear(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto", "titulo"); r != nil {
		return r
	}
	titulo, proyecto, dentro := str(req, "titulo"), str(req, "proyecto"), str(req, "dentro_de")
	prio, err := text.ParsePriority(str(req, "prioridad"))
	if err != nil {
		return fail(err.Error())
	}
	todo := ical.Todo{UID: newUID(), Summary: titulo, Description: str(req, "descripcion"), Priority: prio}
	if v := str(req, "vence"); v != "" {
		due, isDate, err := text.ParseWhen(v, d.loc())
		if err != nil {
			return fail(err.Error())
		}
		todo.Due, todo.DueIsDate = &due, isDate
	}

	cal, found, miss := d.findProject(ctx, proyecto)
	created := false
	if !found {
		if miss != nil && !flag(req, "crear_proyecto") {
			return miss
		}
		if dentro != "" {
			return fail("El proyecto " + proyecto + " no existe, así que no tiene la tarea padre " + dentro + ".")
		}
		if cal, err = d.NC.MkCalendarTodos(ctx, proyecto); err != nil {
			return failErr("No pude crear el proyecto", err)
		}
		created = true
	}

	var parent string
	if !created {
		todos, err := d.fetchTodos(ctx, []nc.Calendar{cal})
		if err != nil {
			return failErr("No pude leer las tareas", err)
		}
		if !flag(req, "forzar") {
			for _, t := range todos {
				if !isCompleted(t.Todo) && text.Normalize(t.Summary) == text.Normalize(titulo) {
					return fail("Ya existe la tarea pendiente '" + t.Summary + "' en " + cal.Name + ". Si igual quieres crearla, repite con forzar.")
				}
			}
		}
		if dentro != "" {
			p, r := d.pickTodo(todos, dentro, "la tarea padre", true)
			if r != nil {
				return r
			}
			todo.ParentUID, parent = p.UID, p.Summary
		}
	}

	if _, err := d.NC.PutObject(ctx, cal.ID, todo.UID, ical.MakeVTodo(todo, d.now()), ""); err != nil {
		return failErr("No pude crear la tarea", err)
	}
	var b strings.Builder
	if created {
		b.WriteString("Creé el proyecto " + cal.Name + " y la tarea '" + titulo + "'")
	} else {
		b.WriteString("Creé la tarea '" + titulo + "' en " + cal.Name)
	}
	if parent != "" {
		b.WriteString(", dentro de '" + parent + "'")
	}
	if todo.Due != nil {
		b.WriteString(", vence " + text.Human(*todo.Due, d.now(), !todo.DueIsDate))
	}
	b.WriteString(", prioridad " + prioWord(prio) + ".")
	return ok(b.String())
}

func (d *Deps) tareaCompletar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto", "tarea"); r != nil {
		return r
	}
	cal, _, r := d.findProject(ctx, str(req, "proyecto"))
	if r != nil {
		return r
	}
	t, r := d.resolveTodo(ctx, cal, str(req, "tarea"), "la tarea", true)
	if r != nil {
		return r
	}
	if isCompleted(t.Todo) {
		return ok("La tarea '" + t.Summary + "' ya estaba completada.")
	}
	now := d.now()
	data := ical.SetVTodoField(t.Obj.Data, "STATUS", "COMPLETED")
	data = ical.SetVTodoField(data, "PERCENT-COMPLETE", "100")
	data = ical.SetVTodoField(data, "COMPLETED", ical.ToUTC(now))
	if _, err := d.NC.PutObject(ctx, cal.ID, objUID(t.Obj.Href), data, t.Obj.ETag); err != nil {
		return failErr("No pude completar la tarea", err)
	}
	if err := d.logCompleted(ctx, cal, t.Summary, now); err != nil {
		return ok("Tarea completada; no pude anotarla en la bitácora: " + oneLine(err.Error()))
	}
	return ok("Listo, completé '" + t.Summary + "' en " + cal.Name + " y lo anoté en la bitácora.")
}

// logCompleted appends "Completada: title" to the bitácora named after the
// project id.
func (d *Deps) logCompleted(ctx context.Context, cal nc.Calendar, title string, now time.Time) error {
	p := bitacora.Path(cal.ID, now)
	if err := d.NC.MkdirAll(ctx, path.Dir(p)); err != nil {
		return err
	}
	entry := bitacora.EntryText(now.Format("15:04"), "Completada: "+title, cal.Name)
	return d.NC.Append(ctx, p, func(old []byte, exists bool) ([]byte, error) {
		if !exists {
			return []byte(bitacora.Header(cal.ID, now) + entry), nil
		}
		return []byte(appendBlock(string(old), entry)), nil
	})
}

func (d *Deps) tareaAnotar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto", "tarea", "texto"); r != nil {
		return r
	}
	cal, _, r := d.findProject(ctx, str(req, "proyecto"))
	if r != nil {
		return r
	}
	t, r := d.resolveTodo(ctx, cal, str(req, "tarea"), "la tarea", true)
	if r != nil {
		return r
	}
	line := "[" + d.now().Format("02/01/2006 15:04") + "] " + str(req, "texto")
	desc := line
	if strings.TrimSpace(t.Description) != "" {
		desc = strings.TrimRight(t.Description, "\n") + "\n" + line
	}
	data := ical.SetVTodoField(t.Obj.Data, "DESCRIPTION", desc)
	if _, err := d.NC.PutObject(ctx, cal.ID, objUID(t.Obj.Href), data, t.Obj.ETag); err != nil {
		return failErr("No pude anotar en la tarea", err)
	}
	return ok("Anoté en la tarea '" + t.Summary + "' de " + cal.Name + ".")
}

func (d *Deps) tareaCambiar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto", "tarea"); r != nil {
		return r
	}
	nuevo, vence, prioridad := str(req, "nuevo_titulo"), str(req, "vence"), str(req, "prioridad")
	if nuevo == "" && vence == "" && prioridad == "" {
		return fail("Dime qué cambio: nuevo_titulo, vence o prioridad.")
	}
	var newTodo ical.Todo
	if vence != "" {
		due, isDate, err := text.ParseWhen(vence, d.loc())
		if err != nil {
			return fail(err.Error())
		}
		newTodo.Due, newTodo.DueIsDate = &due, isDate
	}
	newPrio := 0
	if prioridad != "" {
		p, err := text.ParsePriority(prioridad)
		if err != nil {
			return fail(err.Error())
		}
		newPrio = p
	}
	cal, _, r := d.findProject(ctx, str(req, "proyecto"))
	if r != nil {
		return r
	}
	t, r := d.resolveTodo(ctx, cal, str(req, "tarea"), "la tarea", true)
	if r != nil {
		return r
	}
	obj, err := d.NC.GetObject(ctx, t.Obj.Href)
	if err != nil {
		return failErr("No pude leer la tarea", err)
	}
	var changes []string
	params := map[string]string{}
	if nuevo != "" {
		changes = append(changes, "título "+t.Summary+" → "+nuevo)
		params["nuevo_titulo"] = nuevo
	}
	if vence != "" {
		changes = append(changes, "vence "+d.fmtDue(t.Todo)+" → "+d.fmtDue(newTodo))
		params["vence"] = vence
	}
	if prioridad != "" {
		changes = append(changes, "prioridad "+prioWord(t.Priority)+" → "+prioWord(newPrio))
		params["prioridad"] = prioridad
	}
	summary := fmt.Sprintf("¿Cambio la tarea '%s' de %s: %s?", t.Summary, cal.Name, strings.Join(changes, "; "))
	return d.issue(confirm.Pending{Action: "tarea_cambiar", Summary: summary, Href: obj.Href, ETag: obj.ETag, Params: params})
}

func (d *Deps) tareaBorrar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := missing(req, "proyecto", "tarea"); r != nil {
		return r
	}
	cal, _, r := d.findProject(ctx, str(req, "proyecto"))
	if r != nil {
		return r
	}
	t, r := d.resolveTodo(ctx, cal, str(req, "tarea"), "la tarea", false)
	if r != nil {
		return r
	}
	obj, err := d.NC.GetObject(ctx, t.Obj.Href)
	if err != nil {
		return failErr("No pude leer la tarea", err)
	}
	summary := fmt.Sprintf("¿Borro la tarea '%s' de %s?", t.Summary, cal.Name)
	return d.issue(confirm.Pending{Action: "tarea_borrar", Summary: summary, Href: obj.Href, ETag: obj.ETag})
}
