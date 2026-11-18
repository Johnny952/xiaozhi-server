// Package nc is a small Nextcloud WebDAV client.
//
// Credentials are only used for HTTP basic auth; they never appear in
// returned errors or URLs.
package nc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("nextcloud: not found")
	// ErrConflict means a precondition (If-Match / If-None-Match) failed with 412.
	ErrConflict = errors.New("nextcloud: conflict (precondition failed)")
	// ErrCollision means MKCALENDAR hit an existing collection (405/409).
	ErrCollision = errors.New("nextcloud: collection already exists")
	ErrLocked    = errors.New("nextcloud: resource locked")
)

// Client talks to one Nextcloud instance as one user.
type Client struct {
	HTTP     *http.Client
	LockWait time.Duration

	base string // scheme://host[/prefix], no userinfo, no trailing slash
	user string
	pass string
}

// New builds a client. Any userinfo embedded in baseURL is discarded.
func New(baseURL, user, pass string) *Client {
	base := strings.TrimRight(baseURL, "/")
	if u, err := url.Parse(base); err == nil {
		u.User = nil
		base = strings.TrimRight(u.String(), "/")
	}
	return &Client{
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		LockWait: 2 * time.Second,
		base:     base,
		user:     user,
		pass:     pass,
	}
}

// filesRoot is the URL path (below base) of the user's file tree.
func (c *Client) filesRoot() string {
	return "/remote.php/dav/files/" + url.PathEscape(c.user)
}

// escapePath escapes each segment of a /-separated path.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// do sends one request. Network errors are stripped of the URL so nothing
// sensitive can leak; status mapping is left to the caller via statusErr.
func (c *Client) do(ctx context.Context, method, urlPath string, body []byte, hdr map[string]string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+urlPath, rd)
	if err != nil {
		return nil, fmt.Errorf("nextcloud: build %s request: invalid URL", method)
	}
	req.SetBasicAuth(c.user, c.pass)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("nextcloud: %s failed: %w", method, redact(err, c.pass))
	}
	return resp, nil
}

// redact guards against a password showing up in a lower-level error text.
func redact(err error, pass string) error {
	if pass != "" && strings.Contains(err.Error(), pass) {
		return errors.New("network error")
	}
	return err
}

func statusErr(method, p string, code int) error {
	switch code {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, p)
	case http.StatusPreconditionFailed:
		return fmt.Errorf("%w: %s", ErrConflict, p)
	case http.StatusLocked:
		return fmt.Errorf("%w: %s", ErrLocked, p)
	}
	return fmt.Errorf("nextcloud: %s %s: unexpected status %d", method, p, code)
}

func cleanETag(s string) string { return strings.TrimPrefix(s, "W/") }
