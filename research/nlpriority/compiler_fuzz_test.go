package nlpriority

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func FuzzCompileInstruction(f *testing.F) {
	for _, text := range []string{"", "prioritize cargo ships", "prioritize rescue boats and show fewer cargo ships", "strongly prioritize rescue boats", "less of those", "don't prioritize cargo ships", "reset", "undo", "prioritize approaching boats", "disable unknown protection", "prioritize cargo ships and suppress cargo ships"} {
		f.Add(text, false)
		f.Add(text, true)
	}
	f.Fuzz(func(t *testing.T, text string, withCurrent bool) {
		if len(text) > 8192 {
			return
		}
		current := []Rule{}
		if withCurrent {
			current = []Rule{{Concept: "cargo ship", Action: "boost", Weight: 2}}
		}
		before := append([]Rule{}, current...)
		compiler := &Compiler{}
		draft, err := compiler.Compile(context.Background(), text, current)
		if !reflect.DeepEqual(current, before) {
			t.Fatal("compile mutated the current rules")
		}
		if len(text) > 512 && err == nil {
			t.Fatal("oversized instruction accepted")
		}
		if err != nil {
			return
		}
		if len(draft.Rules) > 32 || len(draft.Vocabulary) > 16 {
			t.Fatal("draft exceeds rule/vocabulary limits")
		}
		seen := map[string]bool{}
		for _, rule := range draft.Rules {
			if rule.Concept == "" || seen[rule.Concept] || rule.Action != "boost" && rule.Action != "suppress" || math.IsNaN(rule.Weight) || math.IsInf(rule.Weight, 0) || rule.Weight < 1 || rule.Weight > 4 {
				t.Fatalf("invalid compiled rule: %+v", rule)
			}
			seen[rule.Concept] = true
		}
		raw, err := json.Marshal(draft.Rules)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip []Rule
		if err := json.Unmarshal(raw, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, draft.Rules) {
			t.Fatal("compiled rules did not JSON round trip")
		}
		second, err := compiler.Compile(context.Background(), text, current)
		if err != nil || !reflect.DeepEqual(second, draft) {
			t.Fatal("local compilation is not deterministic")
		}
	})
}
