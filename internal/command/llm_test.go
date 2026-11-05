package command

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLLMSchemaAndCanonicalization(t *testing.T) {
	var path string
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&request)
		writeLLMContent(w, `{"rules":[{"type":"boost","classes":["boats","bird"]}],"durationMin":null}`)
	}))
	defer server.Close()
	client := NewLLMClient(server.URL+"/v1/", "test-model", time.Second)
	client.Cooldown = -1
	compiled := (&Compiler{LLM: client}).Compile(context.Background(), "prioritize boats")
	if compiled.Path != "llm" || path != "/v1/chat/completions" {
		t.Fatalf("compiled=%+v path=%q", compiled, path)
	}
	if len(compiled.Rules) != 1 || len(compiled.Rules[0].Classes) != 2 ||
		compiled.Rules[0].Classes[0] != "small boat" || compiled.Rules[0].Classes[1] != "bird" {
		t.Fatalf("canonical rules = %#v", compiled.Rules)
	}
	format, _ := request["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("response_format = %#v", format)
	}
}

func TestLLMJSONModeRetry(t *testing.T) {
	var requests atomic.Int32
	formats := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		format, _ := body["response_format"].(map[string]any)
		formats = append(formats, fmt.Sprint(format["type"]))
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeLLMContent(w, `{"rules":[{"type":"boost","classes":["bird"]}]}`)
	}))
	defer server.Close()
	client := NewLLMClient(server.URL, "", time.Second)
	client.Cooldown = -1
	compiled := (&Compiler{LLM: client}).Compile(context.Background(), "birds")
	if compiled.Path != "llm" || requests.Load() != 2 || strings.Join(formats, ",") != "json_schema,json_object" {
		t.Fatalf("path=%s requests=%d formats=%v", compiled.Path, requests.Load(), formats)
	}
}

func TestLLMFallbacksAndValidationDoesNotTripCircuit(t *testing.T) {
	tests := []struct {
		name    string
		content string
		text    string
	}{
		{"bad json", `{"rules":`, "small boats"},
		{"unknown class", `{"rules":[{"type":"boost","classes":["spaceship"]}]}`, "small boats"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				writeLLMContent(w, test.content)
			}))
			defer server.Close()
			client := NewLLMClient(server.URL, "", time.Second)
			compiled := (&Compiler{LLM: client}).Compile(context.Background(), test.text)
			if compiled.Path != "rules" || len(compiled.Warnings) == 0 {
				t.Fatalf("fallback = %+v", compiled)
			}
			expectedRules, expectedUnknown := ParseDirectiveDetailed(test.text)
			if !reflect.DeepEqual(compiled.Rules[0].Classes, expectedRules[0].Classes) ||
				!reflect.DeepEqual(compiled.Unknown, expectedUnknown) {
				t.Fatalf("fallback rules=%#v unknown=%v, parser rules=%#v unknown=%v", compiled.Rules, compiled.Unknown, expectedRules, expectedUnknown)
			}
			if test.name == "unknown class" {
				(&Compiler{LLM: client}).Compile(context.Background(), test.text)
				if requests.Load() != 2 {
					t.Fatalf("validation failure tripped circuit; requests=%d", requests.Load())
				}
			}
		})
	}
}

func TestLLMEmptyDraftFallbackReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeLLMContent(w, `{"rules":[]}`)
	}))
	defer server.Close()
	compiled := (&Compiler{LLM: NewLLMClient(server.URL, "", time.Second)}).Compile(context.Background(), "small boats")
	if compiled.Path != "rules" || len(compiled.Warnings) == 0 || !strings.Contains(compiled.Warnings[0], "(no rules)") {
		t.Fatalf("empty draft result = %+v", compiled)
	}
}

func TestLLMTimeoutAndCircuitBreaker(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(200 * time.Millisecond)
		writeLLMContent(w, `{"rules":[]}`)
	}))
	defer server.Close()
	client := NewLLMClient(server.URL, "", 50*time.Millisecond)
	compiler := &Compiler{LLM: client}
	start := time.Now()
	first := compiler.Compile(context.Background(), "small boats")
	if first.Path != "rules" || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("timeout result=%+v elapsed=%s", first, time.Since(start))
	}
	start = time.Now()
	second := compiler.Compile(context.Background(), "small boats")
	if second.Path != "rules" || time.Since(start) > 20*time.Millisecond || requests.Load() != 1 {
		t.Fatalf("circuit result=%+v elapsed=%s requests=%d", second, time.Since(start), requests.Load())
	}
	if !strings.Contains(second.Warnings[0], "circuit open") {
		t.Fatalf("fallback warning = %q", second.Warnings[0])
	}
}

func TestLLMHTTPFailureFenceAndNil(t *testing.T) {
	t.Run("http failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		got := (&Compiler{LLM: NewLLMClient(server.URL, "", time.Second)}).Compile(context.Background(), "small boats")
		if got.Path != "rules" || !strings.Contains(got.Warnings[0], "HTTP 500") {
			t.Fatalf("fallback = %+v", got)
		}
	})
	t.Run("fenced json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeLLMContent(w, "```json\n"+`{"rules":[{"type":"boost","classes":["bird"]}]}`+"\n```")
		}))
		defer server.Close()
		got := (&Compiler{LLM: NewLLMClient(server.URL, "", time.Second)}).Compile(context.Background(), "birds")
		if got.Path != "llm" || got.Rules[0].Classes[0] != "bird" {
			t.Fatalf("fenced response = %+v", got)
		}
	})
	if NewLLMClient("", "", time.Second) != nil {
		t.Fatal("empty base URL should disable LLM")
	}
	if got := (&Compiler{}).Compile(context.Background(), "small boats"); got.Path != "rules" {
		t.Fatalf("zero-value compiler path = %q", got.Path)
	}
}

func writeLLMContent(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
	})
}
