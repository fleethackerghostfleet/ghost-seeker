package nlpriority

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type conversationModelRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	ResponseFormat struct {
		JSONSchema struct {
			Name string `json:"name"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

func conversationModelReply(w http.ResponseWriter, content string) {
	writeJSON(w, http.StatusOK, map[string]any{"choices": []any{
		map[string]any{"message": map[string]string{"content": content}},
	}})
}

func TestConversationalRequestsReachGraniteAndKeepVisualQualifiers(t *testing.T) {
	cases := []struct{ text, concept string }{
		{"look for big ships", "big ship"},
		{"keep an eye out for small red fishing vessels", "small red fishing vessel"},
		{"find wooden boats", "wooden boat"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			var scopeCalls, intentCalls, conceptCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body conversationModelRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				switch body.ResponseFormat.JSONSchema.Name {
				case "intent_kind":
					scopeCalls.Add(1)
					conversationModelReply(w, `{"intent":"visual_interest"}`)
				case "priority_intent":
					intentCalls.Add(1)
					if len(body.Messages) < 2 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, tc.text) {
						t.Error("the full operator request was not passed to Granite")
					}
					payload, _ := json.Marshal(map[string]any{"operations": []operation{{Action: "boost", Concept: tc.concept, Weight: 2}}})
					conversationModelReply(w, string(payload))
				case "concept_resolution":
					conceptCalls.Add(1)
					if len(body.Messages) < 2 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, tc.concept) {
						t.Error("a visual qualifier was removed before category resolution")
					}
					conversationModelReply(w, `{"qualifiers":[],"label":"NONE"}`)
				default:
					t.Errorf("unexpected model task %q", body.ResponseFormat.JSONSchema.Name)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			draft, err := (&Compiler{URL: server.URL}).Compile(context.Background(), tc.text, nil)
			if err != nil {
				t.Fatal(err)
			}
			if scopeCalls.Load() != 1 || intentCalls.Load() != 1 || conceptCalls.Load() != 1 {
				t.Fatalf("expected scope, interpretation, and category resolution; got %d, %d and %d calls", scopeCalls.Load(), intentCalls.Load(), conceptCalls.Load())
			}
			if !strings.Contains(draft.Path, "granite") {
				t.Fatalf("model provenance missing: %q", draft.Path)
			}
			if !reflect.DeepEqual(draft.Rules, []Rule{{Concept: tc.concept, Action: "boost", Weight: 2}}) {
				t.Fatalf("visual qualifiers or intent changed: %+v", draft)
			}
		})
	}
}

func TestUnclearConversationalRequestsRemainUnapplied(t *testing.T) {
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		var body conversationModelRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if body.ResponseFormat.JSONSchema.Name != "intent_kind" {
			t.Error("unrelated request reached operation extraction")
		}
		conversationModelReply(w, `{"intent":"other"}`)
	}))
	defer server.Close()
	compiler := &Compiler{URL: server.URL}
	for _, text := range []string{"I am hungry", "can you help me with something", "I feel tired"} {
		if draft, err := compiler.Compile(context.Background(), text, nil); err == nil {
			t.Fatalf("unclear request %q became a policy: %+v", text, draft)
		}
	}
	if modelCalls.Load() != 3 {
		t.Fatalf("natural language should reach the interpreter rather than a command-word whitelist; got %d calls", modelCalls.Load())
	}
}

func TestMalformedModelIntentCannotBeLaunderedByCategoryResolution(t *testing.T) {
	cases := []struct{ name, content string }{
		{"protected control", `{"operations":[{"action":"boost","concept":"human overrides","weight":2}]}`},
		{"invalid weight", `{"operations":[{"action":"boost","concept":"wooden boat","weight":99}]}`},
		{"invalid action", `{"operations":[{"action":"delete","concept":"wooden boat","weight":2}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body conversationModelRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if body.ResponseFormat.JSONSchema.Name == "intent_kind" {
					conversationModelReply(w, `{"intent":"visual_interest"}`)
				} else if body.ResponseFormat.JSONSchema.Name == "priority_intent" {
					conversationModelReply(w, tc.content)
				} else {
					conversationModelReply(w, `{"qualifiers":[],"label":"boat"}`)
				}
			}))
			defer server.Close()
			if draft, err := (&Compiler{URL: server.URL}).Compile(context.Background(), "look for wooden boats", nil); err == nil {
				t.Fatalf("invalid model intent was converted to a valid policy: %+v", draft)
			}
		})
	}
}

