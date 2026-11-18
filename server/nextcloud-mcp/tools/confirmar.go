package tools

import (
	"context"
	"errors"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/ical"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/text"
)

func confirmarTools(d *Deps) []Tool {
	return []Tool{{
		Def: mcp.NewTool("confirmar",
			mcp.WithDescription("Ejecuta una acción pendiente (borrar o cambiar). Llámala SOLO si el usuario dijo que sí en su último mensaje, con el código que te dio la herramienta."),
			mcp.WithString("codigo", mcp.Required(), mcp.Description("Código de 4 dígitos, por ejemplo 4821.")),
		),
		Handle: handler(d.confirmar),
	}}
}

// executor runs a confirmed action on the freshly read object.
type executor func(ctx context.Context, d *Deps, p confirm.Pending, obj nc.CalObject) error

var executors = map[string]executor{
	"evento_cancelar": execDelete,
	"tarea_borrar":    execDelete,
	"tarea_cambiar":   execTareaCambiar,
}

// confirmCode reads codigo as exactly four digits. A JSON number keeps its
// leading zeros (482 → "0482").
func confirmCode(req mcp.CallToolRequest) (string, bool) {
	switch v := req.GetArguments()["codigo"].(type) {
	case float64:
		if v < 0 || v > 9999 || v != math.Trunc(v) {
			return "", false
		}
		return strconv.Itoa(10000 + int(v))[1:], true
	case string:
		s := strings.TrimSpace(v)
		if len(s) != 4 {
			return "", false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return "", false
			}
		}
		return s, true
	}
	return "", false
}

func (d *Deps) confirmar(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
	code, okCode := confirmCode(req)
	if !okCode {
		return fail("El código debe tener 4 dígitos, por ejemplo 4821.")
	}
	p, err := d.Confirm.Take(code)
	switch {
	case errors.Is(err, confirm.ErrExpired):
		return fail("El código " + code + " ya venció; repite la orden.")
	case err != nil:
		return fail("El código " + code + " no existe o ya se usó; repite la orden.")
	}
	exec, known := executors[p.Action]
	if !known {
		return fail("No sé ejecutar la acción pendiente; repite la orden.")
	}
	what, changed := "la tarea", "La tarea cambió desde que la pedí; repite la orden."
	if strings.HasPrefix(p.Action, "evento") {
		what, changed = "el evento", "El evento cambió desde que lo pedí; repite la orden."
	}
	obj, err := d.NC.GetObject(ctx, p.Href)
	if errors.Is(err, nc.ErrNotFound) {
		return fail("Ya no existe " + what + "; no hice nada.")
	}
	if err != nil {
		return failErr("No pude releer "+what, err)
	}
	if obj.ETag != p.ETag {
		return fail(changed)
	}
	if err := exec(ctx, d, p, obj); err != nil {
		if errors.Is(err, nc.ErrConflict) {
			return fail(changed)
		}
		return failErr("No pude completar la acción", err)
	}
	return ok("Hecho: " + doneText(p.Summary))
}

// doneText turns the confirmation question into a past-tense statement.
func doneText(summary string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(summary, "¿"), "?")
	for q, done := range map[string]string{"Borro ": "borré ", "Cambio ": "cambié "} {
		if strings.HasPrefix(s, q) {
			return done + strings.TrimPrefix(s, q) + "."
		}
	}
	return s + "."
}

func execDelete(ctx context.Context, d *Deps, p confirm.Pending, _ nc.CalObject) error {
	return d.NC.DeleteObject(ctx, p.Href, p.ETag)
}

func execTareaCambiar(ctx context.Context, d *Deps, p confirm.Pending, obj nc.CalObject) error {
	data := obj.Data
	if v := p.Params["nuevo_titulo"]; v != "" {
		data = ical.SetVTodoField(data, "SUMMARY", v)
	}
	if v := p.Params["vence"]; v != "" {
		due, isDate, err := text.ParseWhen(v, d.loc())
		if err != nil {
			return err
		}
		data = setDue(data, due, isDate)
	}
	if v := p.Params["prioridad"]; v != "" {
		prio, err := text.ParsePriority(v)
		if err != nil {
			return err
		}
		data = ical.SetVTodoField(data, "PRIORITY", strconv.Itoa(prio))
	}
	calID, uid := hrefParts(p.Href)
	_, err := d.NC.PutObject(ctx, calID, uid, data, p.ETag)
	return err
}

// dueMarker is a placeholder value that SetVTodoField writes in place of the
// old DUE (dropping its parameters and folded lines) before the real line,
// which may need ;VALUE=DATE, replaces it.
const dueMarker = "x-nextcloud-mcp-due"

// setDue replaces or adds the VTODO DUE with a DATE or UTC value, written
// the same way MakeVTodo does.
func setDue(ics string, due time.Time, isDate bool) string {
	line := "DUE:" + ical.ToUTC(due)
	if isDate {
		line = "DUE;VALUE=DATE:" + ical.ToDate(due)
	}
	return strings.Replace(ical.SetVTodoField(ics, "DUE", dueMarker), "DUE:"+dueMarker, line, 1)
}

// hrefParts returns the calendar ID and object name (without .ics) of a
// calendar object href, unescaped so PutObject does not escape them twice.
func hrefParts(href string) (calID, uid string) {
	dir, file := path.Split(strings.TrimRight(href, "/"))
	calID = path.Base(dir)
	if u, err := url.PathUnescape(calID); err == nil {
		calID = u
	}
	uid = strings.TrimSuffix(file, ".ics")
	if u, err := url.PathUnescape(uid); err == nil {
		uid = u
	}
	return calID, uid
}
