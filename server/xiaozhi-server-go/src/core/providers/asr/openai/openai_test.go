package openai

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"xiaozhi-server-go/src/core/providers/asr"
	"xiaozhi-server-go/src/core/utils"
)

type result struct {
	text  string
	final bool
}

type fakeListener struct{ ch chan result }

func newListener() *fakeListener { return &fakeListener{ch: make(chan result, 4)} }

func (l *fakeListener) OnAsrResult(text string, final bool) bool {
	l.ch <- result{text, final}
	return true
}

func (l *fakeListener) wait(t *testing.T, d time.Duration) result {
	t.Helper()
	select {
	case r := <-l.ch:
		return r
	case <-time.After(d):
		t.Fatal("timeout waiting for listener")
		return result{}
	}
}

func testLogger(t *testing.T) *utils.Logger {
	t.Helper()
	lg, err := utils.NewLogger(&utils.LogCfg{LogLevel: "ERROR", LogDir: t.TempDir(), LogFile: "t.log"})
	if err != nil {
		t.Fatal(err)
	}
	return lg
}

func newTestProvider(t *testing.T, data map[string]interface{}) (*Provider, *fakeListener) {
	t.Helper()
	p, err := NewProvider(&asr.Config{Name: "openai", Data: data}, false, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	l := newListener()
	pr := p.(*Provider)
	pr.SetListener(l)
	return pr, l
}

func pcm(n int) []byte { return make([]byte, n) }

func TestPCMToWAVHeader(t *testing.T) {
	data := pcm(100)
	w := pcmToWAV(data, 16000, 1)
	if len(w) != 44+len(data) {
		t.Fatalf("len = %d", len(w))
	}
	if string(w[0:4]) != "RIFF" || string(w[8:12]) != "WAVE" || string(w[12:16]) != "fmt " || string(w[36:40]) != "data" {
		t.Fatal("bad tags")
	}
	if binary.LittleEndian.Uint32(w[24:28]) != 16000 {
		t.Fatal("bad sample rate")
	}
	if binary.LittleEndian.Uint16(w[34:36]) != 16 {
		t.Fatal("bad bits per sample")
	}
	if binary.LittleEndian.Uint32(w[40:44]) != 100 || binary.LittleEndian.Uint32(w[4:8]) != 136 {
		t.Fatal("bad sizes")
	}
}

func TestTranscribeSendsMultipart(t *testing.T) {
	var gotErr atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(s string) { gotErr.Store(s) }
		if r.URL.Path != "/v1/audio/transcriptions" || r.Method != http.MethodPost {
			fail("path/method " + r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sekret" {
			fail("auth header")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			fail(err.Error())
		} else {
			if r.FormValue("model") != "m1" || r.FormValue("language") != "es" ||
				r.FormValue("prompt") != "chileno" || r.FormValue("response_format") != "json" {
				fail("form fields")
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				fail(err.Error())
			} else {
				b, _ := io.ReadAll(f)
				if !strings.HasPrefix(string(b), "RIFF") || len(b) != 44+300 {
					fail("file content")
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "hola po"})
	}))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{
		"base_url": srv.URL, "api_key": "sekret", "model": "m1", "language": "es", "prompt": "chileno",
	})
	p.AddAudio(pcm(100))
	p.AddAudio(pcm(100))
	if err := p.SendLastAudio(pcm(100)); err != nil {
		t.Fatal(err)
	}
	r := l.wait(t, 3*time.Second)
	if r.text != "hola po" || !r.final {
		t.Fatalf("got %+v", r)
	}
	if e := gotErr.Load(); e != nil {
		t.Fatal(e)
	}
}

func TestSendLastAudioEmptyBufferNoRequest(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL})
	p.SendLastAudio([]byte{})
	if r := l.wait(t, time.Second); r.text != "" || !r.final {
		t.Fatalf("got %+v", r)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("unexpected request")
	}
}

func TestEmptyTranscriptReportsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL})
	p.SendLastAudio(pcm(10))
	if r := l.wait(t, 3*time.Second); r.text != "" || !r.final {
		t.Fatalf("got %+v", r)
	}
}

func TestTranscribeNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", 503)
	}))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL})
	p.SendLastAudio(pcm(10))
	if r := l.wait(t, 3*time.Second); r.text != "" || !r.final {
		t.Fatalf("got %+v", r)
	}
	if p.bufLen() != 0 {
		t.Fatal("buffer not reset")
	}
}

