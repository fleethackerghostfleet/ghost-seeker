package nlpriority

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
)

func TestCompilerCommandsAndContext(t *testing.T) {
	c := &Compiler{}
	ctx := context.Background()
	d, e := c.Compile(ctx, "prioritize rescue boats and show fewer cargo ships", nil)
	if e != nil {
		t.Fatal(e)
	}
	want := []Rule{{"rescue boat", "boost", 2}, {"cargo ship", "suppress", 1}}
	if !reflect.DeepEqual(d.Rules, want) {
		t.Fatalf("%+v", d)
	}
	d, e = c.Compile(ctx, "remove rescue boats", d.Rules)
	if e != nil || len(d.Rules) != 1 || d.Rules[0].Concept != "cargo ship" {
		t.Fatalf("%+v %v", d, e)
	}
	d, e = c.Compile(ctx, "strongly prioritize rescue boats", nil)
	if e != nil {
		t.Fatal(e)
	}
	d, e = c.Compile(ctx, "less of those", d.Rules)
	if e != nil || d.Rules[0].Weight != 2.5 {
		t.Fatalf("%+v %v", d, e)
	}
	d, e = c.Compile(ctx, "don't prioritize rescue boats", d.Rules)
	if e != nil || len(d.Rules) != 0 {
		t.Fatalf("%+v %v", d, e)
	}
	d, e = c.Compile(ctx, "reset", want)
	if e != nil || len(d.Rules) != 0 || d.Operation != "reset" {
		t.Fatalf("%+v %v", d, e)
	}
}

func TestCompilerRejectsUnsupportedAndConflicting(t *testing.T) {
	c := &Compiler{}
	cases := []string{"prioritize not rescue boats", "prioritize rescue boats and suppress rescue boats", "prioritize approaching boats", "prioritize boats near shore", "only show red boats", "prioritize boats for 20 minutes", "suppress boats with AIS off", "delete low priority images", "disable unknown protection", "more of those", "remove unicorns", "prioritize rescue boats and not cargo ships"}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			if d, e := c.Compile(context.Background(), text, nil); e == nil {
				t.Fatalf("accepted %+v", d)
			}
		})
	}
	if _, e := c.Compile(context.Background(), "more of those", []Rule{{"cargo ship", "boost", 2}, {"rescue boat", "boost", 2}}); e == nil {
		t.Fatal("ambiguous pronoun accepted")
	}
}

