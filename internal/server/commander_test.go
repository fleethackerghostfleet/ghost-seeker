package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestDecisionAPI(t *testing.T) {
	data := store.New(10)
	app := New(data)
	data.Add(&command.Item{ID: "x", TS: time.Now(), Dets: []command.Detection{{Class: "warship", Conf: .95}}}, 0)

	for _, tc := range []struct {
		name, path string
		body       any
		want       int
	}{
		{"not escalated", "/api/items/x/decision", map[string]string{"decision": "track"}, http.StatusConflict},
		{"missing", "/api/items/nope/decision", map[string]string{"decision": "track"}, http.StatusNotFound},
		{"bad body", "/api/items/x/decision", map[string]string{"verdict": "track"}, http.StatusBadRequest},
	} {
		if got := request(app, http.MethodPost, tc.path, tc.body).Code; got != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, got, tc.want)
		}
	}
	if code := request(app, http.MethodPatch, "/api/items/x", map[string]bool{"sent": true}).Code; code != http.StatusOK {
		t.Fatalf("send to commander status = %d", code)
	}
	if code := request(app, http.MethodPost, "/api/items/x/decision", map[string]string{"decision": "bogus"}).Code; code != http.StatusBadRequest {
		t.Errorf("unknown decision status = %d, want 400", code)
	}
	if code := request(app, http.MethodPost, "/api/items/x/decision", map[string]string{"decision": "action", "note": "intercept"}).Code; code != http.StatusOK {
		t.Fatalf("decision status = %d, want 200", code)
	}
	if item, _ := data.GetItem("x"); item.Decision != "action" || item.DecisionNote != "intercept" {
		t.Fatalf("stored decision = %q / %q", item.Decision, item.DecisionNote)
	}
	if res := request(app, http.MethodGet, "/cdr", nil); res.Code != http.StatusFound || res.Header().Get("Location") != "/cdr.html" {
		t.Errorf("GET /cdr = %d %q, want redirect to /cdr.html", res.Code, res.Header().Get("Location"))
	}
}
