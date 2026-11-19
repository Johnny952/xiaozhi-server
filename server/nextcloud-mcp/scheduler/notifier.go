package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Aviso is the short alert shown on the device.
type Aviso struct{ Titulo, Texto string }

// Notifier delivers an Aviso. ErrNotConnected means the device is not there
// right now and the caller should retry later.
type Notifier interface {
	Notify(ctx context.Context, a Aviso) error
}

// ErrNotConnected is what the push API's 404 maps to.
var ErrNotConnected = errors.New("dispositivo no conectado")

// HTTPNotifier posts alerts to the gateway's POST /push.
type HTTPNotifier struct {
	URL, Token, MAC string
	Client          *http.Client
}

type pushBody struct {
	MAC     string      `json:"mac"`
	Payload pushPayload `json:"payload"`
}

type pushPayload struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Emotion string `json:"emotion"`
}

// Notify sends the alert. The token is never included in returned errors.
func (n HTTPNotifier) Notify(ctx context.Context, a Aviso) error {
	body, err := json.Marshal(pushBody{MAC: n.MAC, Payload: pushPayload{
		Type: "alert", Status: a.Titulo, Message: a.Texto, Emotion: "thinking",
	}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+n.Token)
	c := n.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("push: %w", errors.Unwrap(err))
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotConnected
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("push: estado HTTP %d", resp.StatusCode)
	}
	return nil
}
