package nlpriority

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The mapping is a model response fixture, never a production synonym table.
func resolverFixture(t *testing.T, mapping map[string]string) *Compiler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				JSONSchema struct {
					Name string `json:"name"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		if body.ResponseFormat.JSONSchema.Name != "concept_resolution" {
			t.Errorf("unexpected model task %s", body.ResponseFormat.JSONSchema.Name)
			w.WriteHeader(500)
			return
		}
		input := strings.TrimSuffix(strings.TrimPrefix(body.Messages[1].Content, "Input: "), "\nOutput:")
		canonical := "NONE"
		if x, ok := mapping[input]; ok {
			canonical = x
		}
		content, _ := json.Marshal(map[string]any{"qualifiers": []string{}, "label": canonical})
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
	}))
	t.Cleanup(srv.Close)
	return &Compiler{URL: srv.URL}
}

func TestGraniteCategoryEquivalenceChangesActualPriorities(t *testing.T) {
	c := resolverFixture(t, map[string]string{"fishing vessel": "fishing boat"})
	d, err := c.Compile(context.Background(), "prioritize fishing vessels", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != "rules+granite-concepts" || len(d.Resolutions) != 1 || d.Rules[0].Concept != "fishing boat" || len(d.Vocabulary) != 0 {
		t.Fatalf("wrong interpretation %+v", d)
	}
	items := testItems()
	items[0].Subtype = "fishing boat"
	for _, engine := range []string{"edgeml", "tomer"} {
		now := time.Now()
		before, e := Evaluate(context.Background(), items, nil, nil, engine, now, 128000)
		if e != nil {
			t.Fatal(e)
		}
		after, e := Evaluate(context.Background(), items, d.Rules, nil, engine, now, 128000)
		if e != nil {
			t.Fatal(e)
		}
		if priority(after.Items, "rescue").Priority <= priority(before.Items, "rescue").Priority {
			t.Fatalf("%s fishing vessel did not boost fishing boat", engine)
		}
		if priority(after.Items, "cargo").Priority != priority(before.Items, "cargo").Priority {
			t.Fatal("synonym broadened to generic boat")
		}
	}
	items[0].LabelOverride = "cargo ship"
	now := time.Now()
	before, _ := Evaluate(context.Background(), items, nil, nil, "edgeml", now, 128000)
	after, _ := Evaluate(context.Background(), items, d.Rules, nil, "edgeml", now, 128000)
	if priority(before.Items, "rescue").Priority != priority(after.Items, "rescue").Priority {
		t.Fatal("human override ignored")
	}
}

func TestGraniteResolverPreservesNovelDescriptionsAndRemoval(t *testing.T) {
	c := resolverFixture(t, map[string]string{"fishing vessel": "fishing boat", "cargo vessel": "cargo ship"})
	d, e := c.Compile(context.Background(), "prioritize red fishing boats and rescue boats", nil)
	if e != nil {
		t.Fatal(e)
	}
	if d.Rules[0].Concept != "red fishing boat" || d.Rules[1].Concept != "rescue boat" {
		t.Fatal("novel concepts lost qualifiers")
	}
	d, e = c.Compile(context.Background(), "remove fishing vessels", []Rule{{"fishing boat", "boost", 2}})
	if e != nil || len(d.Rules) != 0 {
		t.Fatalf("synonym removal %+v %v", d, e)
	}
	if _, e = c.Compile(context.Background(), "prioritize fishing vessels and suppress fishing boats", nil); e == nil {
		t.Fatal("semantic duplicate contradiction accepted")
	}
	d, e = c.Compile(context.Background(), "more emphasis on fishing vessels", []Rule{{"fishing boat", "boost", 2}})
	if e != nil || d.Rules[0].Weight != 2.5 {
		t.Fatalf("semantic follow-up %+v %v", d, e)
	}
}

func TestOldLiteralRulesResolveOnlyInPreview(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := resolverFixture(t, map[string]string{"fishing vessel": "fishing boat"})
	s, e := Open(Config{DataDir: dir, Compiler: c})
	if e != nil {
		t.Fatal(e)
	}
	items := testItems()
	items[0].Subtype = "fishing boat"
	if _, e = s.Ingest(ctx, items); e != nil {
		t.Fatal(e)
	}
	old := s.data
	old.Active.Rules = []Rule{{"fishing vessel", "boost", 2}}
	old.History = []revision{{Engine: "edgeml", Rules: []Rule{}, Matches: map[string]Match{}}}
	if e = s.save(old); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(Config{DataDir: dir, Compiler: c})
	if e != nil {
		t.Fatal(e)
	}
	state, _ := reopened.State(ctx)
	if state.Rules[0].Concept != "fishing vessel" || len(state.Items) != 2 || state.HistoryDepth != 1 {
		t.Fatal("startup mutated user state")
	}
	p, e := reopened.Preview(ctx, "prioritize fishing vessels", "")
	if e != nil {
		t.Fatal(e)
	}
	if p.Draft.Rules[0].Concept != "fishing boat" {
		t.Fatal(p.Draft)
	}
	state, _ = reopened.State(ctx)
	if state.Rules[0].Concept != "fishing vessel" {
		t.Fatal("preview mutated old rule")
	}
	state, e = reopened.Apply(ctx, p.PreviewID)
	if e != nil || state.Rules[0].Concept != "fishing boat" {
		t.Fatalf("apply %+v %v", state, e)
	}
	state, e = reopened.Undo(ctx)
	if e != nil || state.Rules[0].Concept != "fishing vessel" || len(state.Items) != 2 {
		t.Fatalf("history lost %+v %v", state, e)
	}
}

func TestResolverFailureDoesNotFallBackToLiteral(t *testing.T) {
	if _, e := (&Compiler{}).Compile(context.Background(), "prioritize fishing vessels", nil); e == nil {
		t.Fatal("missing model silently accepted literal")
	}
	c := resolverFixture(t, map[string]string{"fishing vessel": "invented unavailable label"})
	if _, e := c.Compile(context.Background(), "prioritize fishing vessels", nil); e == nil {
		t.Fatal("out-of-catalog model output accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	s, _ := Open(Config{DataDir: t.TempDir(), Compiler: &Compiler{URL: server.URL}})
	if _, e := s.Preview(context.Background(), "prioritize fishing vessels", ""); e == nil {
		t.Fatal("failed resolver produced preview")
	}
	state, _ := s.State(context.Background())
	if len(state.Rules) != 0 {
		t.Fatal("failed resolver mutated active rules")
	}
}

func TestNoMatchWarnings(t *testing.T) {
	s, _ := Open(Config{DataDir: t.TempDir()})
	if _, e := s.Ingest(context.Background(), testItems()); e != nil {
		t.Fatal(e)
	}
	p, e := s.Preview(context.Background(), "prioritize fishing boats", "")
	if e != nil {
		t.Fatal(e)
	}
	text := strings.Join(p.Warnings, " ")
	if !strings.Contains(text, `No images currently match "fishing boat"`) || !strings.Contains(text, "No image priorities changed") {
		t.Fatal(text)
	}
}

// This optional test exercises actual IBM Granite, never a scripted alias map.
func TestLiveGraniteCategoryResolution(t *testing.T) {
	url := os.Getenv("GRANITE_TEST_URL")
	if url == "" {
		t.Skip("set GRANITE_TEST_URL for local model integration")
	}
	c := &Compiler{URL: url}
	cases := map[string]string{"big ships": "big ship", "small fishing vessels": "small fishing vessel", "fishing vessels": "fishing boat", "cargo vessels": "cargo ship", "red fishing boats": "red fishing boat", "rescue boats": "rescue boat"}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			d, e := c.Compile(context.Background(), "prioritize "+input, nil)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(d.Rules, []Rule{{want, "boost", 2}}) {
				t.Fatalf("got %+v want %s", d, want)
			}
		})
	}
	t.Run("legacy literal current rule", func(t *testing.T) {
		d, e := c.Compile(context.Background(), "prioritize fishing vessels", []Rule{{"fishing vessel", "boost", 2}})
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(d.Rules, []Rule{{"fishing boat", "boost", 2}}) {
			t.Fatalf("legacy literal was not resolved: %+v", d)
		}
	})
}

func TestModelReportedQualifierPreservation(t *testing.T) {
	cases := []struct {
		name, input, content, want string
		invalid                    bool
	}{
		{"size preserved", "big ship", `{"qualifiers":["big"],"label":"ship"}`, "NONE", false},
		{"alias retains qualifier", "fishing vessel", `{"qualifiers":["fishing"],"label":"fishing boat"}`, "fishing boat", false},
		{"whole word boundary", "red boat", `{"qualifiers":["red"],"label":"redwing boat"}`, "NONE", false},
		{"unknown retained", "rescue boat", `{"qualifiers":[],"label":"NONE"}`, "NONE", false},
		{"missing qualifiers", "big ship", `{"label":"ship"}`, "", true},
		{"null qualifiers", "big ship", `{"qualifiers":null,"label":"ship"}`, "", true},
		{"hallucinated qualifier", "big ship", `{"qualifiers":["blue"],"label":"ship"}`, "", true},
		{"empty qualifier", "big ship", `{"qualifiers":[""],"label":"ship"}`, "", true},
		{"too many qualifiers", "big ship", `{"qualifiers":["big","big","big","big","big"],"label":"ship"}`, "", true},
		{"raw label validated first", "big ship", `{"qualifiers":["big"],"label":"unavailable"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ResponseFormat struct {
						JSONSchema struct {
							Schema struct {
								Properties json.RawMessage `json:"properties"`
							} `json:"schema"`
						} `json:"json_schema"`
					} `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				properties := string(request.ResponseFormat.JSONSchema.Schema.Properties)
				if strings.Index(properties, `"qualifiers"`) >= strings.Index(properties, `"label"`) {
					t.Error("qualifiers must precede label in schema")
				}
				writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": tc.content}}}})
			}))
			defer server.Close()
			got, err := (&Compiler{URL: server.URL}).resolveOne(context.Background(), tc.input, []string{"ship", "fishing boat", "redwing boat"})
			if tc.invalid {
				if err == nil {
					t.Fatalf("accepted invalid model output %s", tc.content)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
