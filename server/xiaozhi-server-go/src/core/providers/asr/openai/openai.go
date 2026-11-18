// Package openai implements a batch ASR provider that POSTs WAV audio to an
// OpenAI-compatible /v1/audio/transcriptions endpoint (e.g. Speaches).
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"xiaozhi-server-go/src/core/providers/asr"
	"xiaozhi-server-go/src/core/utils"
)

var _ asr.Provider = (*Provider)(nil)

const (
	defaultModel    = "Systran/faster-whisper-small"
	defaultLanguage = "es"
	defaultTimeout  = 15
	defaultRate     = 16000

	// Endpointing defaults (energy VAD). The device runs listen mode "auto" and
	// never sends "listen stop", so the server must detect end of speech.
	defaultVADThreshold   = 500   // RMS of int16 samples counted as speech
	defaultVADSilenceMs   = 800   // trailing silence that ends an utterance
	defaultVADMinSpeechMs = 200   // speech needed before an utterance counts
	defaultVADMaxMs       = 15000 // force finalization of very long utterances
	defaultVADPrerollMs   = 300   // audio kept from before speech onset
)

// Provider accumulates PCM (16-bit LE, mono) and transcribes it in one request
// when the client stops listening or, with VAD enabled, when it detects end of
// speech (needed for listen mode "auto").
type Provider struct {
	*asr.BaseProvider

	baseURL    string
	apiKey     string
	model      string
	language   string
	prompt     string
	sampleRate int
	client     *http.Client
	logger     *utils.Logger

	vadEnabled   bool
	vadThreshold float64
	vadSilenceMs int
	vadMinSpeech int
	vadMaxMs     int
	vadPrerollMs int

	mu  sync.Mutex
	buf []byte
	// endpointing state, guarded by mu
	speechMs  int
	silenceMs int
	totalMs   int
}

func NewProvider(config *asr.Config, deleteFile bool, logger *utils.Logger) (asr.Provider, error) {
	base := asr.NewBaseProvider(config, deleteFile)

	baseURL := strings.TrimRight(strString(config.Data, "base_url", ""), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("missing base_url config for openai ASR")
	}
	timeout := intVal(config.Data, "timeout", defaultTimeout)
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	rate := intVal(config.Data, "sample_rate", defaultRate)
	if rate <= 0 {
		rate = defaultRate
	}

	p := &Provider{
		BaseProvider: base,
		baseURL:      baseURL,
		apiKey:       strString(config.Data, "api_key", ""),
		model:        strString(config.Data, "model", defaultModel),
		language:     strString(config.Data, "language", defaultLanguage),
		prompt:       strString(config.Data, "prompt", ""),
		sampleRate:   rate,
		client:       &http.Client{Timeout: time.Duration(timeout) * time.Second},
		logger:       logger,
		vadEnabled:   boolVal(config.Data, "vad_enabled", true),
		vadThreshold: float64(intVal(config.Data, "vad_threshold", defaultVADThreshold)),
		vadSilenceMs: intVal(config.Data, "vad_silence_ms", defaultVADSilenceMs),
		vadMinSpeech: intVal(config.Data, "vad_min_speech_ms", defaultVADMinSpeechMs),
		vadMaxMs:     intVal(config.Data, "vad_max_ms", defaultVADMaxMs),
		vadPrerollMs: intVal(config.Data, "vad_preroll_ms", defaultVADPrerollMs),
	}
	p.InitAudioProcessing()
	return p, nil
}

