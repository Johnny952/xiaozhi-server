package mcp

import "testing"

func TestGetAvailableToolsOcultaPendientes(t *testing.T) {
	c := &Client{tools: []Tool{{Name: "recordatorio_crear"}, {Name: "recordatorios_pendientes"}}}
	got := c.GetAvailableTools()
	if len(got) != 1 || got[0].Function.Name != "mcp_recordatorio_crear" {
		t.Fatalf("unexpected tools: %+v", got)
	}
	if !c.HasTool("recordatorios_pendientes") || !c.HasTool("mcp_recordatorios_pendientes") {
		t.Fatal("hidden tool must still be callable")
	}
}
