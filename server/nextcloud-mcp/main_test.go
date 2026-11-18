package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"nextcloud-mcp/nctest"
)

func TestRunMissingEnvExits(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(func(string) string { return "" }, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	if !strings.Contains(errb.String(), "NEXTCLOUD_URL") {
		t.Fatalf("stderr = %q", errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout not empty: %q", out.String())
	}
}

func TestRunListsTools(t *testing.T) {
	f := nctest.New(t, "user")
	env := map[string]string{"NEXTCLOUD_URL": f.URL, "NEXTCLOUD_USER": "user", "NEXTCLOUD_PASSWORD": f.Pass, "TZ": "America/Santiago"}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"
	var out, errb bytes.Buffer
	if code := run(func(k string) string { return env[k] }, strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
	want := []string{"bitacora_anotar", "bitacora_corregir", "bitacora_leer", "resumen_del_dia", "agenda",
		"evento_crear", "evento_cancelar", "tareas_pendientes", "tarea_crear", "tarea_completar",
		"tarea_anotar", "tarea_cambiar", "tarea_borrar", "nota_guardar", "notas_buscar",
		"estado_proyecto_anotar", "estado_proyecto_leer", "archivo_leer", "confirmar"}
	got := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m struct {
			ID     int `json:"id"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		for _, tl := range m.Result.Tools {
			got[tl.Name] = true
		}
	}
	if len(got) != 19 {
		t.Fatalf("got %d tools: %v", len(got), got)
	}
	for _, n := range want {
		if !got[n] {
			t.Errorf("missing tool %s", n)
		}
	}
}
