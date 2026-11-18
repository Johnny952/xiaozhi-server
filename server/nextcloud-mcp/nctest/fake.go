// Package nctest provides an in-memory fake Nextcloud for tests.
//
// It is a normal package so tests in other packages (nc, tools, ...) can use
// it. Later tasks extend it with CalDAV and Notes by registering extra routes
// with Handle.
package nctest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
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
	cs        calState
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
	case strings.HasPrefix(r.URL.Path, f.calHome()) || r.URL.Path+"/" == f.calHome():
		f.caldav(w, r)
	case strings.HasPrefix(r.URL.Path, notesPrefix):
		f.notesAPI(w, r)
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

// ---------------------------------------------------------------------------
// CalDAV and Notes (in memory)
// ---------------------------------------------------------------------------

// FakeCal is a calendar-home child. Kind is "calendar" (default),
// "addressbook" or "other" (a plain collection).
type FakeCal struct {
	ID      string
	Name    string
	Kind    string
	Comps   []string // e.g. VEVENT, VTODO
	Objects map[string]string
}

// FakeNote is a note in the Notes API.
type FakeNote struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Category string `json:"category"`
	ETag     string `json:"etag"`
	Modified int64  `json:"modified"`
}

type calState struct {
	cals         []*FakeCal
	LastReport   string
	LastDelete   http.Header
	notes        map[int64]*FakeNote
	nextNote     int64
	noteVersions int64
}

// AddCalendar registers a calendar with the given supported components.
func (f *Fake) AddCalendar(id, name string, comps ...string) *FakeCal {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &FakeCal{ID: id, Name: name, Kind: "calendar", Comps: comps, Objects: map[string]string{}}
	f.cs.cals = append(f.cs.cals, c)
	return c
}

// AddCollection registers a non-calendar collection (Kind "addressbook" or "other").
func (f *Fake) AddCollection(id, name, kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cs.cals = append(f.cs.cals, &FakeCal{ID: id, Name: name, Kind: kind, Objects: map[string]string{}})
}

// Cal returns the calendar with id, or nil.
func (f *Fake) Cal(id string) *FakeCal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calLocked(id)
}

func (f *Fake) calLocked(id string) *FakeCal {
	for _, c := range f.cs.cals {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// LastReportBody is the body of the most recent REPORT request.
func (f *Fake) LastReportBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cs.LastReport
}

// LastDeleteHeader is the header of the most recent calendar DELETE.
func (f *Fake) LastDeleteHeader() http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cs.LastDelete
}

// AddNote inserts a note and returns it.
func (f *Fake) AddNote(title, content string) *FakeNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addNoteLocked(title, content, "")
}

// Note returns the note with id, or nil. Tests may mutate it directly while
// no request is in flight.
func (f *Fake) Note(id int64) *FakeNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cs.notes[id]
}

func (f *Fake) addNoteLocked(title, content, category string) *FakeNote {
	if f.cs.notes == nil {
		f.cs.notes = map[int64]*FakeNote{}
	}
	f.cs.nextNote++
	n := &FakeNote{ID: f.cs.nextNote, Title: title, Content: content, Category: category}
	f.touchNote(n)
	f.cs.notes[n.ID] = n
	return n
}

func (f *Fake) touchNote(n *FakeNote) {
	f.cs.noteVersions++
	n.Modified = f.modified.Unix() + f.cs.noteVersions
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%d", n.ID, n.Title, n.Content, f.cs.noteVersions)))
	n.ETag = hex.EncodeToString(h[:8])
}

func (f *Fake) calHome() string { return "/remote.php/dav/calendars/" + url.PathEscape(f.User) + "/" }

func (f *Fake) caldav(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rest := strings.TrimPrefix(r.URL.Path, f.calHome())
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 2)
	calID, obj := parts[0], ""
	if len(parts) == 2 {
		obj = parts[1]
	}
	switch {
	case r.Method == "PROPFIND" && calID == "":
		f.calHomePropfind(w)
	case r.Method == "MKCALENDAR" && obj == "":
		body, _ := io.ReadAll(r.Body)
		if f.calLocked(calID) != nil {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		c := &FakeCal{ID: calID, Name: calID, Kind: "calendar", Objects: map[string]string{}}
		if m := regexp.MustCompile(`(?s)<(?:\w+:)?displayname>(.*?)</(?:\w+:)?displayname>`).FindSubmatch(body); m != nil {
			var v struct {
				S string `xml:",chardata"`
			}
			if xml.Unmarshal([]byte("<x>"+string(m[1])+"</x>"), &v) == nil {
				c.Name = v.S
			}
		}
		for _, m := range regexp.MustCompile(`<(?:\w+:)?comp\s+name="([A-Z]+)"`).FindAllSubmatch(body, -1) {
			c.Comps = append(c.Comps, string(m[1]))
		}
		f.cs.cals = append(f.cs.cals, c)
		w.WriteHeader(http.StatusCreated)
	default:
		c := f.calLocked(calID)
		if c == nil {
			http.NotFound(w, r)
			return
		}
		f.calObject(w, r, c, obj)
	}
}

