package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"xiaozhi-server-go/src/core/chat"
)

type pendFixture struct {
	h      *ConnectionHandler
	spoken []string
	called string
}

func newPendFixture(res interface{}, err error, ready bool) *pendFixture {
	f := &pendFixture{}
	f.h = &ConnectionHandler{
		dialogueManager: chat.NewDialogueManager(nil, nil),
		stopChan:        make(chan struct{}),
	}
	f.h.pendientesExec = func(ctx context.Context, name string, a map[string]interface{}) (interface{}, error) {
		f.called = name
		return res, err
	}
	f.h.pendientesSpeak = func(s string) error { f.spoken = append(f.spoken, s); return nil }
	f.h.pendientesReady = func() bool { return ready }
	return f
}

func TestHelloPendientesHabla(t *testing.T) {
	f := newPendFixture("Tenías pendiente: pagar la luz.", nil, true)
	f.h.speakPendientes(context.Background())
	if f.called != "recordatorios_pendientes" {
		t.Fatalf("called %q", f.called)
	}
	if len(f.spoken) != 1 || f.spoken[0] != "Tenías pendiente: pagar la luz." {
		t.Fatalf("spoken %v", f.spoken)
	}
	d := f.h.dialogueManager.GetLLMDialogue()
	if len(d) != 1 || d[0].Role != "assistant" {
		t.Fatalf("dialogue %+v", d)
	}
}

func TestHelloPendientesVacioNoHabla(t *testing.T) {
	for _, r := range []interface{}{"", "  \n", nil} {
		f := newPendFixture(r, nil, true)
		f.h.speakPendientes(context.Background())
		if len(f.spoken) != 0 || len(f.h.dialogueManager.GetLLMDialogue()) != 0 {
			t.Fatalf("must be silent for %v", r)
		}
	}
}

func TestHelloPendientesToolErrorIsSilent(t *testing.T) {
	f := newPendFixture(nil, errors.New("boom"), true)
	f.h.speakPendientes(context.Background())
	if len(f.spoken) != 0 || len(f.h.dialogueManager.GetLLMDialogue()) != 0 {
		t.Fatal("error must be silent")
	}
}

func TestHelloPendientesNotReadyIsSilent(t *testing.T) {
	f := newPendFixture("algo", nil, false)
	start := time.Now()
	f.h.speakPendientes(context.Background())
	if time.Since(start) > 3*time.Second {
		t.Fatal("waited too long")
	}
	if f.called != "" || len(f.spoken) != 0 {
		t.Fatal("not ready must neither call nor speak")
	}
}

func TestHelloPendientesClosedConnectionStops(t *testing.T) {
	f := newPendFixture("algo", nil, false)
	close(f.h.stopChan)
	start := time.Now()
	f.h.speakPendientes(context.Background())
	if time.Since(start) > time.Second {
		t.Fatal("must stop on close")
	}
}