func TestTranscribeTimeout(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-done:
		}
	}))
	defer srv.Close()
	defer close(done)
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL, "timeout": 1})
	start := time.Now()
	p.SendLastAudio(pcm(10))
	if r := l.wait(t, 3*time.Second); r.text != "" || !r.final {
		t.Fatalf("got %+v", r)
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("too slow: %v", time.Since(start))
	}
}

func TestResetClearsBuffer(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL})
	p.AddAudio(pcm(50))
	p.Reset()
	p.SendLastAudio(nil)
	if r := l.wait(t, time.Second); r.text != "" || !r.final {
		t.Fatalf("got %+v", r)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("unexpected request")
	}
}

func TestNewProviderRequiresBaseURL(t *testing.T) {
	_, err := NewProvider(&asr.Config{Data: map[string]interface{}{}}, false, testLogger(t))
	if err == nil {
		t.Fatal("expected error")
	}
}

// tone returns ms of 16-bit LE PCM at 16 kHz with constant amplitude amp.
func tone(ms int, amp int16) []byte {
	n := ms * 16
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		v := amp
		if i%2 == 1 {
			v = -amp
		}
		b[2*i] = byte(uint16(v))
		b[2*i+1] = byte(uint16(v) >> 8)
	}
	return b
}

func feed(p *Provider, ms int, amp int16) {
	for i := 0; i < ms/60; i++ {
		p.AddAudio(tone(60, amp))
	}
}

func TestVADFinalizesAfterTrailingSilence(t *testing.T) {
	var gotBytes int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		f, _, _ := r.FormFile("file")
		if f != nil {
			n, _ := io.Copy(io.Discard, f)
			atomic.StoreInt64(&gotBytes, n)
		}
		w.Write([]byte(`{"text":"hola"}`))
	}))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL})
	feed(p, 1200, 0)   // leading silence: trimmed to the preroll
	feed(p, 600, 3000) // speech
	feed(p, 900, 0)    // trailing silence
	r := l.wait(t, 3*time.Second)
	if r.text != "hola" || !r.final {
		t.Fatalf("got %+v", r)
	}
	// WAV = 44 header + preroll(300ms) + speech(600ms) + silence up to 840ms
	if n := atomic.LoadInt64(&gotBytes); n > 44+2*16*(300+600+900) || n < 44+2*16*(600+800) {
		t.Fatalf("unexpected wav size %d", n)
	}
	// Chunks after the endpoint start a fresh utterance; only preroll may remain.
	if p.bufLen() > 2*16*300 {
		t.Fatalf("buffer not taken: %d", p.bufLen())
	}
}

func TestVADIgnoresShortNoise(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL})
	feed(p, 120, 3000) // click shorter than min speech
	feed(p, 1200, 0)
	select {
	case r := <-l.ch:
		t.Fatalf("unexpected result %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("unexpected request")
	}
}

func TestVADSilenceOnlyNeverFinalizes(t *testing.T) {
	p, l := newTestProvider(t, map[string]interface{}{"base_url": "http://127.0.0.1:1"})
	feed(p, 3000, 100)
	select {
	case r := <-l.ch:
		t.Fatalf("unexpected result %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
	if p.bufLen() > 2*16*300 {
		t.Fatalf("preroll not trimmed: %d", p.bufLen())
	}
}

func TestVADMaxUtteranceForcesFinalize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":"largo"}`))
	}))
	defer srv.Close()
	p, l := newTestProvider(t, map[string]interface{}{"base_url": srv.URL, "vad_max_ms": 1000})
	feed(p, 1500, 3000)
	if r := l.wait(t, 3*time.Second); r.text != "largo" {
		t.Fatalf("got %+v", r)
	}
}

func TestVADDisabledKeepsBuffering(t *testing.T) {
	p, l := newTestProvider(t, map[string]interface{}{"base_url": "http://127.0.0.1:1", "vad_enabled": false})
	feed(p, 600, 3000)
	feed(p, 1200, 0)
	select {
	case r := <-l.ch:
		t.Fatalf("unexpected result %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
	if p.bufLen() != 2*16*(600+1200) {
		t.Fatalf("buffer %d", p.bufLen())
	}
}
