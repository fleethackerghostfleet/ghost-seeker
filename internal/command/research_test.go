package command

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResearchCompilerAdaptsKnownClassAndIsolatesState(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/compile" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Text  string          `json:"text"`
			Rules json.RawMessage `json:"rules"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Text != "can you find fishing vessels" || string(body.Rules) != "[]" {
			t.Errorf("compiler must send original text and explicit empty rules: %+v", body)
		}
		_, _ = w.Write([]byte(`{"rules":[{"concept":"fishing boat","action":"boost","weight":2}],"vocabulary":[],"path":"granite+granite-concepts","readback":"Prioritize fishing boat (2.0×)","resolutions":[{"input":"fishing vessel","canonical":"fishing boat"}]}`))
	}))
	defer service.Close()
	client, err := NewResearchClient(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &Compiler{Research: client}
	result := compiler.Compile(context.Background(), "can you find fishing vessels")
	want := []Rule{{Type: "boost", Classes: []string{"fishing vessel"}, Attrs: []string{}, Sources: []string{}, Weight: 2}}
	if result.Blocked || !reflect.DeepEqual(result.Rules, want) || !strings.HasPrefix(result.Path, "research/") || len(result.Resolutions) != 2 {
		t.Fatalf("adapted result: %+v", result)
	}
	compiler.Locks = testLocks{warnings: []string{"Locked by higher policy."}, blocked: true}
	if locked := compiler.Compile(context.Background(), "can you find fishing vessels"); !locked.Blocked || len(locked.Warnings) == 0 {
		t.Fatalf("adapter skipped existing policy locks: %+v", locked)
	}
}

func TestResearchCompilerRejectsUnsupportedAndMalformedDrafts(t *testing.T) {
	for _, raw := range []string{
		`{"rules":[{"concept":"red fishing boat","action":"boost","weight":2}]}`,
		`{"rules":[{"concept":"big ship","action":"boost","weight":2}]}`,
		`{"rules":[{"concept":"rescue boat","action":"boost","weight":2}]}`,
		`{"rules":[{"concept":"fishing boat","action":"boost","weight":2},{"concept":"red boat","action":"boost","weight":2}]}`,
		`{"rules":[{"concept":"fishing boat","action":"boost","weight":2},{"concept":"fishing vessel","action":"suppress","weight":1}]}`,
		`{"rules":[{"concept":"fishing boat","action":"delete","weight":2}]}`,
		`{"rules":[{"concept":"fishing boat","action":"boost","weight":0}]}`,
		`{"rules":[{"concept":"fishing boat","action":"boost","weight":9}]}`,
		`{"rules":[],"operation":"undo"}`,
		`{"rules":[]}`,
		`{"path":"granite"}`,
		`{"rules":null}`,
		`{"rules":[{"concept":"fishing boat","action":"boost","weight":2}],"extra":true}`,
		`{"rules":[{"concept":"fishing boat","action":"boost","weight":2}]} {}`,
		`not JSON`,
	} {
		t.Run(raw, func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(raw)) }))
			defer service.Close()
			client, _ := NewResearchClient(service.URL)
			result := (&Compiler{Research: client}).Compile(context.Background(), "prioritize fishing vessels")
			if !result.Blocked || len(result.Rules) != 0 || len(result.Warnings) == 0 || !strings.HasPrefix(result.Warnings[0], "Blocked:") {
				t.Fatalf("unsupported response became actionable or fell back to parser: %+v", result)
			}
		})
	}
}

func TestResearchCompilerTimeoutAndConfiguredFailureBlock(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP failure", true: "timeout"}[slow], func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(200 * time.Millisecond):
					}
				}
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer service.Close()
			client, _ := NewResearchClient(service.URL)
			client.Timeout = 10 * time.Millisecond
			result := (&Compiler{Research: client}).Compile(context.Background(), "prioritize fishing vessels")
			if !result.Blocked || len(result.Rules) != 0 {
				t.Fatalf("configured failure fell back: %+v", result)
			}
		})
	}
	if baseline := (&Compiler{}).Compile(context.Background(), "prioritize fishing vessels"); baseline.Blocked || len(baseline.Rules) != 1 {
		t.Fatalf("default parser changed: %+v", baseline)
	}
}

func TestResearchCompilerEndpointConfiguration(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:8097", "http://127.0.0.1:8097", "http://[::1]:8097", "http://research:8097", "http://host.docker.internal:8097"} {
		if _, err := NewResearchClient(endpoint); err != nil {
			t.Errorf("local endpoint %q: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"https://example.com", "file:///tmp/compiler", "http://localhost:8097/api/compile", "http://user:password@localhost:8097", "http://localhost:8097/?endpoint=other"} {
		if _, err := NewResearchClient(endpoint); err == nil {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
}

func TestResearchCompilerAcceptsEdgeSubtypesOnlyInShoreMode(t *testing.T) {
	compile := func(concept string, edge bool) Compiled {
		service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"rules":[{"concept":"` + concept + `","action":"boost","weight":2}]}`))
		}))
		defer service.Close()
		client, _ := NewResearchClient(service.URL)
		return (&Compiler{Research: client, EdgeSubtypes: edge}).Compile(context.Background(), "watch for "+concept)
	}
	for concept, want := range map[string]string{
		"sailboat": "sailboat", "marine mammal": "marine mammal", "coast guard": "coast guard", "passenger ship": "passenger ship",
		"container ship": "cargo ship", "speedboat": "small boat", "whales": "marine mammal", "ferries": "passenger ship",
		"yacht": "sailboat", "freighter": "cargo ship", "trawlers": "fishing vessel",
	} {
		if result := compile(concept, true); result.Blocked || len(result.Rules) != 1 || !reflect.DeepEqual(result.Rules[0].Classes, []string{want}) {
			t.Fatalf("%s: %+v", concept, result)
		}
	}
	if result := compile("sailboat", false); result.Blocked || !reflect.DeepEqual(result.Rules[0].Classes, []string{"small boat"}) {
		t.Fatalf("sailboat did not use the available simulation class: %+v", result)
	}
	for _, concept := range []string{"whales", "red whale"} {
		result := compile(concept, false)
		if !result.Blocked || len(result.Rules) != 0 {
			t.Fatalf("simulation mode accepted %s: %+v", concept, result)
		}
		if !strings.Contains(result.Readback, "Not supported:") || !strings.Contains(result.Readback, "this sensor can detect:") {
			t.Fatalf("missing unsupported concept readback: %+v", result)
		}
	}
	if result := compile("freighter", false); result.Blocked || !reflect.DeepEqual(result.Rules[0].Classes, []string{"cargo ship"}) {
		t.Fatalf("catalog-class synonym blocked in simulation mode: %+v", result)
	}
}
