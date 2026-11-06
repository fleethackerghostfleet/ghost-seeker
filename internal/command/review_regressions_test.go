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

func TestLiveMixedActionConjunction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeLLMContent(w, `{"rules":[{"type":"boost","classes":["small boat"]}]}`)
	}))
	defer server.Close()
	compiler := &Compiler{LLM: NewLLMClient(server.URL, "test-model", time.Second), EdgeSubtypes: true}
	got := compiler.Compile(context.Background(), "ignore birds and prioritize sailboats")
	t.Log(got.Readback)
	want := []Rule{{Type: "suppress", Classes: []string{"bird"}}, {Type: "boost", Classes: []string{"sailboat"}}}
	if len(got.Rules) != len(want) {
		t.Fatalf("mixed actions merged: %+v", got)
	}
	for i, rule := range got.Rules {
		if rule.Type != want[i].Type || !reflect.DeepEqual(rule.Classes, want[i].Classes) {
			t.Fatalf("wrong action at %d: %+v", i, got)
		}
	}
}

func TestActionConjunctions(t *testing.T) {
	for _, edgeSubtypes := range []bool{false, true} {
		class := "small boat"
		if edgeSubtypes {
			class = "sailboat"
		}
		compiler := &Compiler{EdgeSubtypes: edgeSubtypes}
		for _, tc := range []struct{ action, kind string }{
			{"prioritize", "boost"}, {"prioritise", "boost"}, {"boost", "boost"}, {"show", "boost"}, {"report", "boost"}, {"focus on", "boost"},
			{"ignore", "suppress"}, {"deprioritize", "suppress"}, {"de-prioritize", "suppress"}, {"exclude", "suppress"}, {"suppress", "suppress"},
			{"filter out", "suppress"}, {"skip", "suppress"}, {"hide", "suppress"}, {"less important", "suppress"}, {"lower priority", "suppress"},
			{"don't care", "suppress"}, {"dont care", "suppress"}, {"not interested", "suppress"}, {"no need", "suppress"}, {"downrank", "suppress"},
		} {
			for _, first := range []struct{ action, kind string }{{"ignore", "suppress"}, {"prioritize", "boost"}} {
				text := first.action + " birds and " + tc.action + " sailboats"
				got := compiler.CompileRules(text)
				if len(got.Rules) != 2 || len(got.Unknown) != 0 || got.Rules[0].Type != first.kind || got.Rules[1].Type != tc.kind ||
					!reflect.DeepEqual(got.Rules[0].Classes, []string{"bird"}) || !reflect.DeepEqual(got.Rules[1].Classes, []string{class}) {
					t.Errorf("edgeSubtypes=%v %q: %+v", edgeSubtypes, text, got)
				}
			}
		}
		for _, text := range []string{"", "and", "ignore birds and sailboats"} {
			got := compiler.CompileRules(text)
			if text == "ignore birds and sailboats" {
				if len(got.Rules) != 1 || got.Rules[0].Type != "suppress" || len(got.Rules[0].Classes) != 2 {
					t.Errorf("class list split: %+v", got)
				}
			} else if len(got.Rules) != 0 {
				t.Errorf("empty conjunction: %+v", got)
			}
		}
	}
}

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
	if got := compiler.Compile(context.Background(), "prioritize sailboats and unicorns"); !reflect.DeepEqual(got.Unknown, []string{"unicorns"}) || strings.Contains(got.Readback, "unicorns") {
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

func TestLLMMixedMammalDirective(t *testing.T) {
	for _, edge := range []bool{false, true} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeLLMContent(w, `{"rules":[{"type":"boost","classes":["marine mammal"]},{"type":"suppress","classes":["fishing vessel"]}]}`)
		}))
		compiler := &Compiler{LLM: NewLLMClient(server.URL, "test-model", time.Second), EdgeSubtypes: edge}
		got := compiler.Compile(context.Background(), "prioritize whales; ignore fishin boats")
		server.Close()
		if calls != 1 || got.Path != "llm" || len(got.Rules) != 2 ||
			got.Rules[0].Type != "boost" || !reflect.DeepEqual(got.Rules[0].Classes, []string{"marine mammal"}) ||
			got.Rules[1].Type != "suppress" || !reflect.DeepEqual(got.Rules[1].Classes, []string{"fishing vessel"}) {
			t.Fatalf("edge=%v calls=%d: %+v", edge, calls, got)
		}
	}
}
