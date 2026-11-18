package tools

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/nctest"
)

var testLoc = func() *time.Location {
	l, err := time.LoadLocation("America/Santiago")
	if err != nil {
		panic(err)
	}
	return l
}()

// testNow is Wednesday 2026-11-18 10:00 local.
var testNow = time.Date(2026, 11, 18, 10, 0, 0, 0, testLoc)

// newDeps returns deps against a fresh fake. Confirmation codes are always
// "4821" (0x12D5) while only one is live.
func newDeps(t *testing.T) (*Deps, *nctest.Fake) {
	t.Helper()
	f := nctest.New(t, "user")
	now := func() time.Time { return testNow }
	d := &Deps{
		NC:      nc.New(f.URL, "user", f.Pass),
		Confirm: confirm.New(now, bytes.NewReader(bytes.Repeat([]byte{0x12, 0xD5}, 5000))),
		Now:     now,
		Loc:     testLoc,
	}
	return d, f
}

// call runs the named tool and returns its single text content.
func call(t *testing.T, tools []Tool, name string, args map[string]any) (string, bool) {
	t.Helper()
	for _, tl := range tools {
		if tl.Def.Name != name {
			continue
		}
		var req mcp.CallToolRequest
		req.Params.Name = name
		req.Params.Arguments = args
		res, err := tl.Handle(context.Background(), req)
		if err != nil {
			t.Fatalf("%s returned Go error: %v", name, err)
		}
		if res == nil || len(res.Content) != 1 {
			t.Fatalf("%s: want exactly one content, got %+v", name, res)
		}
		tc, ok := res.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("%s: content is not text: %T", name, res.Content[0])
		}
		return tc.Text, res.IsError
	}
	t.Fatalf("tool %q not registered", name)
	return "", false
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, nots ...string) {
	t.Helper()
	for _, w := range nots {
		if strings.Contains(got, w) {
			t.Errorf("output must not contain %q:\n%s", w, got)
		}
	}
}

func TestAllToolsHaveSpanishDescriptions(t *testing.T) {
	d, _ := newDeps(t)
	seen := map[string]bool{}
	for _, tl := range All(d) {
		if seen[tl.Def.Name] {
			t.Errorf("duplicate tool %s", tl.Def.Name)
		}
		seen[tl.Def.Name] = true
		if tl.Def.Description == "" {
			t.Errorf("%s has no description", tl.Def.Name)
		}
		for p, s := range tl.Def.InputSchema.Properties {
			m, _ := s.(map[string]any)
			if m["description"] == nil || m["description"] == "" {
				t.Errorf("%s.%s has no description", tl.Def.Name, p)
			}
		}
	}
}
