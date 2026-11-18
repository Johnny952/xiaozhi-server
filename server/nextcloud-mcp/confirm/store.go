// Package confirm implements the two-step confirmation store for destructive tools.
package confirm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// TTL is how long an issued code stays valid.
const TTL = 120 * time.Second

const maxRetries = 100

var (
	ErrUnknown = errors.New("codigo desconocido")
	ErrExpired = errors.New("codigo vencido")
)

// Pending is an action awaiting user confirmation.
type Pending struct {
	Code, Action, Summary, Href, ETag string
	Params                            map[string]string
	IssuedAt                          time.Time
}

// Store holds pending confirmations. Safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	now   func() time.Time
	rnd   io.Reader
	items map[string]Pending
}

// New builds a Store; production passes time.Now and crypto/rand.Reader.
func New(now func() time.Time, rnd io.Reader) *Store {
	return &Store{now: now, rnd: rnd, items: map[string]Pending{}}
}

func (s *Store) purge(now time.Time) {
	for k, p := range s.items {
		if now.Sub(p.IssuedAt) > TTL {
			delete(s.items, k)
		}
	}
}

// Issue stores p under a fresh unique 4-digit code and returns it.
func (s *Store) Issue(p Pending) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.purge(now)
	for i := 0; i < maxRetries; i++ {
		var b [2]byte
		if _, err := io.ReadFull(s.rnd, b[:]); err != nil {
			return "", fmt.Errorf("generando codigo: %w", err)
		}
		code := fmt.Sprintf("%04d", binary.BigEndian.Uint16(b[:])%10000)
		if _, taken := s.items[code]; taken {
			continue
		}
		p.Code = code
		p.IssuedAt = now
		s.items[code] = p
		return code, nil
	}
	return "", errors.New("no se pudo generar un codigo unico")
}

// Take removes and returns the pending item for code (single use).
func (s *Store) Take(code string) (Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[code]
	if !ok {
		return Pending{}, ErrUnknown
	}
	delete(s.items, code)
	if s.now().Sub(p.IssuedAt) > TTL {
		return Pending{}, ErrExpired
	}
	return p, nil
}

// Prompt builds the instruction returned to the LLM.
func Prompt(summary, code string) string {
	return fmt.Sprintf(`PENDIENTE: pregunta al usuario "%s". Solo si responde que sí en su próximo mensaje, llama confirmar con codigo=%s.`, summary, code)
}