func (f *Fake) calObject(w http.ResponseWriter, r *http.Request, c *FakeCal, name string) {
	switch {
	case r.Method == "REPORT" && name == "":
		body, _ := io.ReadAll(r.Body)
		f.cs.LastReport = string(body)
		f.report(w, c, string(body))
	case r.Method == http.MethodGet && name != "":
		data, ok := c.Objects[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etagOf([]byte(data)))
		w.Header().Set("Content-Type", "text/calendar")
		io.WriteString(w, data)
	case r.Method == http.MethodPut && name != "":
		body, _ := io.ReadAll(r.Body)
		cur, exists := c.Objects[name]
		if r.Header.Get("If-None-Match") == "*" && exists {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if im := r.Header.Get("If-Match"); im != "" && (!exists || etagOf([]byte(cur)) != im) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		c.Objects[name] = string(body)
		w.Header().Set("ETag", etagOf(body))
		if exists {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
	case r.Method == http.MethodDelete && name != "":
		f.cs.LastDelete = r.Header.Clone()
		cur, exists := c.Objects[name]
		if !exists {
			http.NotFound(w, r)
			return
		}
		if im := r.Header.Get("If-Match"); im != "" && etagOf([]byte(cur)) != im {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		delete(c.Objects, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *Fake) calHomePropfind(w http.ResponseWriter) {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:card="urn:ietf:params:xml:ns:carddav">`)
	fmt.Fprintf(&sb, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, f.calHome())
	cals := append([]*FakeCal(nil), f.cs.cals...)
	sort.Slice(cals, func(i, j int) bool { return cals[i].ID < cals[j].ID })
	for _, c := range cals {
		rt := "<d:collection/>"
		comps := ""
		switch c.Kind {
		case "addressbook":
			rt += "<card:addressbook/>"
		case "other":
		default:
			rt += "<c:calendar/>"
			comps = "<c:supported-calendar-component-set>"
			for _, n := range c.Comps {
				comps += `<c:comp name="` + n + `"/>`
			}
			comps += "</c:supported-calendar-component-set>"
		}
		fmt.Fprintf(&sb, `<d:response><d:href>%s%s/</d:href><d:propstat><d:prop><d:displayname>%s</d:displayname><d:resourcetype>%s</d:resourcetype>%s</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
			f.calHome(), url.PathEscape(c.ID), xmlEsc(c.Name), rt, comps)
	}
	sb.WriteString(`</d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, sb.String())
}

func xmlEsc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

var (
	compFilterRe = regexp.MustCompile(`<(?:\w+:)?comp-filter\s+name="([A-Z]+)"`)
	timeRangeRe  = regexp.MustCompile(`<(?:\w+:)?time-range\s+start="([^"]+)"\s+end="([^"]+)"`)
)

func (f *Fake) report(w http.ResponseWriter, c *FakeCal, body string) {
	comp := ""
	if ms := compFilterRe.FindAllStringSubmatch(body, -1); len(ms) >= 2 {
		comp = ms[1][1]
	}
	var from, to time.Time
	ranged := false
	if m := timeRangeRe.FindStringSubmatch(body); m != nil {
		a, e1 := time.Parse("20060102T150405Z", m[1])
		b, e2 := time.Parse("20060102T150405Z", m[2])
		if e1 == nil && e2 == nil {
			from, to, ranged = a, b, true
		}
	}
	names := make([]string, 0, len(c.Objects))
	for n := range c.Objects {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`)
	for _, n := range names {
		data := c.Objects[n]
		if comp != "" && !strings.Contains(data, "BEGIN:"+comp) {
			continue
		}
		if ranged && !overlaps(data, from, to) {
			continue
		}
		fmt.Fprintf(&sb, `<d:response><d:href>%s%s/%s</d:href><d:propstat><d:prop><d:getetag>%s</d:getetag><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
			f.calHome(), url.PathEscape(c.ID), url.PathEscape(n), etagOf([]byte(data)), xmlEsc(data))
	}
	sb.WriteString(`</d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, sb.String())
}

func icalProp(data, name string) (time.Time, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, name+":") && !strings.HasPrefix(line, name+";") {
			continue
		}
		i := strings.LastIndex(line, ":")
		v := strings.TrimSpace(line[i+1:])
		for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
			if t, err := time.Parse(layout, v); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// overlaps is a simple DTSTART/DTEND/DUE overlap test; objects without any
// date always match (like an undated VTODO).
func overlaps(data string, from, to time.Time) bool {
	start, ok := icalProp(data, "DTSTART")
	due, hasDue := icalProp(data, "DUE")
	if !ok {
		if !hasDue {
			return true
		}
		start = due
	}
	end := start
	if e, ok := icalProp(data, "DTEND"); ok {
		end = e
	} else if hasDue {
		end = due
	}
	return start.Before(to) && !end.Before(from)
}

const notesPrefix = "/index.php/apps/notes/api/v1/notes"

func (f *Fake) notesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("OCS-APIRequest") != "true" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, notesPrefix), "/")
	writeJSON := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			list := []*FakeNote{}
			ids := make([]int64, 0, len(f.cs.notes))
			for id := range f.cs.notes {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids {
				list = append(list, f.cs.notes[id])
			}
			writeJSON(http.StatusOK, list)
		case http.MethodPost:
			var in struct{ Title, Content, Category string }
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			writeJSON(http.StatusOK, f.addNoteLocked(in.Title, in.Content, in.Category))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	n := f.cs.notes[id]
	if err != nil || n == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("ETag", `"`+n.ETag+`"`)
		writeJSON(http.StatusOK, n)
	case http.MethodPut:
		if im := strings.Trim(r.Header.Get("If-Match"), `"`); r.Header.Get("If-Match") != "" && im != n.ETag {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		var in struct {
			Content *string `json:"content"`
			Title   *string `json:"title"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if in.Content != nil {
			n.Content = *in.Content
		}
		if in.Title != nil {
			n.Title = *in.Title
		}
		f.touchNote(n)
		w.Header().Set("ETag", `"`+n.ETag+`"`)
		writeJSON(http.StatusOK, n)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
