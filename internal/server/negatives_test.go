package server

import (
	"encoding/json"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestNegativeReviews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviews.jsonl")
	log, err := reviews.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data := store.New(10)
	data.Add(&command.Item{ID: "negative", DetectorNegative: true, Edge: .1}, 0)
	data.Add(&command.Item{ID: "normal"}, 0)
	stats, _ := data.Stats()
	if stats.Cap != 2 || stats.Keep != 2 || stats.Drop != 0 || stats.DetectorNegatives != 1 {
		t.Fatalf("%+v", stats)
	}
	s := New(data)
	s.SetReviewLog(log)
	post := func(path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w
	}
	for _, tier := range []string{"2", "4"} {
		w := post("/api/items/negative/review", `{"reviewer":"A","action":"tier","tier":`+tier+`}`)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var v struct {
			Record reviews.Record `json:"record"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if !v.Record.DetectorNegative || v.Record.Agreed != (tier == "4") {
			t.Fatalf("%+v", v.Record)
		}
		if (v.Record.Reason == "missed_detection") != (tier == "2") {
			t.Fatalf("%+v", v.Record)
		}
		w = post("/api/reviews/"+v.Record.ID+"/reason", `{"reviewer":"A","reason":"missed_detection"}`)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w := post("/api/items/normal/review", `{"reviewer":"A","action":"accept"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "detector_negative") {
		t.Fatal(w.Body.String())
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	log, err = reviews.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if len(log.Effective()) != 3 {
		t.Fatal("logs failed to reload")
	}
	var old reviews.Record
	if err := json.Unmarshal([]byte(`{"id":"old","human_tier":4}`), &old); err != nil || old.DetectorNegative {
		t.Fatal("old log compatibility")
	}
}
