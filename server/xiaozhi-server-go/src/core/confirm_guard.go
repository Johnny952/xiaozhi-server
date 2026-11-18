package core

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// confirmGuard ensures the LLM can only call the "confirmar" MCP tool when the
// user actually confirmed (affirmation or the spoken code) in the utterance
// that follows a PENDIENTE tool result. The zero value is ready to use.
type confirmGuard struct {
	mu      sync.Mutex
	pending string // code awaiting the user's next utterance
	enabled string // code enabled for exactly one confirm call
}

var pendingCodeRe = regexp.MustCompile(`codigo=(\d{4})`)

// OnToolResult records a pending code and clears any standing enablement.
func (g *confirmGuard) OnToolResult(result string) {
	if !strings.HasPrefix(result, "PENDIENTE:") {
		return
	}
	m := pendingCodeRe.FindStringSubmatch(result)
	if m == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending = m[1]
	g.enabled = ""
}

// OnUserUtterance evaluates the user's phrase against the pending code.
func (g *confirmGuard) OnUserUtterance(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == "" {
		g.enabled = ""
		return
	}
	code := g.pending
	g.pending = ""
	g.enabled = ""
	if containsCode(text, code) || (containsAffirmation(text) && !containsNegation(text)) {
		g.enabled = code
	}
}

// Authorize reports whether the tool call may proceed. Non-confirm tools
// always pass. Any confirm attempt consumes the enablement.
func (g *confirmGuard) Authorize(toolName string, args map[string]interface{}) bool {
	if !isConfirmTool(toolName) {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	enabled := g.enabled
	g.enabled = ""
	if enabled == "" {
		return false
	}
	code, ok := parseCode(args["codigo"]) // nil map index is safe
	return ok && code == enabled
}

func parseCode(v interface{}) (string, bool) {
	switch c := v.(type) {
	case float64:
		if math.IsNaN(c) || c < 0 || c > 9999 || c != math.Trunc(c) {
			return "", false
		}
		return fmt.Sprintf("%04d", int(c)), true
	case string:
		if len(c) != 4 {
			return "", false
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				return "", false
			}
		}
		return c, true
	}
	return "", false
}

func isConfirmTool(name string) bool {
	name = strings.TrimPrefix(name, "mcp_")
	return name == "confirmar" || name == "confirm"
}

var accentMap = map[rune]rune{
	'á': 'a', 'à': 'a', 'ä': 'a', 'â': 'a',
	'é': 'e', 'è': 'e', 'ë': 'e', 'ê': 'e',
	'í': 'i', 'ì': 'i', 'ï': 'i', 'î': 'i',
	'ó': 'o', 'ò': 'o', 'ö': 'o', 'ô': 'o',
	'ú': 'u', 'ù': 'u', 'ü': 'u', 'û': 'u',
}

// tokenize lowercases, strips accents and splits on non letter/digit runes.
func tokenize(text string) []string {
	lowered := strings.Map(func(r rune) rune {
		r = unicode.ToLower(r)
		if m, ok := accentMap[r]; ok {
			return m
		}
		return r
	}, text)
	return strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

var (
	affirmations = map[string]bool{"si": true, "dale": true, "confirmo": true, "hazlo": true, "ok": true, "okay": true}
	negations    = map[string]bool{"no": true, "nunca": true, "cancela": true, "cancelar": true, "espera": true}
	digitWords   = map[string]string{
		"cero": "0", "uno": "1", "dos": "2", "tres": "3", "cuatro": "4",
		"cinco": "5", "seis": "6", "siete": "7", "ocho": "8", "nueve": "9",
	}
)

func containsAffirmation(text string) bool {
	toks := tokenize(text)
	for i, t := range toks {
		if affirmations[t] {
			return true
		}
		if t == "de" && i+1 < len(toks) && toks[i+1] == "acuerdo" {
			return true
		}
	}
	return false
}

func containsNegation(text string) bool {
	for _, t := range tokenize(text) {
		if negations[t] {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// containsCode looks for code in runs of consecutive digit tokens/digit words.
func containsCode(text, code string) bool {
	if code == "" {
		return false
	}
	var run strings.Builder
	for _, t := range tokenize(text) {
		if d, ok := digitWords[t]; ok {
			run.WriteString(d)
		} else if allDigits(t) {
			run.WriteString(t)
		} else {
			if strings.Contains(run.String(), code) {
				return true
			}
			run.Reset()
		}
	}
	return strings.Contains(run.String(), code)
}
