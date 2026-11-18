// Package tools implements the Spanish voice-assistant MCP tools over
// Nextcloud. Every handler answers with plain text for voice; domain and
// network failures come back as a one-line tool error, never a Go error.
package tools

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // TZ must resolve inside minimal containers

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/nc"
)

// Deps are the shared dependencies of all tools.
type Deps struct {
	NC      *nc.Client
	Confirm *confirm.Store
	Now     func() time.Time
	Loc     *time.Location
}

// Tool is one MCP tool definition with its handler.
type Tool struct {
	Def    mcp.Tool
	Handle server.ToolHandlerFunc
}

// All returns every tool, bound to d.
func All(d *Deps) []Tool {
	var out []Tool
	out = append(out, bitacoraTools(d)...)
	out = append(out, agendaTools(d)...)
	out = append(out, tareaTools(d)...)
	return out
}

// maxParallel caps concurrent requests issued by a single tool call.
const maxParallel = 4

// clipMax is the voice size limit for content answers.
const clipMax = 2000

func (d *Deps) loc() *time.Location {
	if d.Loc != nil {
		return d.Loc
	}
	return time.Local
}

func (d *Deps) now() time.Time {
	n := time.Now
	if d.Now != nil {
		n = d.Now
	}
	return n().In(d.loc())
}

func ok(s string) *mcp.CallToolResult { return mcp.NewToolResultText(s) }

func fail(msg string) *mcp.CallToolResult { return mcp.NewToolResultError(oneLine(msg)) }

var urlRe = regexp.MustCompile(`https?://\S+`)

// oneLine strips URLs and collapses whitespace so errors are safe for voice.
func oneLine(s string) string {
	s = urlRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// failErr reports err after prefix without URLs or credentials.
func failErr(prefix string, err error) *mcp.CallToolResult {
	return fail(prefix + ": " + oneLine(err.Error()))
}

// str returns argument k as trimmed text. Numbers and booleans sent by the
// LLM are accepted and formatted.
func str(req mcp.CallToolRequest, k string) string {
	v, ok := req.GetArguments()[k]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func flag(req mcp.CallToolRequest, k string) bool { return req.GetBool(k, false) }

// missing returns a failure naming the first empty required argument.
func missing(req mcp.CallToolRequest, keys ...string) *mcp.CallToolResult {
	for _, k := range keys {
		if str(req, k) == "" {
			return fail("Falta el parámetro " + k + ".")
		}
	}
	return nil
}

// handler adapts a simple function to server.ToolHandlerFunc.
func handler(f func(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return f(ctx, req), nil
	}
}

func reqStr(name, desc string) mcp.ToolOption {
	return mcp.WithString(name, mcp.Required(), mcp.Description(desc))
}

func optStr(name, desc string) mcp.ToolOption {
	return mcp.WithString(name, mcp.Description(desc))
}

func optBool(name, desc string) mcp.ToolOption {
	return mcp.WithBoolean(name, mcp.Description(desc))
}

func fmtDate(t time.Time) string { return t.Format("02/01/2006") }

// dayStart returns local midnight of t.
func dayStart(t time.Time) time.Time {
	y, m, dd := t.Date()
	return time.Date(y, m, dd, 0, 0, 0, 0, t.Location())
}

// quoteSafe makes s safe inside the double-quoted confirmation prompt.
func quoteSafe(s string) string { return strings.ReplaceAll(s, `"`, "'") }
