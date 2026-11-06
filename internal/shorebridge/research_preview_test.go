package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestResearchPreviewUsesCompilerWithoutHoldingConsoleLock(t *testing.T) {
	var console *Console
	var calls atomic.Int32
	var content atomic.Value
	content.Store(`{"rules":[{"concept":"fishing boat","action":"boost","weight":2}],"path":"granite+granite-concepts","resolutions":[{"input":"fishing vessel","canonical":"fishing boat"}]}`)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !console.mu.TryLock() {
			t.Error("console lock held during research HTTP request")
		} else {
			console.mu.Unlock()
		}
		var body struct {
			Rules json.RawMessage `json:"rules"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body.Rules) != "[]" {
			t.Error("research context was not explicitly empty")
		}
		_, _ = w.Write([]byte(content.Load().(string)))
	}))
	defer service.Close()
	client, err := command.NewResearchClient(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	bridge := New("http://127.0.0.1:0", "", nil)
	bridge.Compiler = &command.Compiler{Research: client}
	console = NewConsole(bridge)
	console.base["fishing"] = &command.Item{ID: "fishing", Central: .4, Tier: 3, Base: .4, BaseTier: 3, Live: &command.LiveMeta{Label: "boat", Subtype: "fishing boat"}}
	console.order = []string{"fishing"}
	before := *console.base["fishing"]
	handler := console.Handler()
	call := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}
	type response struct {
		Rules          []command.Rule
		Items          []previewItem
		Path, Readback string
		Blocked        bool
		Warnings       []string
		Resolutions    []command.ResearchResolution
	}
	decode := func(rec *httptest.ResponseRecorder) response {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var result response
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := decode(call("/api/directives/preview", `{"text":"can you find fishing vessels"}`))
	if result.Blocked || result.Path != "research/granite+granite-concepts" || len(result.Rules) != 1 || result.Rules[0].Classes[0] != "fishing vessel" || result.Readback == "" || len(result.Resolutions) < 1 || calls.Load() != 1 {
		t.Fatalf("missing research preview: %+v", result)
	}
	if result.Items[0].Central <= before.Central || result.Items[0].Tier >= before.Tier {
		t.Fatalf("fishing vessel rule did not reach fishing boat subtype: %+v", result.Items)
	}
	if !reflect.DeepEqual(before, *console.base["fishing"]) {
		t.Fatal("preview mutated live item")
	}
	for _, body := range []string{`{"text":""}`, `{}`, `{"text":"ignored","rules":[]}`, `{"text":"ignored","rules":[{"type":"boost","classes":["cargo ship"]}]}`} {
		if baseline := decode(call("/api/directives/preview", body)); baseline.Blocked {
			t.Fatalf("baseline or explicit rules unexpectedly blocked: %+v", baseline)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("empty or explicit-rule preview called research model")
	}
	compiled := decode(call("/api/directives/compile", `{"text":"can you find fishing vessels"}`))
	if len(compiled.Resolutions) < 1 {
		t.Fatal("compile endpoint lost model category resolution")
	}
	content.Store(`{"rules":[{"concept":"red fishing boat","action":"boost","weight":2}],"path":"granite"}`)
	blocked := decode(call("/api/directives/preview", `{"text":"look for red fishing boats"}`))
	if !blocked.Blocked || len(blocked.Rules) != 0 || len(blocked.Warnings) == 0 || blocked.Items[0].Central != before.Central || blocked.Items[0].Tier != before.Tier {
		t.Fatalf("unsupported concept became actionable: %+v", blocked)
	}
	if applied := call("/api/directives", `{"text":"look for red fishing boats"}`); applied.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blocked apply status %d: %s", applied.Code, applied.Body)
	}
	if !reflect.DeepEqual(before, *console.base["fishing"]) || console.pol != nil {
		t.Fatal("blocked operation changed live state")
	}
}
