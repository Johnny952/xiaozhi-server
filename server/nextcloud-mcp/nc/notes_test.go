package nc_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"nextcloud-mcp/nc"
)

func TestNotesHeaderAndIfMatch(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()

	// Sin OCS-APIRequest (pero autenticado) el fake responde 400.
	req, _ := http.NewRequest("GET", f.URL+"/index.php/apps/notes/api/v1/notes", nil)
	req.SetBasicAuth("ana", f.Pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("authed status without header = %d, want 400", resp.StatusCode)
	}

	n, err := c.CreateNote(ctx, "Ideas", "uno")
	if err != nil || n.ID == 0 || n.Title != "Ideas" || n.ETag == "" {
		t.Fatalf("CreateNote = %+v %v", n, err)
	}
	list, err := c.Notes(ctx)
	if err != nil || len(list) != 1 || list[0].ID != n.ID {
		t.Fatalf("Notes = %+v %v", list, err)
	}
	got, err := c.Note(ctx, n.ID)
	if err != nil || got.Content != "uno" {
		t.Fatalf("Note = %+v %v", got, err)
	}
	if _, err := c.Note(ctx, 999); !errors.Is(err, nc.ErrNotFound) {
		t.Fatalf("missing note err = %v", err)
	}

	if err := c.UpdateNoteContent(ctx, got, "uno\ndos"); err != nil {
		t.Fatal(err)
	}
	if f.Note(n.ID).Content != "uno\ndos" {
		t.Fatalf("content = %q", f.Note(n.ID).Content)
	}
	// got ya está viejo: ETag cambió.
	if err := c.UpdateNoteContent(ctx, got, "x"); !errors.Is(err, nc.ErrConflict) {
		t.Fatalf("stale etag err = %v, want ErrConflict", err)
	}
	if f.Note(n.ID).Content != "uno\ndos" {
		t.Fatal("stale update must not write")
	}
}

func TestUpdateNoteModifiedMismatchConflicts(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()
	n, _ := c.CreateNote(ctx, "T", "a")
	// Cambio concurrente que un servidor sin If-Match no detectaría por ETag.
	f.Note(n.ID).Modified += 10
	if err := c.UpdateNoteContent(ctx, n, "b"); !errors.Is(err, nc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if f.Note(n.ID).Content != "a" {
		t.Fatal("must not write on Modified mismatch")
	}
}
