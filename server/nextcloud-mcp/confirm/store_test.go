package confirm

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newClock() *clock { return &clock{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)} }

func TestIssueFourDigitsUnique(t *testing.T) {
	c := newClock()
	// same 2 bytes repeated, then a different value
	rnd := bytes.NewReader([]byte{0x01, 0x02, 0x01, 0x02, 0x03, 0x04})
	s := New(c.now, rnd)
	a, err := s.Issue(Pending{Action: "x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Issue(Pending{Action: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 4 || len(b) != 4 {
		t.Fatalf("codes not 4 digits: %q %q", a, b)
	}
	if a == b {
		t.Fatalf("codes collide: %q", a)
	}
}

func TestIssueGivesUpOnEndlessCollision(t *testing.T) {
	c := newClock()
	rnd := bytes.NewReader(bytes.Repeat([]byte{1, 2}, 500))
	s := New(c.now, rnd)
	if _, err := s.Issue(Pending{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue(Pending{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestStoreExpiresAndSingleUse(t *testing.T) {
	c := newClock()
	rnd := bytes.NewReader([]byte{0, 1, 0, 2})
	s := New(c.now, rnd)
	a, _ := s.Issue(Pending{Action: "a", Params: map[string]string{"k": "v"}})
	b, _ := s.Issue(Pending{Action: "b"})
	c.t = c.t.Add(119 * time.Second)
	p, err := s.Take(a)
	if err != nil || p.Action != "a" || p.Code != a || p.Params["k"] != "v" {
		t.Fatalf("take a: %+v %v", p, err)
	}
	if _, err := s.Take(a); !errors.Is(err, ErrUnknown) {
		t.Fatalf("want ErrUnknown, got %v", err)
	}
	c.t = c.t.Add(2 * time.Second)
	if _, err := s.Take(b); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if _, err := s.Take(b); !errors.Is(err, ErrUnknown) {
		t.Fatalf("want ErrUnknown, got %v", err)
	}
}

func TestPromptExact(t *testing.T) {
	got := Prompt("borrar tarea X", "4821")
	want := `PENDIENTE: pregunta al usuario "borrar tarea X". Solo si responde que sí en su próximo mensaje, llama confirmar con codigo=4821.`
	if got != want {
		t.Fatalf("got %q", got)
	}
}
