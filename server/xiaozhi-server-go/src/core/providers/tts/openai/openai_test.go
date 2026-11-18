package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"xiaozhi-server-go/src/core/providers/tts"
)

var fakeMP3 = []byte{0xFF, 0xFB, 0x90, 0x00, 1, 2, 3, 4}

func newProv(t *testing.T, cfg *tts.Config) (*Provider, string) {
	t.Helper()
	dir := t.TempDir()
	cfg.OutputDir = dir
	p, err := NewProvider(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Provider), dir
}

func TestToTTSPostsSpeechRequest(t *testing.T) {
	var got map[string]interface{}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
			t.Errorf("bad request %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Write(fakeMP3)
	}))
	defer srv.Close()
	p, dir := newProv(t, &tts.Config{BaseURL: srv.URL + "/v1", Model: "m", Voice: "v", Speed: 1, ResponseFormat: "mp3"})
	path, err := p.ToTTS("hola")
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != "m" || got["voice"] != "v" || got["input"] != "hola" || got["response_format"] != "mp3" || got["speed"] != float64(1) {
		t.Fatalf("bad body: %v", got)
	}
	if auth != "" {
		t.Fatalf("unexpected auth %q", auth)
	}
	if filepath.Dir(path) != dir || !strings.HasSuffix(path, ".mp3") {
		t.Fatalf("bad path %s", path)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(fakeMP3) {
		t.Fatal("content mismatch")
	}
}

func TestToTTSSendsBearerWhenKeySet(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write(fakeMP3)
	}))
	defer srv.Close()
	p, _ := newProv(t, &tts.Config{BaseURL: srv.URL, APIKey: "k"})
	if _, err := p.ToTTS("hola"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" {
		t.Fatalf("auth=%q", auth)
	}
}

func TestToTTSForcesMP3(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Write(fakeMP3)
	}))
	defer srv.Close()
	p, _ := newProv(t, &tts.Config{BaseURL: srv.URL, ResponseFormat: "wav"})
	if _, err := p.ToTTS("hola"); err != nil {
		t.Fatal(err)
	}
	if got["response_format"] != "mp3" {
		t.Fatalf("format=%v", got["response_format"])
	}
}

func TestToTTSNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	p, dir := newProv(t, &tts.Config{BaseURL: srv.URL})
	if _, err := p.ToTTS("hola"); err == nil {
		t.Fatal("expected error")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("leftover files: %v", ents)
	}
}

func TestToTTSEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	p, dir := newProv(t, &tts.Config{BaseURL: srv.URL})
	if _, err := p.ToTTS("hola"); err == nil {
		t.Fatal("expected error")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("leftover files: %v", ents)
	}
}

func TestToTTSTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write(fakeMP3)
	}))
	defer srv.Close()
	p, _ := newProv(t, &tts.Config{BaseURL: srv.URL, Timeout: 1})
	start := time.Now()
	if _, err := p.ToTTS("hola"); err == nil {
		t.Fatal("expected error")
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}

func TestToTTSEmptyText(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	p, _ := newProv(t, &tts.Config{BaseURL: srv.URL})
	for _, s := range []string{"", "   \n"} {
		if _, err := p.ToTTS(s); err == nil {
			t.Fatalf("expected error for %q", s)
		}
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("request was made")
	}
}
