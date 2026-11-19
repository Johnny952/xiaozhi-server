package scheduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/ical"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/nctest"
	"nextcloud-mcp/state"
	"nextcloud-mcp/tools"
)

var loc = func() *time.Location {
	l, err := time.LoadLocation("America/Santiago")
	if err != nil {
		panic(err)
	}
	return l
}()

func at(h, m int) time.Time { return time.Date(2026, 11, 18, h, m, 0, 0, loc) }

type fakeNotifier struct {
	avisos []Aviso
	err    error
	// onNotify runs inside Notify, to simulate a concurrent edit.
	onNotify func()
}

func (n *fakeNotifier) Notify(_ context.Context, a Aviso) error {
	if n.onNotify != nil {
		n.onNotify()
	}
	if n.err != nil {
		return n.err
	}
	n.avisos = append(n.avisos, a)
	return nil
}

type fixture struct {
	s    *Scheduler
	n    *fakeNotifier
	c    *nctest.FakeCal
	st   *state.Store
	now  *time.Time
	path string
}

func newFixture(t *testing.T, now time.Time, hora string) *fixture {
	t.Helper()
	f := nctest.New(t, "user")
	f.AddCalendar("personal", "Personal", "VEVENT")
	c := f.AddCalendar("recordatorios", "Recordatorios", "VTODO")
	path := filepath.Join(t.TempDir(), "recordatorios-state.json")
	st := state.Open(path)
	n := &fakeNotifier{}
	fx := &fixture{n: n, c: c, st: st, now: &now, path: path}
	fx.s = &Scheduler{
		Deps:        tools.Deps{NC: nc.New(f.URL, "user", f.Pass), Now: func() time.Time { return *fx.now }, Loc: loc},
		N:           n,
		State:       st,
		ResumenHora: hora,
	}
	return fx
}

func (fx *fixture) add(uid, summary string, due time.Time) {
	fx.c.Objects[uid+".ics"] = ical.MakeVTodo(ical.Todo{UID: uid, Summary: summary, Due: &due, Alarm: true}, due.Add(-time.Hour))
}

func (fx *fixture) tick(t *testing.T) {
	t.Helper()
	if err := fx.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
}

func TestTickAvisaVencido(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "llamar al proveedor de internet por favor ahora mismo", now.Add(-time.Minute))
	fx.tick(t)
	if len(fx.n.avisos) != 1 {
		t.Fatalf("avisos: %+v", fx.n.avisos)
	}
	a := fx.n.avisos[0]
	if a.Titulo != "Recordatorio" || a.Texto != "llamar al proveedor de internet por" {
		t.Errorf("aviso: %+v", a)
	}
	if len([]rune(a.Texto)) > 40 {
		t.Errorf("texto too long: %q", a.Texto)
	}
	if !strings.Contains(fx.c.Objects["a.ics"], tools.MarkNotified+":") {
		t.Errorf("not marked:\n%s", fx.c.Objects["a.ics"])
	}
}

func TestTickIgnoraYaNotificado(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "x", now.Add(-time.Minute))
	fx.c.Objects["a.ics"] = ical.SetVTodoField(fx.c.Objects["a.ics"], tools.MarkNotified, "1")
	fx.tick(t)
	if len(fx.n.avisos) != 0 {
		t.Errorf("avisos: %+v", fx.n.avisos)
	}
}

func TestTickIgnoraMasDe24h(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "x", now.Add(-25*time.Hour))
	fx.tick(t)
	if len(fx.n.avisos) != 0 {
		t.Errorf("avisos: %+v", fx.n.avisos)
	}
}

func TestTickIgnoraFuturo(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "x", now.Add(time.Minute))
	fx.tick(t)
	if len(fx.n.avisos) != 0 {
		t.Errorf("avisos: %+v", fx.n.avisos)
	}
}

func TestTickIgnoraSoloFecha(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.c.Objects["a.ics"] = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:a\r\nSUMMARY:x\r\nDUE;VALUE=DATE:20261118\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	fx.tick(t)
	if len(fx.n.avisos) != 0 {
		t.Errorf("avisos: %+v", fx.n.avisos)
	}
}

func TestTick404Reintenta(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "llamar", now.Add(-time.Minute))
	fx.n.err = ErrNotConnected
	fx.tick(t)
	if strings.Contains(fx.c.Objects["a.ics"], tools.MarkNotified) {
		t.Fatal("marked despite 404")
	}
	fx.n.err = nil
	fx.tick(t)
	if len(fx.n.avisos) != 1 || !strings.Contains(fx.c.Objects["a.ics"], tools.MarkNotified+":") {
		t.Errorf("avisos %+v\n%s", fx.n.avisos, fx.c.Objects["a.ics"])
	}
}

func TestTickErrorDelNotifierSeDevuelve(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "llamar", now.Add(-time.Minute))
	fx.n.err = errors.New("boom")
	if err := fx.s.Tick(context.Background()); err == nil {
		t.Error("want error")
	}
	if strings.Contains(fx.c.Objects["a.ics"], tools.MarkNotified) {
		t.Error("marked despite error")
	}
}

func TestTickVarios(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	for i, u := range []string{"a", "b", "c"} {
		fx.add(u, "r"+u, now.Add(-time.Duration(i+1)*time.Minute))
	}
	fx.tick(t)
	if len(fx.n.avisos) != 1 || fx.n.avisos[0].Texto != "Tienes 3 recordatorios" || fx.n.avisos[0].Titulo != "Recordatorio" {
		t.Fatalf("avisos: %+v", fx.n.avisos)
	}
	for _, u := range []string{"a", "b", "c"} {
		if !strings.Contains(fx.c.Objects[u+".ics"], tools.MarkNotified+":") {
			t.Errorf("%s not marked", u)
		}
	}
}

