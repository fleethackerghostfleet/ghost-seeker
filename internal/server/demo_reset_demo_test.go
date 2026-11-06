//go:build !prod

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestDemoResetValidationAndWrapping(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var opts []Option
		if enabled {
			opts = append(opts, WithDemoReset())
		}
		app := New(store.New(4), opts...)
		config := request(app, "GET", "/api/config", nil)
		if !strings.Contains(config.Body.String(), fmt.Sprintf(`"demoReset":%t`, enabled)) {
			t.Fatal(config.Body.String())
		}
		for _, tc := range []struct {
			method, body string
			status       int
		}{
			{"POST", `{"confirm":"reset"}`, 200}, {"GET", "", 405},
			{"POST", "", 400}, {"POST", `{}`, 400}, {"POST", `null`, 400},
			{"POST", `{"confirm":"no"}`, 400}, {"POST", `{"confirm":"reset","extra":1}`, 400},
			{"POST", `{"confirm":"reset"} {}`, 400},
		} {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/demo/reset", strings.NewReader(tc.body)))
			want := tc.status
			if !enabled {
				want = 404
			}
			if w.Code != want {
				t.Fatalf("enabled=%v %s %q: %d %s", enabled, tc.method, tc.body, w.Code, w.Body.String())
			}
		}
	}
	app := New(store.New(4), WithDemoReset())
	wrapped := httpx.SameOrigin(opauth.NewSessions("test-token", false).Wrap(app))
	for _, tc := range []struct {
		token, origin string
		status        int
	}{
		{"", "", 401}, {"test-token", "https://evil.example", 403}, {"test-token", "http://localhost", 200},
	} {
		r := httptest.NewRequest("POST", "http://localhost/api/demo/reset", strings.NewReader(`{"confirm":"reset"}`))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("wrapped: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestDemoResetStreamsAndStalePublications(t *testing.T) {
	data := store.New(4)
	old, _ := data.Add(&command.Item{ID: "old"}, 2)
	app := New(data, WithDemoReset())
	ts := httptest.NewServer(app)
	defer ts.Close()
	readers := make([]*bufio.Reader, 2)
	readSnapshot := func(reader *bufio.Reader) map[string]any {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var body map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &body); err != nil {
					t.Fatal(err)
				}
				return body
			}
		}
	}
	for i := range readers {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/stream", nil)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		readers[i] = bufio.NewReader(response.Body)
		if snapshot := readSnapshot(readers[i]); snapshot["epoch"] != float64(0) {
			t.Fatal(snapshot)
		}
	}
	peer, _ := app.hub.add()
	defer app.hub.remove(peer)
	if response := request(app, "POST", "/api/demo/reset", map[string]string{"confirm": "reset"}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	for _, reader := range readers {
		snapshot := readSnapshot(reader)
		if snapshot["reset"] != true || snapshot["epoch"] != float64(1) || len(snapshot["items"].([]any)) != 0 || snapshot["cdr"] != float64(0) {
			t.Fatal(snapshot)
		}
	}
	<-peer.events
	app.PublishItem(old, command.Stats{Cap: 999})
	app.PublishUpdate(old, 999)
	if len(peer.events) != 0 {
		t.Fatal("stale item published")
	}
	app.PublishStats(command.Stats{Cap: 999})
	e := <-peer.events
	if stats := e.data.(map[string]any)["stats"].(command.Stats); stats.Cap != 0 {
		t.Fatal(stats)
	}
}

func TestDemoResetPreservesReviewLogAndConcurrentOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviews.jsonl")
	l, err := reviews.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	data := store.New(1000)
	app := New(data, WithDemoReset())
	app.SetReviewLog(l)
	data.Add(&command.Item{ID: "reviewed"}, 0)
	if w := request(app, "POST", "/api/items/reviewed/review", map[string]any{"reviewer": "test", "action": "accept"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	request(app, "POST", "/api/demo/reset", map[string]string{"confirm": "reset"})
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reset modified review log")
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				switch worker {
				case 0:
					item, _ := data.Add(&command.Item{ID: fmt.Sprintf("item-%d", i), KB: 10}, 0)
					app.PublishItem(item, command.Stats{})
				case 1:
					request(app, "POST", fmt.Sprintf("/api/items/item-%d/review", i), map[string]any{"reviewer": "test", "action": "accept"})
				case 2:
					for _, item := range data.AdvanceUploads(time.Millisecond) {
						app.PublishUpdate(item, 0)
					}
				case 3:
					request(app, "POST", "/api/demo/reset", map[string]string{"confirm": "reset"})
				}
			}
		}(worker)
	}
	wg.Wait()
}
