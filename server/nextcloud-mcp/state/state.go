// Package state is the small JSON file shared by the MCP server (resumen_leido)
// and the scheduler (resumen_enviado). Each process writes only its own field;
// writes re-read the file under an exclusive flock and replace it atomically.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// State is the content of the file. Dates are YYYY-MM-DD.
type State struct {
	ResumenEnviado string `json:"resumen_enviado"`
	ResumenLeido   string `json:"resumen_leido"`
}

// Store is the state file at a path; the lock lives next to it.
type Store struct {
	path string
}

// Open returns a Store for path. Nothing is touched until Get or Set.
func Open(path string) *Store { return &Store{path: path} }

func (s *Store) lockPath() string {
	return filepath.Join(filepath.Dir(s.path), "recordatorios-state.lock")
}

func (s *Store) read() (State, error) {
	var st State
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("estado corrupto: %w", err)
	}
	return st, nil
}

// Get returns the current state; a missing file is the empty state.
func (s *Store) Get() (State, error) { return s.read() }

// Set changes one field ("resumen_enviado" or "resumen_leido") and keeps the
// other as found on disk.
func (s *Store) Set(field, value string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st, err := s.read()
	if err != nil {
		return err
	}
	switch field {
	case "resumen_enviado":
		st.ResumenEnviado = value
	case "resumen_leido":
		st.ResumenLeido = value
	default:
		return fmt.Errorf("campo de estado desconocido: %s", field)
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
