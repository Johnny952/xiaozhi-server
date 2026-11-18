package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nextcloud-mcp/nc"
)

func TestParallelStopsAfterFirstError(t *testing.T) {
	var started int32
	boom := errors.New("boom")
	err := parallel(context.Background(), 100, func(ctx context.Context, i int) error {
		atomic.AddInt32(&started, 1)
		if i == 0 {
			return boom
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return nil
		}
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := atomic.LoadInt32(&started); n > maxParallel+1 {
		t.Fatalf("started %d jobs after first error", n)
	}
}

func TestHandlerDeadlineAnswersInTime(t *testing.T) {
	old := toolTimeout
	toolTimeout = 100 * time.Millisecond
	defer func() { toolTimeout = old }()
	h := handler(func(ctx context.Context, req mcp.CallToolRequest) *mcp.CallToolResult {
		<-ctx.Done()
		return failErr("No pude", ctx.Err())
	})
	start := time.Now()
	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil || res == nil || !res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Content[0].(mcp.TextContent).Text, "no respondió a tiempo") {
		t.Fatalf("text = %v", res.Content[0])
	}
	if time.Since(start) > time.Second {
		t.Fatal("too slow")
	}
}

func TestFetchLogDaysStopsOnFirstError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := &Deps{NC: nc.New(srv.URL, "u", "p"), Now: time.Now, Loc: time.UTC}
	var dates []time.Time
	for i := 0; i < 31; i++ {
		dates = append(dates, time.Date(2026, 10, 1+i%28, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i/28))
	}
	if _, err := d.fetchLogDays(context.Background(), []string{"a", "b", "c", "d", "e"}, dates); err == nil {
		t.Fatal("want error")
	}
	if n := atomic.LoadInt32(&hits); n > 3*maxParallel {
		t.Fatalf("%d requests after first error", n)
	}
}
