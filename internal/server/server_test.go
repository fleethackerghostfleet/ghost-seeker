package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestAPI(t *testing.T) {
	data := store.New(10)
	app := New(data)

	response := request(app, http.MethodGet, "/api/config", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d, want %d", response.Code, http.StatusOK)
	}

	response = request(app, http.MethodPost, "/api/directives", map[string]string{"text": "small boats"})
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/directives status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	response = request(app, http.MethodPost, "/api/directives", map[string]string{"text": "hello world"})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST invalid directive status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}

	response = request(app, http.MethodPut, "/api/model", map[string]string{"id": "unknown"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid model status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	item := command.Item{
		ID: "test-item",
		TS: time.Now(),
		Dets: []command.Detection{
			{Class: "bird", Conf: .8},
		},
	}
	data.Add(&item, 0)

	response = request(app, http.MethodPatch, "/api/items/missing", map[string]int{"manual": 2})
	if response.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown item status = %d, want %d", response.Code, http.StatusNotFound)
	}

	response = request(app, http.MethodPatch, "/api/items/test-item", map[string]int{"manual": 2})
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH manual=2 status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var patched struct {
		Item command.Item `json:"item"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Item.Tier != 2 {
		t.Fatalf("tier after manual=2 is %d, want 2", patched.Item.Tier)
	}

	response = request(app, http.MethodPatch, "/api/items/test-item", map[string]int{"manual": 0})
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH manual=0 status = %d, want %d", response.Code, http.StatusOK)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Item.Tier != command.TierFor(patched.Item.Central) {
		t.Fatalf("tier after clearing manual override is %d, want model tier %d", patched.Item.Tier, command.TierFor(patched.Item.Central))
	}

	response = request(app, http.MethodDelete, "/api/directives", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/directives status = %d, want %d", response.Code, http.StatusNoContent)
	}
	_, _, _, state := data.Snapshot()
	if len(state.Directives) != 0 {
		t.Fatalf("remaining directives = %d, want 0", len(state.Directives))
	}
}

func TestDirectiveLengthLimit(t *testing.T) {
	app := New(store.New(10))
	response := request(app, http.MethodPost, "/api/directives", map[string]string{"text": strings.Repeat("x", 501)})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST overlong directive status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" {
		t.Fatal("POST overlong directive response has no error")
	}
}

func TestDirectiveBodySizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want int
	}{
		{name: "malformed 128 KiB body", body: bytes.Repeat([]byte("x"), 128<<10), want: http.StatusBadRequest},
		{name: "valid oversized JSON", body: []byte(`{"text":"` + strings.Repeat("x", 128<<10) + `"}`), want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := New(store.New(10))
			req := httptest.NewRequest(http.MethodPost, "/api/directives", bytes.NewReader(tc.body))
			response := httptest.NewRecorder()
			app.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("POST oversized directive status = %d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestStreamClientLimit(t *testing.T) {
	app := New(store.New(10))
	cancels := make([]context.CancelFunc, 0, maxStreamClients)
	var wg sync.WaitGroup
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		wg.Wait()
	}()

	for i := 0; i < maxStreamClients; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
		ctx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(ctx)
		cancels = append(cancels, cancel)
		response := httptest.NewRecorder()
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.ServeHTTP(response, req)
		}()
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		app.hub.mu.Lock()
		count := len(app.hub.clients)
		app.hub.mu.Unlock()
		if count == maxStreamClients {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active stream clients = %d, want %d", count, maxStreamClients)
		}
		time.Sleep(time.Millisecond)
	}

	response := request(app, http.MethodGet, "/api/stream", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("65th stream client status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestCompileDirectiveEndpoint(t *testing.T) {
	app := New(store.New(10))
	response := request(app, http.MethodPost, "/api/directives/compile", map[string]string{"text": "small boats for 2 hours"})
	if response.Code != http.StatusOK {
		t.Fatalf("compile status = %d: %s", response.Code, response.Body.String())
	}
	var compiled command.Compiled
	if err := json.Unmarshal(response.Body.Bytes(), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Path != "rules" || compiled.Readback != "Boost: small boat. For 2h." {
		t.Fatalf("compiled = %+v", compiled)
	}
}

func TestBlockedDirectiveAndDurationExpiry(t *testing.T) {
	app := New(store.New(10))
	response := request(app, http.MethodPost, "/api/directives", map[string]string{"text": "prioritize everything"})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blocked add status = %d, want %d: %s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}

	var expire func()
	var delay time.Duration
	app.after = func(d time.Duration, callback func()) {
		delay, expire = d, callback
	}
	response = request(app, http.MethodPost, "/api/directives", map[string]string{"text": "small boats for 1 hour"})
	if response.Code != http.StatusCreated {
		t.Fatalf("timed add status = %d: %s", response.Code, response.Body.String())
	}
	if delay != time.Hour || expire == nil {
		t.Fatalf("expiry scheduler got delay=%s callback=%t", delay, expire != nil)
	}
	_, _, _, state := app.store.Snapshot()
	if len(state.Directives) != 1 {
		t.Fatalf("active directives before expiry = %d", len(state.Directives))
	}
	expire()
	_, _, _, state = app.store.Snapshot()
	if len(state.Directives) != 0 {
		t.Fatalf("active directives after expiry = %d", len(state.Directives))
	}
}

func TestExplicitDurationForTextDirective(t *testing.T) {
	app := New(store.New(10))
	var delay time.Duration
	app.after = func(d time.Duration, _ func()) { delay = d }
	response := request(app, http.MethodPost, "/api/directives", map[string]any{"text": "small boats", "durationMin": 60})
	if response.Code != http.StatusCreated {
		t.Fatalf("timed add status = %d: %s", response.Code, response.Body.String())
	}
	if delay != time.Hour {
		t.Fatalf("expiry delay = %s, want 1h", delay)
	}
}

func TestEditedDirectiveExtractsDurationForExpiry(t *testing.T) {
	app := New(store.New(10))
	var delay time.Duration
	app.after = func(d time.Duration, _ func()) { delay = d }
	rules := []command.Rule{{Type: "boost", Classes: []string{"tanker"}, Weight: 1}}
	response := request(app, http.MethodPost, "/api/directives", map[string]any{
		"text": "boost tankers for 2 hours", "rules": rules,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("timed edited add status = %d: %s", response.Code, response.Body.String())
	}
	if delay != 120*time.Minute {
		t.Fatalf("expiry delay = %s, want 2h", delay)
	}
}

func TestDirectiveCountLimit(t *testing.T) {
	app := New(store.New(10))
	for i := 0; i < store.MaxActiveDirectives; i++ {
		response := request(app, http.MethodPost, "/api/directives", map[string]string{"text": "small boats"})
		if response.Code != http.StatusCreated {
			t.Fatalf("POST directive %d status = %d, want %d: %s", i+1, response.Code, http.StatusCreated, response.Body.String())
		}
	}
	response := request(app, http.MethodPost, "/api/directives", map[string]string{"text": "birds"})
	if response.Code != http.StatusConflict {
		t.Fatalf("POST directive beyond limit status = %d, want %d", response.Code, http.StatusConflict)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != store.ErrTooManyActiveDirectives.Error() {
		t.Fatalf("error = %q, want %q", body["error"], store.ErrTooManyActiveDirectives)
	}
	_, _, _, state := app.store.Snapshot()
	if len(state.Directives) != store.MaxActiveDirectives {
		t.Fatalf("active directives = %d, want %d", len(state.Directives), store.MaxActiveDirectives)
	}
}

func request(handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			panic(err)
		}
	}
	request := httptest.NewRequest(method, target, &encoded)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestFullResOverrideRequiresOperator(t *testing.T) {
	data := store.New(10)
	app := New(data)
	data.Add(&command.Item{ID: "img-1", TS: time.Now(), Dets: []command.Detection{{Class: "bird", Conf: .8}}}, 0)

	if r := request(app, http.MethodPost, "/api/items/missing/full-res", map[string]string{"by": "op1"}); r.Code != http.StatusNotFound {
		t.Fatalf("missing item status = %d, want 404", r.Code)
	}
	if r := request(app, http.MethodPost, "/api/items/img-1/full-res", map[string]string{"by": "  "}); r.Code != http.StatusBadRequest {
		t.Fatalf("blank operator status = %d, want 400", r.Code)
	}
	r := request(app, http.MethodPost, "/api/items/img-1/full-res", map[string]string{"by": "op1"})
	if r.Code != http.StatusOK {
		t.Fatalf("full-res status = %d: %s", r.Code, r.Body.String())
	}
	var body struct{ Item command.Item }
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Item.FullRes || body.Item.FullResBy != "op1" || body.Item.FullResAt == nil {
		t.Fatalf("item = %+v, want fullRes by op1", body.Item)
	}
}
