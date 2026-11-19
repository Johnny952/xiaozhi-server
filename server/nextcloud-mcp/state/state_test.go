package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStateArchivoInexistente(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "recordatorios-state.json"))
	got, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got != (State{}) {
		t.Errorf("want empty state, got %+v", got)
	}
}

func TestStateSetPreservaOtroCampo(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "recordatorios-state.json"))
	if err := s.Set("resumen_enviado", "2026-11-18"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("resumen_leido", "2026-11-19"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got.ResumenEnviado != "2026-11-18" || got.ResumenLeido != "2026-11-19" {
		t.Errorf("got %+v", got)
	}
}

func TestStateCampoDesconocido(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "recordatorios-state.json"))
	if err := s.Set("otro", "x"); err == nil {
		t.Error("want error for unknown field")
	}
}

func TestStateConcurrente(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recordatorios-state.json")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Separate stores model the two processes sharing the file.
			s := Open(path)
			field := "resumen_enviado"
			if i%2 == 1 {
				field = "resumen_leido"
			}
			if err := s.Set(field, "2026-11-18"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("invalid JSON %q: %v", data, err)
	}
	if st.ResumenEnviado != "2026-11-18" || st.ResumenLeido != "2026-11-18" {
		t.Errorf("lost a field: %+v", st)
	}
}
