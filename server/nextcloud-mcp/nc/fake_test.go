package nc_test

import (
	"net/http"
	"testing"

	"nextcloud-mcp/nctest"
)

func TestFakeEtagIsQuotedContentHash(t *testing.T) {
	f := nctest.New(t, "ana")
	f.Files["/a"] = []byte("hola")
	e1 := f.ETag("/a")
	if len(e1) < 3 || e1[0] != '"' || e1[len(e1)-1] != '"' {
		t.Fatalf("etag %q not quoted", e1)
	}
	f.Files["/a"] = []byte("chau")
	if f.ETag("/a") == e1 {
		t.Fatal("etag must change with content")
	}
}

func TestFakeRejectsBadAuth(t *testing.T) {
	f := nctest.New(t, "ana")
	resp, err := http.Get(f.URL + "/remote.php/dav/files/ana/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
