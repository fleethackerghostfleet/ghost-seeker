package rnd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func closeTo(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-10 {
		t.Fatalf("got %g want %g", got, want)
	}
}
func TestCountersReplay(t *testing.T) {
	empty := Summarize(nil)
	if empty.Graded != 0 || empty.MeanAbs != 0 || len(empty.PerSource) != 0 {
		t.Fatal(empty)
	}
	events := []Event{
		{Seq: 1, Kind: "grade", ImageID: "a", Source: "s", ModelTier: 1, HumanTier: 3},
		{Seq: 2, Kind: "grade", ImageID: "b", Source: "s", ModelTier: 4, HumanTier: 1},
		{Seq: 3, Kind: "grade", ImageID: "c", Source: "t", ModelTier: 2, HumanTier: 2},
	}
	s := Summarize(events)
	if s.Graded != 3 || s.Changed != 2 || s.Confusion[0][2] != 1 || s.Confusion[3][0] != 1 || s.PerSource["s"].Graded != 2 {
		t.Fatal(s)
	}
	closeTo(t, s.MeanAbs, 5.0/3)
	closeTo(t, s.MeanChanged, 2.5)
	closeTo(t, s.MeanSigned, -1.0/3)
	closeTo(t, s.ChangedPct, 200.0/3)
	closeTo(t, s.MeanLast20, 5.0/3)
	events = append(events, Event{Seq: 4, Kind: "grade", ImageID: "a", Source: "s", ModelTier: 1, HumanTier: 1})
	s = Summarize(events)
	if s.Graded != 3 || s.Changed != 1 {
		t.Fatal(s)
	}
	events = append(events, Event{Seq: 5, Kind: "undo", Ref: 4})
	s = Summarize(events)
	if s.Graded != 3 || s.Changed != 2 {
		t.Fatal(s)
	}
	events = append(events, Event{Seq: 6, Kind: "undo", Ref: 1})
	if s = Summarize(events); s.Graded != 2 || s.Changed != 1 {
		t.Fatal(s)
	}
}
func TestLast20(t *testing.T) {
	events := []Event{}
	for i := 1; i <= 25; i++ {
		tier := 1
		if i > 5 {
			tier = 3
		}
		events = append(events, Event{Seq: i, Kind: "grade", ImageID: string(rune('a' + i)), Source: "s", ModelTier: 1, HumanTier: tier})
	}
	s := Summarize(events)
	closeTo(t, s.MeanLast20, 2)
	closeTo(t, s.MeanAbs, 1.6)
	closeTo(t, s.MeanSigned, 1.6)
}
func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "set")
	logs := filepath.Join(root, "logs")
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	images := []Image{}
	ps := []Prediction{}
	for i, id := range []string{"a", "b", "c"} {
		images = append(images, Image{ID: id, Source: "source", File: id + ".jpg", Width: 100, Height: 100, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("image")))})
		ps = append(ps, Prediction{ID: id, ModelTier: i + 1, ModelScore: float64(i), Detections: []Detection{}})
		if err := os.WriteFile(filepath.Join(dir, "images", id+".jpg"), []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSONL(filepath.Join(dir, "manifest.jsonl"), images); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(filepath.Join(dir, "predictions.jsonl"), ps); err != nil {
		t.Fatal(err)
	}
	return dir, logs
}
func TestSessionVerifiesImageHash(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = os.WriteFile(filepath.Join(dir, "images", "a.jpg"), []byte("changed image"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSession(dir, logs, "Alice"); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch for image a") {
		t.Fatalf("mismatch accepted: %v", err)
	}
	if err = os.Remove(filepath.Join(dir, "images", "a.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSession(dir, logs, "Alice"); err == nil {
		t.Fatal("missing image accepted")
	}
}
func TestPersistenceUndoResume(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "Alice / test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSession(dir, logs, "Alice / test"); err == nil {
		t.Fatal("concurrent writer accepted")
	}
	if _, err = s.Undo(); err != ErrNoUndo {
		t.Fatal(err)
	}
	if _, err = s.Grade("missing", 1); err == nil {
		t.Fatal("missing accepted")
	}
	if _, err = s.Grade("a", 5); err == nil {
		t.Fatal("invalid accepted")
	}
	if _, err = s.Grade("a", 4); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grade("b", 2); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grade("a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Undo(); err != nil {
		t.Fatal(err)
	}
	path := s.Path()
	s.Close()
	s, err = OpenSession(dir, logs, "Alice / test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := s.State()
	if s.Path() != path || state.Summary.Graded != 2 || state.Summary.Changed != 1 || state.Grades["a"].HumanTier != 4 {
		t.Fatal(state)
	}
	raw, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + "-summary.json")
	if err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err = json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Graded != 2 || summary.Confusion[0][3] != 1 {
		t.Fatal(summary)
	}
	rows, err := readRows[Event](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[3].Kind != "undo" || rows[3].Running.Graded != 2 || rows[0].Delta != 3 || !rows[0].Changed {
		t.Fatal(rows)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.Undo(); err != nil {
			t.Fatal(err)
		}
	}
	if s.State().Summary.Graded != 0 {
		t.Fatal("not clean")
	}
	s.Close()
	if _, err = s.Grade("a", 1); err == nil {
		t.Fatal("closed accepted")
	}
	other, err := OpenSession(dir, logs, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.State().Summary.Graded != 0 || other.Path() == path {
		t.Fatal("reviewer isolation")
	}
}
func TestTornAppendAndCorruption(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grade("a", 2); err != nil {
		t.Fatal(err)
	}
	path := s.Path()
	s.Close()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{partial")
	f.Close()
	s, err = OpenSession(dir, logs, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if s.State().Summary.Graded != 1 {
		t.Fatal("lost complete grade")
	}
	s.Close()
	parts, _ := filepath.Glob(path + ".partial-*")
	if len(parts) != 1 {
		t.Fatal(parts)
	}
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{}\n")
	f.Close()
	if _, err = OpenSession(dir, logs, "Alice"); err == nil {
		t.Fatal("corruption ignored")
	}
}
func TestHTTP(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := s.Handler()
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/", "", 200}, {"GET", "/api/state", "", 200}, {"GET", "/images/a", "", 200}, {"GET", "/images/missing", "", 404},
		{"POST", "/api/undo", "{}", 409}, {"POST", "/api/grade", `{"id":"a","tier":3}`, 200},
		{"POST", "/api/grade", `{"id":"x","tier":1}`, 400}, {"POST", "/api/grade", `{"id":"a","tier":0}`, 400},
		{"POST", "/api/grade", `{"id":"a","tier":1,"extra":1}`, 400}, {"POST", "/api/grade", `{"id":"a","tier":1}{}`, 400},
		{"POST", "/api/grade", "{", 400}, {"POST", "/api/undo", "{}", 200},
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Host = "localhost"
		handler.ServeHTTP(rr, req)
		if rr.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rr.Code, rr.Body.String())
		}
		if tc.path == "/api/state" && tc.method == "GET" {
			var state struct {
				Entries []struct {
					ID string `json:"id"`
				}
			}
			if err = json.Unmarshal(rr.Body.Bytes(), &state); err != nil || state.Entries[0].ID != "a" {
				t.Fatal("missing ID", rr.Body.String())
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/undo", strings.NewReader("{}"))
	req.Host = "localhost"
	req.Header.Set("Origin", "http://evil.invalid")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
	if s.State().Summary.Graded != 0 {
		t.Fatal("undo did not clear")
	}
}
func TestAllowedHosts(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := s.Handler("preview.example", "second.example")
	for _, tc := range []struct {
		host string
		code int
	}{
		{"localhost", 200}, {"localhost:8095", 200}, {"127.0.0.1:8095", 200}, {"[::1]:8095", 200}, {"::1", 200},
		{"preview.example", 200}, {"second.example:8095", 200}, {"attacker.example", 403},
		{"localhost.attacker.example", 403}, {"127.0.0.2", 403}, {"", 403}, {"localhost:8095:80", 403},
	} {
		for _, path := range []string{"/", "/api/state", "/images/a"} {
			req := httptest.NewRequest("GET", path, nil)
			req.Host = tc.host
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.code {
				t.Fatalf("Host=%q path=%s: %d", tc.host, path, rr.Code)
			}
		}
	}
	req := httptest.NewRequest("POST", "/api/grade", strings.NewReader(`{"id":"a","tier":1}`))
	req.Host = "attacker.example"
	req.Header.Set("Origin", "http://attacker.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 403 || s.State().Summary.Graded != 0 {
		t.Fatal("same-origin attacker Host accepted")
	}
}
func TestBadInputs(t *testing.T) {
	dir, logs := fixture(t)
	if _, err := OpenSession(dir, logs, " "); err == nil {
		t.Fatal("empty reviewer")
	}
	if _, err := OpenSession(t.TempDir(), logs, "x"); err == nil {
		t.Fatal("missing metadata")
	}
	if err := os.WriteFile(filepath.Join(dir, "predictions.jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession(dir, logs, "x"); err == nil {
		t.Fatal("empty predictions")
	}
}

func TestReplayRejectsGradeForReplacedFrame(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grade("a", 2); err != nil {
		t.Fatal(err)
	}
	s.Close()
	replacement := []byte("a different frame")
	if err = os.WriteFile(filepath.Join(dir, "images", "a.jpg"), replacement, 0600); err != nil {
		t.Fatal(err)
	}
	images, err := readRows[Image](filepath.Join(dir, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, im := range images {
		if im.ID == "a" {
			im.SHA256 = fmt.Sprintf("%x", sha256.Sum256(replacement))
		}
		b, _ := json.Marshal(im)
		out.Write(b)
		out.WriteByte('\n')
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.jsonl"), []byte(out.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSession(dir, logs, "Alice"); err == nil || !strings.Contains(err.Error(), "invalid grade in journal") {
		t.Fatalf("grade for replaced frame accepted: %v", err)
	}
}