func TestConversationalModelFailurePreservesSavedPolicy(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"unavailable", "malformed JSON", "clarification"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body conversationModelRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if body.ResponseFormat.JSONSchema.Name == "intent_kind" {
					conversationModelReply(w, `{"intent":"visual_interest"}`)
					return
				}
				switch failure {
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "malformed JSON":
					conversationModelReply(w, `{"operations":`)
				default:
					conversationModelReply(w, `{"operations":[]}`)
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			service, err := Open(Config{DataDir: dir, Compiler: &Compiler{URL: server.URL}})
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.Preview(ctx, "prioritize cargo ships", "")
			if err != nil {
				t.Fatal(err)
			}
			before, err := service.Apply(ctx, preview.PreviewID)
			if err != nil {
				t.Fatal(err)
			}
			beforeBytes, err := os.ReadFile(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Preview(ctx, "look for big ships", ""); err == nil {
				t.Fatal("failed interpretation produced an actionable preview")
			}
			after, err := service.State(ctx)
			if err != nil {
				t.Fatal(err)
			}
			afterBytes, err := os.ReadFile(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if before.Revision != after.Revision || before.HistoryDepth != after.HistoryDepth || !reflect.DeepEqual(before.Rules, after.Rules) || !bytes.Equal(beforeBytes, afterBytes) {
				t.Fatal("failed interpretation changed the saved policy or undo history")
			}
		})
	}
}

func TestVisualIntentAdmissionRejectsMalformedDecisions(t *testing.T) {
	for _, content := range []string{`{"intent":"other"}`, `{"intent":"unknown"}`, `{}`, `{"intent":"visual_interest","extra":true}`, `{"intent":"visual_interest"} {}`, `not json`} {
		t.Run(content, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conversationModelReply(w, content)
			}))
			defer server.Close()
			if _, err := (&Compiler{URL: server.URL}).Compile(context.Background(), "look for big ships", nil); err == nil {
				t.Fatal("invalid scope decision produced an actionable draft")
			}
			if calls.Load() != 1 {
				t.Fatal("rejected request reached further interpretation")
			}
		})
	}
}

func TestLiveConversationalLanguage(t *testing.T) {
	url := os.Getenv("GRANITE_TEST_URL")
	if url == "" {
		t.Skip("set GRANITE_TEST_URL for actual local IBM Granite integration")
	}
	compiler := &Compiler{URL: url}
	draft, err := compiler.Compile(context.Background(), "look for big ships", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(draft.Rules, []Rule{{Concept: "big ship", Action: "boost", Weight: 2}}) {
		t.Fatalf("the requested size or category was lost: %+v", draft)
	}
	if _, err = compiler.Compile(context.Background(), "I am hungry", nil); err == nil {
		t.Fatal("unrelated conversation produced an actionable priority instruction")
	}
}

func TestNonActionQuestionsAndAmbiguousReferencesCannotInventInterests(t *testing.T) {
	for _, text := range []string{"what is the weather", "why is the sea blue", "look for those ones"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("non-actionable question reached model")
			conversationModelReply(w, `{"operations":[{"action":"boost","concept":"cargo ship","weight":2}]}`)
		}))
		if _, err := (&Compiler{URL: server.URL}).Compile(context.Background(), text, nil); err == nil {
			t.Fatalf("unrelated or ambiguous request accepted: %s", text)
		}
		server.Close()
	}
	draft, err := (&Compiler{}).Compile(context.Background(), "stop looking for fishing boats", []Rule{{Concept: "fishing boat", Action: "boost", Weight: 2}})
	if err != nil || len(draft.Rules) != 0 {
		t.Fatalf("stop looking did not remove existing interest: %+v %v", draft, err)
	}
}
