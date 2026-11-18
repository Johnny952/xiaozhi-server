package nc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const notesBase = "/index.php/apps/notes/api/v1/notes"

// Note is a Nextcloud Notes entry. Modified is a unix timestamp (seconds).
type Note struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Category string `json:"category"`
	ETag     string `json:"etag"`
	Modified int64  `json:"modified"`
}

func (c *Client) notesDo(ctx context.Context, method, p string, in any, hdr map[string]string, out any) error {
	var body []byte
	h := map[string]string{"OCS-APIRequest": "true", "Accept": "application/json"}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = b
		h["Content-Type"] = "application/json"
	}
	for k, v := range hdr {
		h[k] = v
	}
	resp, err := c.do(ctx, method, p, body, h)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return statusErr(method, "notes", resp.StatusCode)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("nextcloud: parse notes response: %w", err)
	}
	return nil
}

// Notes lists all notes.
func (c *Client) Notes(ctx context.Context) ([]Note, error) {
	var out []Note
	if err := c.notesDo(ctx, http.MethodGet, notesBase, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Note fetches one note.
func (c *Client) Note(ctx context.Context, id int64) (Note, error) {
	var n Note
	err := c.notesDo(ctx, http.MethodGet, notesBase+"/"+strconv.FormatInt(id, 10), nil, nil, &n)
	return n, err
}

// CreateNote creates a note with the given title and content.
func (c *Client) CreateNote(ctx context.Context, title, content string) (Note, error) {
	var n Note
	err := c.notesDo(ctx, http.MethodPost, notesBase, map[string]string{"title": title, "content": content, "category": ""}, nil, &n)
	return n, err
}

// UpdateNoteContent replaces the content of n, but only if n is still the
// current version. Two guards are used:
//
//  1. Defensive fallback: the note is re-read right before the PUT and its
//     Modified is compared with n.Modified; a mismatch returns ErrConflict.
//     This is kept because it is not yet verified that the Notes API honours
//     If-Match (live verification against the real server is pending).
//  2. The PUT sends If-Match: n.ETag; a 412 returns ErrConflict.
func (c *Client) UpdateNoteContent(ctx context.Context, n Note, content string) error {
	cur, err := c.Note(ctx, n.ID)
	if err != nil {
		return err
	}
	if cur.Modified != n.Modified {
		return fmt.Errorf("%w: note %d changed since it was read", ErrConflict, n.ID)
	}
	etag := n.ETag
	if etag != "" && !strings.HasPrefix(etag, `"`) {
		etag = `"` + etag + `"`
	}
	hdr := map[string]string{}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	err = c.notesDo(ctx, http.MethodPut, notesBase+"/"+strconv.FormatInt(n.ID, 10),
		map[string]string{"content": content}, hdr, nil)
	if err != nil && errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: note %d changed since it was read", ErrConflict, n.ID)
	}
	return err
}