func strString(m map[string]interface{}, key, def string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func boolVal(m map[string]interface{}, key string, def bool) bool {
	if b, ok := m[key].(bool); ok {
		return b
	}
	return def
}

// intVal accepts int/int64/float64 (YAML and JSON decode to different types).
func intVal(m map[string]interface{}, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

// AddAudio appends PCM data to the pending buffer. With VAD enabled it also
// detects end of speech and finalizes the utterance on its own.
func (p *Provider) AddAudio(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	p.mu.Lock()
	p.buf = append(p.buf, data...)
	if !p.vadEnabled {
		p.mu.Unlock()
		return nil
	}
	chunkMs := len(data) * 1000 / (2 * p.sampleRate)
	if rms(data) >= p.vadThreshold {
		p.speechMs += chunkMs
		p.silenceMs = 0
	} else if p.speechMs > 0 {
		p.silenceMs += chunkMs
	}
	if p.speechMs > 0 {
		p.totalMs += chunkMs
	}
	finalize := false
	switch {
	case p.speechMs >= p.vadMinSpeech && p.silenceMs >= p.vadSilenceMs:
		finalize = true
	case p.speechMs >= p.vadMinSpeech && p.totalMs >= p.vadMaxMs:
		finalize = true
	case p.speechMs > 0 && p.speechMs < p.vadMinSpeech && p.silenceMs >= p.vadSilenceMs:
		// a click or short noise: drop it and keep waiting
		p.resetVADLocked()
	}
	if p.speechMs == 0 {
		// before speech onset keep only the preroll
		keep := p.vadPrerollMs * 2 * p.sampleRate / 1000
		if keep >= 0 && len(p.buf) > keep {
			p.buf = append(p.buf[:0:0], p.buf[len(p.buf)-keep:]...)
		}
	}
	p.mu.Unlock()
	if finalize {
		p.logger.Debug("openai ASR endpoint detected")
		return p.SendLastAudio(nil)
	}
	return nil
}

func (p *Provider) resetVADLocked() {
	p.speechMs, p.silenceMs, p.totalMs = 0, 0, 0
}

// rms returns the root mean square of 16-bit little-endian PCM samples.
func rms(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		v := float64(int16(uint16(pcm[2*i]) | uint16(pcm[2*i+1])<<8))
		sum += v * v
	}
	return math.Sqrt(sum / float64(n))
}

func (p *Provider) bufLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buf)
}

func (p *Provider) takeBuffer() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.buf
	p.buf = nil
	p.resetVADLocked()
	return b
}

// SendLastAudio finalizes the utterance. Transcription runs in a goroutine so
// the message loop is not blocked; the result goes to the listener.
func (p *Provider) SendLastAudio(data []byte) error {
	if err := p.AddAudio(data); err != nil {
		return err
	}
	pcm := p.takeBuffer()
	listener := p.GetListener()

	go func() {
		text := ""
		if len(pcm) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), p.client.Timeout)
			defer cancel()
			var err error
			text, err = p.Transcribe(ctx, pcm)
			if err != nil {
				p.logger.Error("openai ASR transcription failed: %v", err)
				text = ""
			}
		}
		if text == "" && p.SilenceTime() > 30*time.Second {
			p.SilenceCount++
		} else {
			p.SilenceCount = 0
		}
		if listener != nil {
			listener.OnAsrResult(text, true)
		}
	}()
	return nil
}

// Transcribe sends PCM audio as WAV and returns the recognized text.
func (p *Provider) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(pcmToWAV(pcm, p.sampleRate, 1)); err != nil {
		return "", err
	}
	fields := map[string]string{
		"model":           p.model,
		"language":        p.language,
		"response_format": "json",
	}
	if p.prompt != "" {
		fields["prompt"] = p.prompt
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Body is not logged: it may echo request details.
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	text := strings.TrimSpace(out.Text)
	p.logger.Debug("openai ASR result: %s", text)
	return text, nil
}

// Reset discards any buffered audio.
func (p *Provider) Reset() error {
	p.takeBuffer()
	p.InitAudioProcessing()
	return nil
}

func (p *Provider) Initialize() error { return nil }

// CloseConnection is a no-op: this provider holds no long-lived connection.
// Required by providers.ASRProvider; without it the manager's type assertion
// panics on every device connect.
func (p *Provider) CloseConnection() error { return nil }

func (p *Provider) Cleanup() error {
	p.takeBuffer()
	return nil
}

func init() {
	asr.Register("openai", NewProvider)
}
