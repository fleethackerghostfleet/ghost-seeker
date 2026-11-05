package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestReviews(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	l, e := reviews.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	d := store.New(10)
	d.Add(&command.Item{ID: "i", Central: .75, CentralTier: 2, BaseTier: 3, DatasetID: "seaships7000", ImageID: "seaships7000/valid/1", SourceVideo: "SeaShips", SHA256: strings.Repeat("a", 64)}, 0)
	stored, _ := d.GetItem("i")
	s := New(d)
	s.SetReviewLog(l)
	post := func(path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w
	}
	if w := post("/api/items/i/review", `{"reviewer":"  ","action":"accept"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := post("/api/items/i/review", `{"reviewer":" A ","action":"tier","tier":1}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var v struct {
		Record reviews.Record `json:"record"`
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	if v.Record.ModelKey != "maritime" || v.Record.ModelScore != stored.Central || v.Record.ModelTier != stored.CentralTier || v.Record.Reviewer != "A" ||
		v.Record.DatasetID != stored.DatasetID || v.Record.ImageID != stored.ImageID || v.Record.SourceVideo != stored.SourceVideo || v.Record.SHA256 != stored.SHA256 {
		t.Fatalf("%+v", v)
	}
	if w = post("/api/items/i/review", `{"reviewer":"A","action":"accept","model_score":0}`); w.Code != 400 {
		t.Fatal("accepted model injection")
	}
	l.Close()
	l, e = reviews.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	s.SetReviewLog(l)
	if l.Summary().Count != 1 {
		t.Fatal("persistence")
	}
	w = post("/api/reviews/"+v.Record.ID+"/undo", `{"reviewer":"A"}`)
	if w.Code != 200 || l.Summary().Count != 0 {
		t.Fatal(w.Body.String())
	}
	it, _ := d.GetItem("i")
	if it.Reviewed || it.Manual != 0 {
		t.Fatal("undo did not restore")
	}
}

func TestReviewPreservesEscalation(t *testing.T) {
	for _, action := range []string{"tier", "accept", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			l, err := reviews.Open(filepath.Join(t.TempDir(), "log"))
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			d := store.New(10)
			d.Add(&command.Item{ID: "i"}, 0)
			yes := true
			d.PatchItem("i", nil, nil, nil, &yes)
			s := New(d)
			s.SetReviewLog(l)
			w := httptest.NewRecorder()
			tier := 2
			if action == "accept" {
				item, _ := d.GetItem("i")
				tier = item.CentralTier
			}
			s.ServeHTTP(w, httptest.NewRequest("POST", "/api/items/i/review", strings.NewReader(fmt.Sprintf(`{"reviewer":"A","action":%q,"tier":%d}`, action, tier))))
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			item, _ := d.GetItem("i")
			if !item.Sent || !item.Reviewed {
				t.Fatalf("review lost escalation: %+v", item)
			}
		})
	}
}

func TestAcceptRejectsChangedDisplayedPriority(t *testing.T) {
	l, err := reviews.Open(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	d := store.New(10)
	d.Add(&command.Item{ID: "active"}, 0)
	before, _ := d.GetItem("active")
	s := New(d)
	s.SetReviewLog(l)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/items/active/review", strings.NewReader(body)))
		return w
	}
	stale := before.CentralTier%4 + 1
	w := post(fmt.Sprintf(`{"reviewer":"A","action":"accept","tier":%d}`, stale))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "model priority changed") {
		t.Fatalf("stale accept: %d %s", w.Code, w.Body.String())
	}
	after, _ := d.GetItem("active")
	if after.Reviewed || after.Manual != before.Manual || l.Summary().Count != 0 {
		t.Fatalf("stale accept changed item or review log: %+v", after)
	}
	w = post(fmt.Sprintf(`{"reviewer":"A","action":"accept","tier":%d}`, before.CentralTier))
	if w.Code != 200 {
		t.Fatalf("refreshed accept: %d %s", w.Code, w.Body.String())
	}
	after, _ = d.GetItem("active")
	if !after.Reviewed || after.Manual != before.CentralTier || l.Summary().Count != 1 {
		t.Fatalf("refreshed accept was not saved: %+v", after)
	}
	if w = post(`{"reviewer":"A","action":"accept"}`); w.Code != 200 {
		t.Fatalf("legacy accept without tier: %d %s", w.Code, w.Body.String())
	}
}
