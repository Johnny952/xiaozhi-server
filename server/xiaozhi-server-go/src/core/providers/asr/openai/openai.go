// Package openai implements a batch ASR provider that POSTs WAV audio to an
// OpenAI-compatible /v1/audio/transcriptions endpoint (e.g. Speaches).
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
)

// Provider accumulates PCM (16-bit LE, mono) and transcribes it in one request
// when the client stops listening.
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

	mu  sync.Mutex
	buf []byte
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

// AddAudio appends PCM data to the pending buffer.
func (p *Provider) AddAudio(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	p.mu.Lock()
	p.buf = append(p.buf, data...)
	p.mu.Unlock()
	return nil
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
