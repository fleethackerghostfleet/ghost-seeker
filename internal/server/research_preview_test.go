package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestResearchPreviewAndApplyUseDemoScorer(t *testing.T) {
	var calls atomic.Int32
	compilerService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["rules"]) != "[]" {
			t.Error("request reused research active policy")
		}
		_, _ = w.Write([]byte(`{"rules":[{"concept":"fishing boat","action":"boost","weight":2}],"path":"granite+granite-concepts","readback":"Prioritize fishing boat"}`))
	}))
	defer compilerService.Close()
	client, _ := command.NewResearchClient(compilerService.URL)
	data := store.New(10)
	for _, item := range []command.Item{
		{ID: "fishing", SourceID: "EO-P7", TS: time.Now(), Dets: []command.Detection{{Class: "fishing vessel", Conf: .9}}},
		{ID: "cargo", SourceID: "EO-P7", TS: time.Now(), Dets: []command.Detection{{Class: "cargo ship", Conf: .9}}},
		{ID: "pinned", SourceID: "EO-P7", TS: time.Now(), Manual: 4, Dets: []command.Detection{{Class: "fishing vessel", Conf: .9}}},
	} {
		command.Rescore(&item, "maritime", nil)
		data.Add(&item, 0)
	}
	app := New(data)
	app.SetCompiler(&command.Compiler{Research: client, Locks: app.PolicyLocks()})
	before, _, _, state := data.Snapshot()
	response := request(app, http.MethodPost, "/api/directives/preview", map[string]any{"text": "can you find fishing vessels"})
	if response.Code != 200 {
		t.Fatalf("preview %d: %s", response.Code, response.Body)
	}
	var preview struct {
		Rules   []command.Rule
		Items   []previewItem
		Path    string
		Blocked bool
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Blocked || len(preview.Rules) != 1 || preview.Rules[0].Classes[0] != "fishing vessel" || preview.Path != "research/granite+granite-concepts" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	afterPreview, _, _, stateAfterPreview := data.Snapshot()
	if !reflect.DeepEqual(before, afterPreview) || !reflect.DeepEqual(state, stateAfterPreview) {
		t.Fatal("preview changed active state")
	}
	predicted := map[string]previewItem{}
	for _, item := range preview.Items {
		predicted[item.ID] = item
	}
	oldFish, _ := data.GetItem("fishing")
	if predicted["fishing"].Central <= oldFish.Central || predicted["fishing"].Central <= predicted["cargo"].Central || predicted["pinned"].Tier != 4 {
		t.Fatalf("demo scores or human override incorrect: %+v", predicted)
	}
	response = request(app, http.MethodPost, "/api/directives/preview", map[string]any{"text": "do not reinterpret this", "rules": preview.Rules})
	if response.Code != 200 || calls.Load() != 1 {
		t.Fatal("explicit rule preview unnecessarily called compiler")
	}
	response = request(app, http.MethodPost, "/api/directives", map[string]any{"text": "can you find fishing vessels"})
	if response.Code != 201 {
		t.Fatalf("apply %d: %s", response.Code, response.Body)
	}
	for id, want := range predicted {
		got, _ := data.GetItem(id)
		if got.Central != want.Central || got.Tier != want.Tier {
			t.Errorf("applied %s score %v/%d differs from preview %v/%d", id, got.Central, got.Tier, want.Central, want.Tier)
		}
	}
}

func TestResearchFailuresNeverMutateDemoState(t *testing.T) {
	for _, failure := range []string{"timeout", "unavailable", "malformed", "unsupported"} {
		t.Run(failure, func(t *testing.T) {
			compilerService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch failure {
				case "timeout":
					select {
					case <-r.Context().Done():
						return
					case <-time.After(200 * time.Millisecond):
					}
				case "unavailable":
					w.WriteHeader(503)
					return
				case "malformed":
					_, _ = w.Write([]byte(`not JSON`))
					return
				}
				_, _ = w.Write([]byte(`{"rules":[{"concept":"red fishing boat","action":"boost","weight":2}],"path":"granite"}`))
			}))
			defer compilerService.Close()
			client, _ := command.NewResearchClient(compilerService.URL)
			client.Timeout = 10 * time.Millisecond
			data := store.New(2)
			data.Add(&command.Item{ID: "fishing", Dets: []command.Detection{{Class: "fishing vessel", Conf: .9}}}, 0)
			data.AddDirective("existing", []command.Rule{{Type: "suppress", Classes: []string{"bird"}}})
			app := New(data)
			app.SetCompiler(&command.Compiler{Research: client})
			before, _, _, state := data.Snapshot()
			preview := request(app, http.MethodPost, "/api/directives/preview", map[string]any{"text": "prioritize fishing vessels"})
			var result struct {
				Blocked  bool
				Rules    []command.Rule
				Warnings []string
			}
			if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if preview.Code != 200 || !result.Blocked || len(result.Rules) != 0 || len(result.Warnings) == 0 {
				t.Fatalf("failure preview %d %+v", preview.Code, result)
			}
			apply := request(app, http.MethodPost, "/api/directives", map[string]any{"text": "prioritize fishing vessels"})
			if apply.Code != 422 {
				t.Fatalf("failure apply %d %s", apply.Code, apply.Body)
			}
			after, _, _, afterState := data.Snapshot()
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(state, afterState) {
				t.Fatal("failed operation changed demo state")
			}
		})
	}
}