func TestGraniteConstrainedOutputAndFailures(t *testing.T) {
	var content = `{"operations":[{"action":"boost","concept":"rescue boat","weight":2}],"clarification":""}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
		}
		var req map[string]any
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Fatal(e)
		}
		if req["response_format"] == nil || req["model"] != "granite-4.0-350m" {
			t.Errorf("missing schema/model: %v", req)
		}
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer srv.Close()
	c := &Compiler{URL: srv.URL}
	d, e := c.Compile(context.Background(), "Give rescue boats a higher priority", nil)
	if e != nil || d.Path != "granite" || d.Rules[0].Concept != "rescue boat" {
		t.Fatalf("%+v %v", d, e)
	}
	for _, bad := range []string{`not json`, `{"operations":[],"clarification":"Which boats?"}`, `{"operations":[{"action":"boost","concept":"rescue boat","weight":100}],"clarification":""}`, `{"operations":[{"action":"boost","concept":"rescue boat","weight":2,"extra":true}],"clarification":""}`, `{"operations":[{"action":"boost","concept":"moving boat","weight":2}],"clarification":""}`} {
		content = bad
		if _, e = c.Compile(context.Background(), "Give rescue boats a higher priority", nil); e == nil {
			t.Fatalf("accepted bad result %s", bad)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.Compile(ctx, "Give rescue boats a higher priority", nil); e == nil {
		t.Fatal("cancelled request accepted")
	}
}

func testItems() []Item {
	now := time.Now().Add(-time.Minute).UnixMilli()
	a, b := .2, .7
	return []Item{{ID: "rescue", Label: "boat", Subtype: "speedboat", Confidence: .8, Unknownness: .1, CapturedAt: now, ThumbBytes: 4096, FullBytes: 32768, TomerScore: &a}, {ID: "cargo", Label: "boat", Subtype: "cargo ship", Confidence: .8, Unknownness: .1, CapturedAt: now, ThumbBytes: 4096, FullBytes: 32768, TomerScore: &b}}
}
func priority(items []RankedItem, id string) RankedItem {
	for _, x := range items {
		if x.ID == id {
			return x
		}
	}
	panic(id)
}
func firstImage(ev Evaluation) string {
	for _, d := range ev.Delivery {
		if d.Form == "THUMB" || d.Form == "FULL" {
			return d.ID
		}
	}
	return ""
}

func TestProductionBaselineAndNovelMatch(t *testing.T) {
	ctx := context.Background()
	items := testItems()
	now := time.Now()
	rules := []Rule{{"rescue boat", "boost", 3}}
	matches := map[string]Match{"rescue": {ID: "rescue", Label: "rescue boat", Accepted: true, Experimental: true, Similarity: .8}}
	for _, engine := range []string{"edgeml", "tomer"} {
		t.Run(engine, func(t *testing.T) {
			baseline, e := Evaluate(ctx, items, nil, nil, engine, now, 128000)
			if e != nil {
				t.Fatal(e)
			}
			eng, _ := scoring.ForPrioritizer(engine)
			for _, it := range items {
				raw := eng.Score(scoringItem(it, nil, nil), &scoring.Context{Policy: Policy(nil), Now: now}, 0)
				if priority(baseline.Items, it.ID).Priority != raw.Priority {
					t.Fatal("baseline drift")
				}
			}
			noMatch, e := Evaluate(ctx, items, rules, nil, engine, now, 128000)
			if e != nil {
				t.Fatal(e)
			}
			if priority(noMatch.Items, "rescue").Priority != priority(baseline.Items, "rescue").Priority || priority(noMatch.Items, "cargo").Priority != priority(baseline.Items, "cargo").Priority {
				t.Fatal("generic boat matched rescue boat")
			}
			after, e := Evaluate(ctx, items, rules, matches, engine, now, 128000)
			if e != nil {
				t.Fatal(e)
			}
			if priority(after.Items, "rescue").Priority <= priority(baseline.Items, "rescue").Priority {
				t.Fatal("accepted concept did not boost")
			}
			if firstImage(after) != "rescue" {
				t.Fatalf("delivery ignored boost: %+v", after.Delivery)
			}
			if priority(after.Items, "cargo").Priority != priority(baseline.Items, "cargo").Priority {
				t.Fatal("unrelated cargo changed")
			}
		})
	}
}

func TestUnknownAndHumanOverrideProtection(t *testing.T) {
	ctx := context.Background()
	items := testItems()
	items[0].Unknownness = .9
	items[1].LabelOverride = "cargo ship"
	matches := map[string]Match{"rescue": {ID: "rescue", Label: "rescue boat", Accepted: true, Experimental: true}, "cargo": {ID: "cargo", Label: "rescue boat", Accepted: true, Experimental: true}}
	for _, engine := range []string{"edgeml", "tomer"} {
		ev, e := Evaluate(ctx, items, []Rule{{"rescue boat", "suppress", 1}}, matches, engine, time.Now(), 128000)
		if e != nil {
			t.Fatal(e)
		}
		if priority(ev.Items, "rescue").Suppressed {
			t.Fatal("unknown suppressed")
		}
		if priority(ev.Items, "cargo").Suppressed || priority(ev.Items, "cargo").ExperimentalLabel != "" {
			t.Fatal("human override lost")
		}
		ev, e = Evaluate(ctx, items, []Rule{{"cargo ship", "suppress", 1}}, matches, engine, time.Now(), 128000)
		if e != nil {
			t.Fatal(e)
		}
		if !priority(ev.Items, "cargo").Suppressed {
			t.Fatal("exact human override rule not applied")
		}
	}
	items[1].LabelOverride = "unknown"
	ev, e := Evaluate(ctx, items, []Rule{{"unknown", "suppress", 1}}, matches, "edgeml", time.Now(), 128000)
	if e != nil {
		t.Fatal(e)
	}
	if priority(ev.Items, "cargo").Suppressed {
		t.Fatal("explicit unknown override suppressed")
	}
}

func TestPreviewApplyUndoRestartAndAtomicFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(Config{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Ingest(ctx, testItems()); e != nil {
		t.Fatal(e)
	}
	p, e := s.Preview(ctx, "prioritize cargo ships", "")
	if e != nil {
		t.Fatal(e)
	}
	state, _ := s.State(ctx)
	if len(state.Rules) != 0 {
		t.Fatal("preview mutated active state")
	}
	stale, e := s.Preview(ctx, "suppress cargo ships", "")
	if e != nil {
		t.Fatal(e)
	}
	state, e = s.Apply(ctx, p.PreviewID)
	if e != nil || state.Revision != 2 || len(state.Rules) != 1 {
		t.Fatalf("%+v %v", state, e)
	}
	if _, e = s.Apply(ctx, stale.PreviewID); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale preview: %v", e)
	}
	reopened, e := Open(Config{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	state, e = reopened.State(ctx)
	if e != nil || len(state.Rules) != 1 || state.HistoryDepth != 1 {
		t.Fatalf("restart: %+v %v", state, e)
	}
	undo, e := reopened.Preview(ctx, "undo", "")
	if e != nil {
		t.Fatal(e)
	}
	state, e = reopened.Apply(ctx, undo.PreviewID)
	if e != nil || len(state.Rules) != 0 || state.HistoryDepth != 0 {
		t.Fatalf("undo: %+v %v", state, e)
	}
	p, e = reopened.Preview(ctx, "prioritize cargo ships", "")
	if e != nil {
		t.Fatal(e)
	}
	blocker := filepath.Join(dir, "not-directory")
	if e = os.WriteFile(blocker, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	reopened.cfg.DataDir = blocker
	if _, e = reopened.Apply(ctx, p.PreviewID); e == nil {
		t.Fatal("persistence failure not surfaced")
	}
	state, _ = reopened.State(ctx)
	if len(state.Rules) != 0 {
		t.Fatal("failed apply mutated active state")
	}
}

func TestNewImagesBetweenPreviewAndApply(t *testing.T) {
	ctx := context.Background()
	s, _ := Open(Config{DataDir: t.TempDir()})
	p, e := s.Preview(ctx, "prioritize cargo ships", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Ingest(ctx, testItems()); e != nil {
		t.Fatal(e)
	}
	state, e := s.Apply(ctx, p.PreviewID)
	if e != nil {
		t.Fatal(e)
	}
	if state.Items[0].ID != "cargo" {
		t.Fatal("new arrivals did not receive active rule")
	}
}

func TestVisionIdentityAndIncompleteResponses(t *testing.T) {
	items := testItems()
	items[0].ModelID = "image-model"
	items[0].Embedding = []float32{1, 0}
	model := "image-model"
	matches := []Match{{ID: "rescue", Label: "rescue boat", Accepted: true, Experimental: true, Similarity: .8}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"modelId": model, "dimension": 2, "matches": matches})
	}))
	defer server.Close()
	v := &Vision{URL: server.URL}
	rules := []Rule{{"rescue boat", "boost", 2}}
	if _, _, e := v.Classify(context.Background(), rules, items); e != nil {
		t.Fatal(e)
	}
	model = "wrong-model"
	if _, _, e := v.Classify(context.Background(), rules, items); e == nil {
		t.Fatal("mismatch accepted")
	}
	model = "image-model"
	matches = nil
	if _, _, e := v.Classify(context.Background(), rules, items); e == nil {
		t.Fatal("omission accepted")
	}
}

func TestInputValidationAndHTTP(t *testing.T) {
	s, _ := Open(Config{DataDir: t.TempDir()})
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/api/compile", strings.NewReader(`{"text":"prioritize cargo ships","rules":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", server.URL+"/api/apply", strings.NewReader(`{"previewId":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://other.example")
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	items := testItems()
	items[0].Embedding = []float32{float32(math.NaN()), 0}
	items[0].ModelID = "x"
	if e = validateItems(items, time.Now()); e == nil {
		t.Fatal("nonfinite embedding accepted")
	}
	items = testItems()
	items[0].Subtype = "__nl_forged__"
	if e = validateItems(items, time.Now()); e == nil {
		t.Fatal("forged marker accepted")
	}
}

