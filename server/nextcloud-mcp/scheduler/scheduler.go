// Package scheduler is the "scheduler" subcommand: every minute it pushes an
// alert for reminders that came due and, once a day, for the morning summary.
// It only announces; the MCP reads the content when the button is pressed.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"nextcloud-mcp/ical"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/state"
	"nextcloud-mcp/tools"
)

const (
	window  = 24 * time.Hour // how long after DUE a reminder is still announced
	maxText = 40             // the device shows ~40 characters
	every   = 60 * time.Second
)

// Scheduler runs one announcement cycle per Tick.
type Scheduler struct {
	Deps        tools.Deps
	N           Notifier
	State       *state.Store
	ResumenHora string // "HH:MM"; empty disables the daily summary

	// notified holds the reminders whose alert went out but whose
	// X-WEITA-NOTIFIED mark did not (etag conflict, network): only the mark is
	// retried, so the alert is not repeated. It lives in memory: if the
	// process restarts in that gap, one duplicate alert is accepted.
	notified map[string]bool
}

func (s *Scheduler) now() time.Time {
	n := time.Now
	if s.Deps.Now != nil {
		n = s.Deps.Now
	}
	if s.Deps.Loc != nil {
		return n().In(s.Deps.Loc)
	}
	return n()
}

// Tick runs one cycle. A device that is not connected is not an error: the
// same work is retried on the next Tick.
func (s *Scheduler) Tick(ctx context.Context) error {
	now := s.now()
	return errors.Join(s.avisarRecordatorios(ctx, now), s.avisarResumen(ctx, now))
}

func (s *Scheduler) avisarRecordatorios(ctx context.Context, now time.Time) error {
	todos, err := tools.OpenRecordatorios(ctx, &s.Deps)
	if err != nil {
		return err
	}
	var due, fresh []todoRef
	keep := map[string]bool{}
	for _, t := range todos {
		// Date-only reminders are ignored, like recordatorios_pendientes does.
		if t.Due == nil || t.DueIsDate || t.Due.After(now) || now.Sub(*t.Due) > window {
			continue
		}
		if _, _, ok := ical.Field(t.Obj.Data, tools.MarkNotified); ok {
			continue
		}
		if _, _, ok := ical.Field(t.Obj.Data, tools.MarkSpoken); ok {
			continue
		}
		r := todoRef{uid: t.UID, summary: t.Summary, calID: t.Cal.ID, obj: t.Obj}
		due = append(due, r)
		if s.notified[t.UID] {
			keep[t.UID] = true
		} else {
			fresh = append(fresh, r)
		}
	}
	s.notified = keep // forget reminders that were completed or cancelled meanwhile
	if len(fresh) > 0 {
		if err := s.N.Notify(ctx, avisoRecordatorios(fresh)); err != nil {
			if errors.Is(err, ErrNotConnected) {
				return nil
			}
			return err
		}
		for _, r := range fresh {
			s.notified[r.uid] = true
		}
	}
	var errs []error
	for _, r := range due {
		if err := s.marcar(ctx, r); err != nil {
			// Conflicts resolve themselves: next Tick re-reads and retries.
			if !errors.Is(err, nc.ErrConflict) {
				errs = append(errs, err)
			}
			continue
		}
		delete(s.notified, r.uid)
	}
	return errors.Join(errs...)
}

type todoRef struct {
	uid, summary, calID string
	obj                 nc.CalObject
}

func (s *Scheduler) marcar(ctx context.Context, r todoRef) error {
	data := ical.SetVTodoField(r.obj.Data, tools.MarkNotified, "1")
	name := strings.TrimSuffix(path.Base(r.obj.Href), ".ics")
	_, err := s.Deps.NC.PutObject(ctx, r.calID, name, data, r.obj.ETag)
	return err
}

func avisoRecordatorios(l []todoRef) Aviso {
	if len(l) == 1 {
		return Aviso{Titulo: "Recordatorio", Texto: clip(l[0].summary)}
	}
	return Aviso{Titulo: "Recordatorio", Texto: fmt.Sprintf("Tienes %d recordatorios", len(l))}
}

// clip cuts to maxText characters, at a word boundary when there is one.
func clip(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxText {
		return string(r)
	}
	cut := string(r[:maxText])
	if i := strings.LastIndex(cut, " "); i > 0 && r[maxText] != ' ' {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut)
}

// avisarResumen sends the daily "summary ready" alert once ResumenHora has
// passed, and records it only after the push succeeded. If the device is off
// it retries every Tick until midnight; the next day it waits for ResumenHora.
func (s *Scheduler) avisarResumen(ctx context.Context, now time.Time) error {
	if s.ResumenHora == "" || s.State == nil {
		return nil
	}
	at, err := time.ParseInLocation("15:04", s.ResumenHora, now.Location())
	if err != nil {
		return fmt.Errorf("RESUMEN_HORA inválida: %w", err)
	}
	y, m, d := now.Date()
	if now.Before(time.Date(y, m, d, at.Hour(), at.Minute(), 0, 0, now.Location())) {
		return nil
	}
	today := now.Format("2006-01-02")
	st, err := s.State.Get()
	if err != nil {
		return err
	}
	if st.ResumenEnviado == today {
		return nil
	}
	if err := s.N.Notify(ctx, Aviso{Titulo: "Buenos días", Texto: "Tienes tu resumen del día"}); err != nil {
		if errors.Is(err, ErrNotConnected) {
			return nil
		}
		return err
	}
	return s.State.Set("resumen_enviado", today)
}

// Run is the scheduler subcommand: it loops every minute until SIGTERM/SIGINT.
func Run(env func(string) string, errw io.Writer) int {
	// RECORDATORIOS_MAC (empty = every connected device) and RESUMEN_HORA
	// (empty = no daily summary) are optional.
	var missing []string
	for _, k := range []string{"NEXTCLOUD_URL", "NEXTCLOUD_USER", "NEXTCLOUD_PASSWORD", "PUSH_URL", "PUSH_TOKEN"} {
		if env(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(errw, "nextcloud-mcp scheduler: faltan variables: %s\n", strings.Join(missing, ", "))
		return 1
	}
	loc, err := time.LoadLocation(env("TZ"))
	if err != nil || env("TZ") == "" {
		if loc, err = time.LoadLocation("America/Santiago"); err != nil {
			fmt.Fprintf(errw, "nextcloud-mcp scheduler: zona horaria: %v\n", err)
			return 1
		}
	}
	s := &Scheduler{
		Deps: tools.Deps{
			NC:  nc.New(env("NEXTCLOUD_URL"), env("NEXTCLOUD_USER"), env("NEXTCLOUD_PASSWORD")),
			Now: time.Now,
			Loc: loc,
		},
		N: HTTPNotifier{
			URL: env("PUSH_URL"), Token: env("PUSH_TOKEN"), MAC: env("RECORDATORIOS_MAC"),
			Client: &http.Client{Timeout: 10 * time.Second},
		},
		State:       state.Open(state.Path(env)),
		ResumenHora: env("RESUMEN_HORA"),
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(errw, "nextcloud-mcp scheduler: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-tick.C:
		}
	}
}
