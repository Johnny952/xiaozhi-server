package nc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetPrefixCapsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", 1<<20)))
	}))
	defer srv.Close()
	c := New(srv.URL, "u", "p")
	b, _, err := c.GetPrefix(context.Background(), "/x.txt", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1024 {
		t.Fatalf("len = %d", len(b))
	}
}
