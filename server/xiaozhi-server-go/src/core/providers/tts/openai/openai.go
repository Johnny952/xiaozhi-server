// Package openai implements a TTS provider that POSTs to an OpenAI-compatible
// /v1/audio/speech endpoint (e.g. a local Speaches container serving Piper).
// Output is always mp3.
package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xiaozhi-server-go/src/core/providers/tts"
)

const (
	defaultModel   = "speaches-ai/piper-es_MX-claude-high"
	defaultVoice   = "es_MX-claude-high"
	defaultSpeed   = 1.0
	defaultTimeout = 20
	// Responses larger than this are rejected (guards memory).
	maxAudioBytes = 32 << 20
)

// Provider is an OpenAI-compatible TTS provider.
type Provider struct {
	*tts.BaseProvider
	client *http.Client
}

// NewProvider creates the provider.
func NewProvider(config *tts.Config, deleteFile bool) (tts.Provider, error) {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Provider{
		BaseProvider: tts.NewBaseProvider(config, deleteFile),
		client:       &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}, nil
}

func (p *Provider) endpoint() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(p.Config().BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("openai tts: base_url is required")
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/audio/speech", nil
	}
	return base + "/v1/audio/speech", nil
}

// ToTTS synthesizes text and returns the path of the generated mp3 file.
func (p *Provider) ToTTS(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("openai tts: empty text")
	}
	cfg := p.Config()
	url, err := p.endpoint()
	if err != nil {
		return "", err
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel
	}
	voice := cfg.Voice
	if voice == "" {
		voice = defaultVoice
	}
	speed := cfg.Speed
	if speed <= 0 {
		speed = defaultSpeed
	}
	// Always mp3: the audio pipeline resamples mp3 but not 22050 Hz WAV.
	body, err := json.Marshal(map[string]interface{}{
		"model":           model,
		"voice":           voice,
		"input":           text,
		"response_format": "mp3",
		"speed":           speed,
	})
	if err != nil {
		return "", fmt.Errorf("openai tts: encode request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai tts: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai tts: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("openai tts: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes+1))
	if err != nil {
		return "", fmt.Errorf("openai tts: read body: %w", err)
	}
	if len(audio) == 0 {
		return "", fmt.Errorf("openai tts: empty audio response")
	}
	if len(audio) > maxAudioBytes {
		return "", fmt.Errorf("openai tts: audio response too large")
	}

	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = os.TempDir()
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("openai tts: create output dir: %w", err)
	}
	path := filepath.Join(outputDir, fmt.Sprintf("openai_tts_%d.mp3", time.Now().UnixNano()))
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("openai tts: write file: %w", err)
	}
	return path, nil
}

func init() {
	tts.Register("openai", NewProvider)
}
