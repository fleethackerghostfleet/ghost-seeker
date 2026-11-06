package command

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLLMLiveSailboatSpecificity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeLLMContent(w, `{"rules":[{"type":"boost","classes":["small boat"]}]}`)
	}))
	defer server.Close()
	compiler := &Compiler{LLM: NewLLMClient(server.URL, "test-model", time.Second), EdgeSubtypes: true}
	got := compiler.Compile(context.Background(), "prioritize sailboats")
	if len(got.Rules) != 1 || len(got.Rules[0].Classes) != 1 || got.Rules[0].Classes[0] != "sailboat" {
		t.Fatalf("LLM broadened live sailboats: %+v", got)
	}
	for _, tc := range []struct {
		text    string
		classes []string
	}{
		{"prioritize sailboats and small boats", []string{"sailboat", "small boat"}},
		{"report sailing yachts", []string{"sailboat"}},
		{"report whales", []string{"marine mammal"}},
	} {
		got := compiler.Compile(context.Background(), tc.text)
		if len(got.Rules) != 1 || !reflect.DeepEqual(got.Rules[0].Classes, tc.classes) {
			t.Fatalf("%q: %+v", tc.text, got)
		}
	}
	if got := compiler.Compile(context.Background(), "ignore sailboats for 30 minutes"); len(got.Rules) != 1 || got.Rules[0].Type != "suppress" || got.DurationMin != 30 {
		t.Fatalf("action/duration lost: %+v", got)
	}
	if got := compiler.Compile(context.Background(), "prioritize sailboats and unicorns"); !strings.Contains(got.Readback, `Not supported: "unicorns"`) {
		t.Fatalf("unsupported term hidden: %+v", got)
	}
	if got := compiler.Compile(context.Background(), "prioritize small boats"); got.Path != "llm" || got.Rules[0].Classes[0] != "small boat" {
		t.Fatalf("ordinary model path changed: %+v", got)
	}
	compiler.EdgeSubtypes = false
	if got := compiler.Compile(context.Background(), "prioritize sailboats"); got.Path != "llm" || got.Rules[0].Classes[0] != "small boat" {
		t.Fatalf("simulation model path changed: %+v", got)
	}
}
