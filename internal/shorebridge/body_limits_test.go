package shorebridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

type eventFlushWriter struct {
	*httptest.ResponseRecorder
	flush func()
}

func (w eventFlushWriter) Flush() { w.ResponseRecorder.Flush(); w.flush() }

func TestConsoleSSEStillFlushesSnapshotAndUpdates(t *testing.T) {
	c := NewConsole(New("", "", nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	flushed := 0
	writer := eventFlushWriter{w, func() {
		flushed++
		if flushed == 1 {
			c.mu.Lock()
			c.publish("update", map[string]any{"test": true})
			c.mu.Unlock()
		} else {
			cancel()
		}
	}}
	c.Handler().ServeHTTP(writer, httptest.NewRequest("GET", "/api/stream", nil).WithContext(ctx))
	if flushed != 2 || w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(w.Body.String(), "event: snapshot") || !strings.Contains(w.Body.String(), "event: update") {
		t.Fatalf("stream flushes=%d body=%s", flushed, w.Body.String())
	}
	if len(c.subs) != 0 {
		t.Fatal("stream subscription leaked")
	}
}

func TestLiveJSONBodyLimitsAndTrailingData(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid body reached upstream")
		w.WriteHeader(500)
	}))
	defer upstream.Close()
	b := New(upstream.URL, "", nil)
	c := NewConsole(b)
	log, err := reviews.Open(filepath.Join(t.TempDir(), "reviews.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	c.SetReviewLog(log)
	for _, route := range []struct {
		handler      http.Handler
		method, path string
		limit        int
	}{
		{b.Handler(), "POST", "/api/shore/policy", 64 << 10},
		{b.Handler(), "POST", "/api/shore/emcon", 4 << 10},
		{b.Handler(), "POST", "/api/shore/pull", 64 << 10},
		{b.Handler(), "POST", "/api/shore/override", 64 << 10},
		{c.Handler(), "PATCH", "/api/items/missing", 64 << 10},
		{c.Handler(), "POST", "/api/items/missing/decision", 64 << 10},
		{c.Handler(), "POST", "/api/items/missing/full-res", 64 << 10},
		{c.Handler(), "POST", "/api/items/missing/pull", 64 << 10},
		{c.Handler(), "POST", "/api/directives", 64 << 10},
		{c.Handler(), "POST", "/api/directives/compile", 64 << 10},
		{c.Handler(), "POST", "/api/directives/preview", 256 << 10},
		{c.Handler(), "POST", "/api/items/missing/review", 64 << 10},
		{c.Handler(), "POST", "/api/reviews/missing/reason", 64 << 10},
		{c.Handler(), "POST", "/api/reviews/missing/undo", 64 << 10},
	} {
		t.Run(route.path, func(t *testing.T) {
			for _, tc := range []struct {
				body string
				want int
			}{
				{`{}` + strings.Repeat(" ", route.limit), 413},
				{`{"note":"` + strings.Repeat("x", route.limit) + `"}`, 413},
				{`{} {}`, 400}, {`{} junk`, 400}, {``, 400},
			} {
				w := httptest.NewRecorder()
				route.handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(tc.body)))
				if w.Code != tc.want {
					t.Fatalf("status=%d want=%d: %s", w.Code, tc.want, w.Body.String())
				}
			}
		})
	}
}
