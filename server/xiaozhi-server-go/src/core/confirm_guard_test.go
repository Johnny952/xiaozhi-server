package core

import "testing"

const pend = "PENDIENTE: borrar archivo. Pide confirmación. codigo=4821"

func armed(t *testing.T, utt string) *confirmGuard {
	t.Helper()
	g := &confirmGuard{}
	g.OnToolResult(pend)
	g.OnUserUtterance(utt)
	return g
}

func args(c interface{}) map[string]interface{} { return map[string]interface{}{"codigo": c} }

func TestConfirmGuardAffirmationEnablesOnce(t *testing.T) {
	g := armed(t, "sí, dale")
	if !g.Authorize("mcp_confirmar", args("4821")) {
		t.Fatal("expected authorized")
	}
	if g.Authorize("mcp_confirmar", args("4821")) {
		t.Fatal("second use must fail")
	}
}

func TestConfirmGuardSelfConfirmRejected(t *testing.T) {
	g := &confirmGuard{}
	g.OnToolResult(pend)
	if g.Authorize("mcp_confirmar", args("4821")) {
		t.Fatal("self confirm must be rejected")
	}
}

func TestConfirmGuardNegationBlocks(t *testing.T) {
	for _, u := range []string{"no, no lo borres", "ok no", "mejor no, espera"} {
		g := armed(t, u)
		if g.Authorize("mcp_confirmar", args("4821")) {
			t.Fatalf("%q must block", u)
		}
	}
	g := armed(t, "no sé, 4821")
	if !g.Authorize("mcp_confirmar", args("4821")) {
		t.Fatal("explicit code should authorize")
	}
}

func TestConfirmGuardSpokenDigits(t *testing.T) {
	for _, u := range []string{"cuatro ocho dos uno", "48 21", "4-8-2-1"} {
		g := armed(t, u)
		if !g.Authorize("mcp_confirmar", args("4821")) {
			t.Fatalf("%q must authorize", u)
		}
	}
}

func TestConfirmGuardOnlyNextUtterance(t *testing.T) {
	g := &confirmGuard{}
	g.OnToolResult(pend)
	g.OnUserUtterance("qué hora es")
	g.OnUserUtterance("sí")
	if g.Authorize("mcp_confirmar", args("4821")) {
		t.Fatal("must fail")
	}
}

func TestConfirmGuardWrongCode(t *testing.T) {
	g := armed(t, "sí")
	if g.Authorize("mcp_confirmar", args("1234")) {
		t.Fatal("wrong code")
	}
}

func TestConfirmGuardNumericCode(t *testing.T) {
	g := armed(t, "sí")
	if !g.Authorize("mcp_confirmar", args(float64(4821))) {
		t.Fatal("float64 4821")
	}
	for _, c := range []interface{}{float64(48.21), "04821", nil} {
		g := armed(t, "sí")
		if g.Authorize("mcp_confirmar", args(c)) {
			t.Fatalf("%v must fail", c)
		}
	}
	g = armed(t, "sí")
	if g.Authorize("mcp_confirmar", map[string]interface{}{}) {
		t.Fatal("missing key")
	}
	if g.Authorize("mcp_confirmar", nil) {
		t.Fatal("nil args")
	}
}

func TestConfirmGuardNonConfirmToolsPass(t *testing.T) {
	g := &confirmGuard{}
	if !g.Authorize("mcp_agenda", nil) {
		t.Fatal("non confirm tool must pass")
	}
}

func TestConfirmGuardEmptyUtteranceIgnored(t *testing.T) {
	g := &confirmGuard{}
	g.OnToolResult(pend)
	g.OnUserUtterance("")
	g.OnUserUtterance("   ")
	g.OnUserUtterance("sí")
	if !g.Authorize("mcp_confirmar", args("4821")) {
		t.Fatal("should authorize")
	}
}

func TestConfirmGuardNewPendingClearsEnable(t *testing.T) {
	g := armed(t, "sí")
	g.OnToolResult("PENDIENTE: otra cosa codigo=1111")
	if g.Authorize("mcp_confirmar", args("4821")) || g.Authorize("mcp_confirmar", args("1111")) {
		t.Fatal("new pending must clear enable")
	}
}