func TestSchedulerEtagConflictRetries(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "")
	fx.add("a", "llamar", now.Add(-time.Minute))
	// A concurrent edit lands between the read and the mark PUT.
	fx.n.onNotify = func() {
		fx.c.Objects["a.ics"] = ical.SetVTodoField(fx.c.Objects["a.ics"], "DESCRIPTION", "editado")
		fx.n.onNotify = nil
	}
	fx.tick(t)
	if len(fx.n.avisos) != 1 {
		t.Fatalf("avisos: %+v", fx.n.avisos)
	}
	if strings.Contains(fx.c.Objects["a.ics"], tools.MarkNotified) {
		t.Fatal("edit was overwritten by a stale PUT")
	}
	fx.tick(t)
	if len(fx.n.avisos) != 1 {
		t.Errorf("notified again: %+v", fx.n.avisos)
	}
	data := fx.c.Objects["a.ics"]
	if !strings.Contains(data, tools.MarkNotified+":") || !strings.Contains(data, "editado") {
		t.Errorf("want mark plus the edit:\n%s", data)
	}
}

func TestTickResumenUnaVezPorDia(t *testing.T) {
	fx := newFixture(t, at(7, 31), "07:30")
	fx.tick(t)
	if len(fx.n.avisos) != 1 || fx.n.avisos[0] != (Aviso{Titulo: "Buenos días", Texto: "Tienes tu resumen del día"}) {
		t.Fatalf("avisos: %+v", fx.n.avisos)
	}
	if st, _ := fx.st.Get(); st.ResumenEnviado != "2026-11-18" {
		t.Errorf("state: %+v", st)
	}
	fx.tick(t)
	if len(fx.n.avisos) != 1 {
		t.Errorf("second tick notified: %+v", fx.n.avisos)
	}
}

func TestTickResumenAntesDeHora(t *testing.T) {
	fx := newFixture(t, at(7, 29), "07:30")
	fx.tick(t)
	if len(fx.n.avisos) != 0 {
		t.Errorf("avisos: %+v", fx.n.avisos)
	}
}

func TestTickResumenReintentaHastaMedianoche(t *testing.T) {
	fx := newFixture(t, at(7, 31), "07:30")
	fx.n.err = ErrNotConnected
	fx.tick(t)
	if st, _ := fx.st.Get(); st.ResumenEnviado != "" {
		t.Fatalf("state written on 404: %+v", st)
	}
	fx.n.err = nil
	*fx.now = at(12, 0)
	fx.tick(t)
	if len(fx.n.avisos) != 1 {
		t.Fatalf("avisos: %+v", fx.n.avisos)
	}
	// A fresh day: 404 late at night leaves no state; next day before the hour is quiet.
	fx2 := newFixture(t, at(23, 58), "07:30")
	fx2.n.err = ErrNotConnected
	fx2.tick(t)
	if st, _ := fx2.st.Get(); st.ResumenEnviado != "" {
		t.Fatalf("state written on 404: %+v", st)
	}
	fx2.n.err = nil
	*fx2.now = time.Date(2026, 11, 19, 0, 1, 0, 0, loc)
	fx2.tick(t)
	if len(fx2.n.avisos) != 0 {
		t.Errorf("avisos: %+v", fx2.n.avisos)
	}
}

func TestSchedulerRestartNoDuplicates(t *testing.T) {
	now := at(8, 0)
	fx := newFixture(t, now, "07:30")
	fx.add("a", "llamar", now.Add(-time.Minute))
	fx.tick(t)
	if len(fx.n.avisos) != 2 {
		t.Fatalf("avisos: %+v", fx.n.avisos)
	}
	n2 := &fakeNotifier{}
	s2 := &Scheduler{Deps: fx.s.Deps, N: n2, State: state.Open(fx.path), ResumenHora: "07:30"}
	if err := s2.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n2.avisos) != 0 {
		t.Errorf("duplicates: %+v", n2.avisos)
	}
}

func TestHTTPNotifier(t *testing.T) {
	var gotAuth, gotBody, gotCT string
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotAuth, gotBody, gotCT = r.Header.Get("Authorization"), string(b), r.Header.Get("Content-Type")
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		w.WriteHeader(code)
	}))
	defer srv.Close()
	n := HTTPNotifier{URL: srv.URL, Token: "tok", MAC: "aa:bb", Client: srv.Client()}
	if err := n.Notify(context.Background(), Aviso{Titulo: "Recordatorio", Texto: "llamar"}); err != nil {
		t.Fatal(err)
	}
	want := `{"mac":"aa:bb","payload":{"type":"alert","status":"Recordatorio","message":"llamar","emotion":"thinking"}}`
	if gotAuth != "Bearer tok" || gotBody != want || gotCT != "application/json" {
		t.Errorf("auth %q ct %q body %q", gotAuth, gotCT, gotBody)
	}
	code = http.StatusNotFound
	if err := n.Notify(context.Background(), Aviso{}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("404: %v", err)
	}
	code = http.StatusInternalServerError
	if err := n.Notify(context.Background(), Aviso{}); err == nil || errors.Is(err, ErrNotConnected) {
		t.Errorf("500: %v", err)
	}
}

func TestRunFaltanEnv(t *testing.T) {
	var errb strings.Builder
	if code := Run(func(string) string { return "" }, &errb); code == 0 {
		t.Error("want non-zero")
	}
	if !strings.Contains(errb.String(), "PUSH_URL") {
		t.Errorf("stderr: %q", errb.String())
	}
}
