package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestSceneFeedForms(t *testing.T) {
	for _, tc := range []struct {
		name, form, state, image string
		bytes                    int64
	}{
		{name: "missing forms"},
		{name: "summary", form: "fact", state: "received", bytes: 100},
		{name: "offered", form: "thumb", state: "offered", bytes: 100},
		{name: "thumbnail", form: "thumb", state: "received", image: "thumb", bytes: 100},
		{name: "pulled full", form: "full", state: "received", image: "full", bytes: 100},
		{name: "malformed form", form: "unexpected", state: "broken", bytes: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forms := map[string]FormStatus{}
			if tc.form != "" {
				forms[tc.form] = FormStatus{State: tc.state, Bytes: tc.bytes}
			}
			best := &Card{ID: "scene", Source: "Open_ocean_watch", CapturedMs: 1000, Forms: forms}
			if tc.image != "" {
				best.ImageForm, best.Image = tc.image, "/frame.jpg"
			}
			o := &Object{Key: "item:scene", Scene: true, Best: best, LastMs: 1000, Priority: "P4"}
			c := NewConsole(New("", "", nil))
			c.onSnapshot(&Snapshot{Objects: []*Object{o, {Scene: true}}, Counts: Counts{Scenes: 1}}, nil)
			items := c.itemsLocked()
			if len(items) != 1 {
				t.Fatalf("scene-only snapshot: %+v", items)
			}
			it := items[0]
			if it.Tier != 4 || it.CentralTier != 4 || it.BaseTier != 4 || len(it.Det) != 0 || len(it.Dets) != 0 || it.Sent || it.Manual != 0 || it.Live.Event != "scene" || it.Live.Unknown {
				t.Fatalf("unsafe scene card: %+v", it)
			}
			encoded, err := json.Marshal(it)
			if err != nil || !strings.Contains(string(encoded), `"dets":[]`) || !strings.Contains(string(encoded), `"det":[]`) {
				t.Fatalf("nonempty/null detections: %s %v", encoded, err)
			}
			if it.Live.ImageForm != tc.image || c.live.Counts.Objects != 0 || len(c.classes) != 0 || c.cdrLocked() != 0 {
				t.Fatalf("scene affected contacts: %+v", it.Live)
			}
		})
	}
}

func TestSceneFeedDoesNotChangeContactRanks(t *testing.T) {
	contact := view("ship", 1000, map[string]string{"sighting_id": "ship-1"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_THUMB: true})
	baseline, _ := Group([]*pb.ItemView{contact}, nil, nil)
	want := scoreObjects(baseline)[baseline[0]]
	scene := view("scene", 2000, map[string]string{"event": "scene"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_THUMB: true})
	scene.Item.Source = "Open_ocean_watch"
	scene.Score.Priority = 100
	objects, counts := Group([]*pb.ItemView{scene, contact}, nil, nil)
	c := NewConsole(New("", "", nil))
	c.onSnapshot(&Snapshot{Objects: objects, Counts: counts}, nil)
	if len(c.base) != 2 || counts.Objects != 1 || counts.Scenes != 1 || counts.Sightings != 1 {
		t.Fatalf("mixed scene projection: cards=%d counts=%+v", len(c.base), counts)
	}
	for _, o := range objects {
		if !o.Scene && scoreObjects(objects)[o] != want {
			t.Fatal("scene changed contact normalization or rank")
		}
	}
	c.onSnapshot(&Snapshot{}, nil)
	if len(c.base) != 0 {
		t.Fatal("empty snapshot retained scene cards")
	}
}

func TestSceneFeedRejectsContactActions(t *testing.T) {
	c := NewConsole(New("", "", nil))
	scene := view("scene", 1000, map[string]string{"event": "scene"}, nil)
	objects, counts := Group([]*pb.ItemView{scene}, nil, nil)
	c.onSnapshot(&Snapshot{Objects: objects, Counts: counts}, nil)
	log, err := reviews.Open(filepath.Join(t.TempDir(), "reviews.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	c.SetReviewLog(log)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPatch, "/api/items/item:scene", `{"manual":1,"sent":true,"reviewed":true}`},
		{http.MethodPost, "/api/items/item:scene/review", `{"reviewer":"test","action":"cdr"}`},
		{http.MethodPost, "/api/items/item:scene/decision", `{"decision":"track"}`},
		{http.MethodPost, "/api/items/item:scene/full-res", `{"by":"test"}`},
	} {
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	c.overlay["item:scene"] = &overlay{Manual: 1, Sent: true, Reviewed: true}
	if it := c.withOverlay(c.base["item:scene"]); it.Sent || it.Manual != 0 || it.Tier != 4 || c.cdrLocked() != 0 {
		t.Fatalf("stale overlay affected scene: %+v", it)
	}
}