func TestFollowUpToUnappliedDraft(t *testing.T) {
	ctx := context.Background()
	s, _ := Open(Config{DataDir: t.TempDir()})
	p, e := s.Preview(ctx, "prioritize rescue boats", "")
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.PreviewFrom(ctx, "more of those", "", p.PreviewID)
	if e != nil {
		t.Fatal(e)
	}
	if second.Draft.Rules[0].Weight != 2.5 {
		t.Fatal(second.Draft)
	}
	state, _ := s.State(ctx)
	if len(state.Rules) != 0 {
		t.Fatal("draft follow-up applied state")
	}
	state, e = s.Apply(ctx, second.PreviewID)
	if e != nil || state.Rules[0].Weight != 2.5 {
		t.Fatalf("%+v %v", state, e)
	}
}

func TestGranitePolarityAndUnrelatedChatGuard(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"operations":[{"action":"boost","concept":"cargo ship","weight":2}]}`}}}})
	}))
	defer srv.Close()
	c := &Compiler{URL: srv.URL}
	for _, text := range []string{"I am hungry", "Make everything perfect"} {
		if _, e := c.Compile(context.Background(), text, nil); e == nil {
			t.Fatal("unrelated request accepted")
		}
	}
	if called {
		t.Fatal("unrelated request reached model")
	}
	if _, e := c.Compile(context.Background(), "Give cargo ships lower priority", nil); e == nil {
		t.Fatal("reversed polarity accepted")
	}
	if !called {
		t.Fatal("model was not exercised")
	}
}

func TestDuplicateIDsAndMixedEncoderBatchAreAtomic(t *testing.T) {
	ctx := context.Background()
	s, _ := Open(Config{DataDir: t.TempDir()})
	items := testItems()
	if _, e := s.Ingest(ctx, items); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Ingest(ctx, items[:1]); e == nil {
		t.Fatal("duplicate replaced source evidence")
	}
	bad := testItems()
	bad[0].ID = "new-a"
	bad[1].ID = "new-b"
	bad[0].Embedding = []float32{1, 0}
	bad[1].Embedding = []float32{0, 1}
	bad[0].ModelID = "model-a"
	bad[1].ModelID = "model-b"
	if _, e := s.Ingest(ctx, bad); e == nil {
		t.Fatal("mixed encoders accepted")
	}
	state, _ := s.State(ctx)
	if len(state.Items) != 2 {
		t.Fatal("invalid batch partially ingested")
	}
}

func TestProtectionCommandsAndCompoundPolarity(t *testing.T) {
	c := &Compiler{}
	for _, text := range []string{"ignore human overrides", "disable unknown protection", "remove human labels", "reset human overrides", "ignore unknown objects", "ignore manual corrections", "suppress operator overrides", "change base scores"} {
		if d, e := c.Compile(context.Background(), text, nil); e == nil {
			t.Fatalf("protection request %q accepted: %+v", text, d)
		}
	}
	for _, text := range []string{"prioritize cargo ships and lower sailboats", "prioritize cargo ships and stop showing red boats"} {
		d, e := c.Compile(context.Background(), text, nil)
		if e != nil {
			t.Fatal(e)
		}
		if len(d.Rules) != 2 || d.Rules[1].Action != "suppress" {
			t.Fatalf("incorrect compound direction: %+v", d)
		}
	}
	for _, text := range []string{"prioritize cargo ships and reduce sailboats", "prioritize cargo ships and put red boats last"} {
		if d, e := c.Compile(context.Background(), text, nil); e == nil {
			t.Fatalf("unparsed command inherited boost: %+v", d)
		}
	}
	if _, e := c.Compile(context.Background(), "prioritize unknown objects", nil); e != nil {
		t.Fatalf("unknown boost was disallowed: %v", e)
	}
}

func TestUnknownBoostUsesExistingSignalAndHonorsOverride(t *testing.T) {
	items := testItems()
	items[0].Unknownness = .9
	items[1].Unknownness = .9
	items[1].LabelOverride = "cargo ship"
	now := time.Now()
	before, e := Evaluate(context.Background(), items, nil, nil, "edgeml", now, 128000)
	if e != nil {
		t.Fatal(e)
	}
	d, e := (&Compiler{}).Compile(context.Background(), "prioritize unknown objects", nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Vocabulary) != 0 {
		t.Fatal("unknown treated as novel visual class")
	}
	after, e := Evaluate(context.Background(), items, d.Rules, nil, "edgeml", now, 128000)
	if e != nil {
		t.Fatal(e)
	}
	if priority(after.Items, "rescue").Priority <= priority(before.Items, "rescue").Priority {
		t.Fatal("unknown not boosted")
	}
	if priority(after.Items, "cargo").Priority != priority(before.Items, "cargo").Priority {
		t.Fatal("known human override received unknown boost")
	}
}
