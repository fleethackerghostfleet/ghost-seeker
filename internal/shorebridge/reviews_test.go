package shorebridge

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestDurableLiveReviews(t *testing.T) {
	c := NewConsole(New("http://shore", "", nil))
	c.base["i"] = &command.Item{ID: "i", SourceID: "cam", CentralTier: 2, BaseTier: 3, Tier: 2, Dets: []command.Detection{{Class: "ship", Conf: .9}}, Live: &command.LiveMeta{ItemID: "shore-i", SightingID: "s1", Label: "ship", Subtype: "cargo", Unknownness: .7, ImageForm: "crop"}}
	c.order = []string{"i"}
	c.sources["cam"] = catalog.Source{Name: "Camera"}
	c.policy.Text = "prioritize ships"
	handler := c.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	for _, path := range []string{"/api/items/i/review", "/api/reviews/x/reason", "/api/reviews/x/undo", "/api/reviews/summary", "/api/reviews/export?format=csv"} {
		method := "POST"
		if strings.Contains(path, "summary") || strings.Contains(path, "export") {
			method = "GET"
		}
		if w := request(method, path, "{}"); w.Code != 503 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	path := filepath.Join(t.TempDir(), "reviews.jsonl")
	l, err := reviews.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c.SetReviewLog(l)
	for _, body := range []string{`{"reviewer":" ","action":"accept"}`, `{"reviewer":"A","action":"tier","tier":0}`, `{"reviewer":"A","action":"bogus"}`, `{"reviewer":"A","action":"accept","model_score":1}`, `{`} {
		if w := request("POST", "/api/items/i/review", body); w.Code != 400 {
			t.Fatalf("invalid: %d %s", w.Code, w.Body)
		}
	}
	if w := request("POST", "/api/items/missing/review", `{"reviewer":"A","action":"accept"}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	var first string
	for n, action := range []string{"accept", "tier", "dismiss", "cdr"} {
		w := request("POST", "/api/items/i/review", `{"reviewer":" A ","action":"`+action+`","tier":3}`)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
		var response struct {
			Record reviews.Record
			Item   command.Item
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		r := response.Record
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != n+1 {
			t.Fatalf("review %s: got %d durable records, want %d", action, len(lines), n+1)
		}
		var durable reviews.Record
		if err := json.Unmarshal([]byte(lines[n]), &durable); err != nil || durable.ID != r.ID {
			t.Fatalf("durable review differs from response: %+v, %v", durable, err)
		}
		if first == "" {
			first = r.ID
		}
		if r.Live == nil || r.Live.ShoreItemID != "shore-i" || r.Live.ImageForm != "crop" || r.Live.Camera != "Camera" || r.Live.SightingID != "s1" || r.Live.Subtype != "cargo" || r.Live.Unknownness != .7 || r.ModelKey != "live:edge" || r.Model != "Ship score" || r.Reviewer != "A" || r.Directives[0] != c.policy.Text {
			t.Fatalf("%+v", r)
		}
		expected := map[string]int{"accept": 2, "tier": 3, "dismiss": 0, "cdr": 1}[action]
		if r.HumanTier != expected || !response.Item.Reviewed {
			t.Fatalf("%s: %+v", action, response)
		}
		if action == "cdr" && (!response.Item.Sent || response.Item.SentAt == nil || !response.Item.Dismissed) {
			t.Fatal(response.Item)
		}
	}
	if w := request("POST", "/api/reviews/"+first+"/undo", `{"reviewer":"A"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/api/reviews/"+first+"/reason", `{"reviewer":"A","reason":"bird"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/api/reviews/"+first+"/reason", `{"reviewer":"A","reason":"ship","note":"verified"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if l.Effective()[0].Note != "verified" {
		t.Fatal(l.Effective())
	}
	for _, format := range []string{"csv", "jsonl"} {
		w := request("GET", "/api/reviews/export?format="+format, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), first) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if w := request("GET", "/api/reviews/export?format=bad", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for len(l.Effective()) > 0 {
		rs := l.Effective()
		last := rs[len(rs)-1]
		w := request("POST", "/api/reviews/"+last.ID+"/undo", `{"reviewer":"A"}`)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	restored := c.withOverlay(c.base["i"])
	if restored.Manual != 0 || restored.Reviewed || restored.Sent || restored.Dismissed || restored.SentAt != nil {
		t.Fatal(restored)
	}
	if l.Summary().Count != 0 {
		t.Fatal(l.Summary())
	}
	reopened, err := reviews.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Summary().Count != 0 {
		t.Fatal(reopened.Summary())
	}
}

func TestLiveReviewPersistenceFailureDoesNotChangeOverlay(t *testing.T) {
	c := NewConsole(New("http://shore", "", nil))
	c.base["i"] = &command.Item{ID: "i", CentralTier: 2}
	l, err := reviews.Open(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	c.SetReviewLog(l)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/items/i/review", strings.NewReader(`{"reviewer":"A","action":"cdr"}`)))
	if w.Code != 500 || c.overlay["i"] != nil {
		t.Fatalf("%d %+v", w.Code, c.overlay)
	}
}
