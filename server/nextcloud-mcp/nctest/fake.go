// Package nctest provides an in-memory fake Nextcloud for tests.
//
// It is a normal package so tests in other packages (nc, tools, ...) can use
// it. Later tasks extend it with CalDAV and Notes by registering extra routes
// with Handle.
package nctest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fake is a fake Nextcloud server. Tests may read and write Files and Dirs
// directly while no request is in flight.
type Fake struct {
	URL  string
	User string
	Pass string // expected basic-auth password

	Files map[string][]byte // key: path relative to the user root, e.g. "/Notas/a.md"
	Dirs  map[string]bool   // folders, e.g. "/Notas"

	// Observability for tests.
	PutCount      int
	LastPutHeader http.Header

	mu        sync.Mutex
	srv       *httptest.Server
	failPuts  []int
	mutations []mutation
	routes    []route
	modified  time.Time
}

type mutation struct {
	path string
	body []byte
}

type route struct {
	prefix string
	h      http.HandlerFunc
}

// New starts a fake server for user and registers cleanup on t.
func New(t testing.TB, user string) *Fake {
	t.Helper()
	f := &Fake{
		User:     user,
		Pass:     "secret",
		Files:    map[string][]byte{},
		Dirs:     map[string]bool{},
		modified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

// Close stops the server early (safe to call twice with the test cleanup).
func (f *Fake) Close() { f.srv.Close() }

// Handle registers h for request paths starting with prefix. Handlers run
// after authentication and before the built-in WebDAV routes; they are
// called with the Fake's lock NOT held.
func (f *Fake) Handle(prefix string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = append(f.routes, route{prefix, h})
}

// FailNextPut makes the next PUT (one per call, queued) answer with code.
func (f *Fake) FailNextPut(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPuts = append(f.failPuts, code)
}

// MutateBeforeNextPut simulates a concurrent write: just before the next
// PUT is evaluated (one mutation per PUT, queued), Files[path] is set to body.
func (f *Fake) MutateBeforeNextPut(path string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutations = append(f.mutations, mutation{path, body})
}

// ETag returns the quoted content hash of a file ("" if absent).
func (f *Fake) ETag(p string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.etagLocked(p)
}

func (f *Fake) etagLocked(p string) string {
	b, ok := f.Files[p]
	if !ok {
		return ""
	}
	return etagOf(b)
}

func etagOf(b []byte) string {
	h := sha256.Sum256(b)
	return `"` + hex.EncodeToString(h[:8]) + `"`
}

func (f *Fake) filesPrefix() string { return "/remote.php/dav/files/" + url.PathEscape(f.User) }

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != f.User || p != f.Pass {
		w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	routes := append([]route(nil), f.routes...)
	f.mu.Unlock()
	for _, rt := range routes {
		if strings.HasPrefix(r.URL.Path, rt.prefix) {
			rt.h(w, r)
			return
		}
	}
	switch {
	case r.Method == "SEARCH" && strings.HasPrefix(r.URL.Path, "/remote.php/dav"):
		f.search(w, r)
	case strings.HasPrefix(r.URL.Path, f.filesPrefix()):
		rel := strings.TrimPrefix(r.URL.Path, f.filesPrefix())
		if rel == "" {
			rel = "/"
		}
		f.webdav(w, r, strings.TrimRight(rel, "/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *Fake) webdav(w http.ResponseWriter, r *http.Request, rel string) {
	if rel == "" {
		rel = "/"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		b, ok := f.Files[rel]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etagOf(b))
		w.Write(b)
	case http.MethodPut:
		f.put(w, r, rel)
	case "MKCOL":
		if f.Dirs[rel] {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if parent := path.Dir(rel); parent != "/" && !f.Dirs[parent] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.Dirs[rel] = true
		w.WriteHeader(http.StatusCreated)
	case "PROPFIND":
		f.propfind(w, rel)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *Fake) put(w http.ResponseWriter, r *http.Request, rel string) {
	f.PutCount++
	f.LastPutHeader = r.Header.Clone()
	body, _ := io.ReadAll(r.Body)
	if len(f.failPuts) > 0 {
		code := f.failPuts[0]
		f.failPuts = f.failPuts[1:]
		w.WriteHeader(code)
		return
	}
	if len(f.mutations) > 0 {
		m := f.mutations[0]
		f.mutations = f.mutations[1:]
		f.Files[m.path] = m.body
	}
	cur, exists := f.Files[rel]
	if r.Header.Get("If-None-Match") == "*" && exists {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if im := r.Header.Get("If-Match"); im != "" && (!exists || etagOf(cur) != im) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	f.Files[rel] = body
	w.Header().Set("ETag", etagOf(body))
	if exists {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

type item struct {
	path string
	dir  bool
	size int
}

func (f *Fake) propfind(w http.ResponseWriter, rel string) {
	var items []item
	if rel != "/" && !f.Dirs[rel] {
		if b, ok := f.Files[rel]; ok {
			items = append(items, item{rel, false, len(b)})
		} else {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	} else {
		items = append(items, item{rel, true, 0})
		for p := range f.Dirs {
			if path.Dir(p) == path.Clean(rel) && p != rel {
				items = append(items, item{p, true, 0})
			}
		}
		for p, b := range f.Files {
			if path.Dir(p) == path.Clean(rel) {
				items = append(items, item{p, false, len(b)})
			}
		}
	}
	f.writeMultistatus(w, items)
}

var likeRe = regexp.MustCompile(`<(?:\w+:)?literal>(.*?)</(?:\w+:)?literal>`)
var limitRe = regexp.MustCompile(`<(?:\w+:)?nresults>(\d+)</(?:\w+:)?nresults>`)

func (f *Fake) search(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	m := likeRe.FindSubmatch(raw)
	if m == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var needle string
	{
		var v struct {
			S string `xml:",chardata"`
		}
		if err := xml.Unmarshal([]byte("<x>"+string(m[1])+"</x>"), &v); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		needle = strings.ToLower(strings.Trim(v.S, "%"))
	}
	limit := 1 << 30
	if lm := limitRe.FindSubmatch(raw); lm != nil {
		fmt.Sscanf(string(lm[1]), "%d", &limit)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []item
	for p, b := range f.Files {
		if strings.Contains(strings.ToLower(path.Base(p)), needle) {
			items = append(items, item{p, false, len(b)})
		}
	}
	for p := range f.Dirs {
		if strings.Contains(strings.ToLower(path.Base(p)), needle) {
			items = append(items, item{p, true, 0})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	if len(items) > limit {
		items = items[:limit]
	}
	f.writeMultistatus(w, items)
}

func (f *Fake) writeMultistatus(w http.ResponseWriter, items []item) {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
	for _, it := range items {
		href := f.filesPrefix() + escape(it.path)
		rt, size := "", fmt.Sprintf("<d:getcontentlength>%d</d:getcontentlength>", it.size)
		if it.dir {
			href += "/"
			if it.path == "/" {
				href = f.filesPrefix() + "/"
			}
			rt, size = "<d:collection/>", ""
		}
		fmt.Fprintf(&sb, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype>%s</d:resourcetype>%s<d:getlastmodified>%s</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
			href, rt, size, f.modified.Format(http.TimeFormat))
	}
	sb.WriteString(`</d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, sb.String())
}

func escape(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
