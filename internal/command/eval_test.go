package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const rulesEvalFloor = 39

type evalCase struct {
	ID     string         `json:"id"`
	Text   string         `json:"text"`
	Expect map[string]any `json:"expect"`
}

type evalResult struct {
	boost, suppress, attrs, sources map[string]bool
	within, duration                int
	only, blocked                   bool
}

func TestEvalRulesPath(t *testing.T) {
	cases := loadEvalCases(t)
	passed, _, _ := runEval(t, &Compiler{}, cases, true)
	if passed < rulesEvalFloor {
		t.Fatalf("rules evaluation scored %d/%d, below floor %d", passed, len(cases), rulesEvalFloor)
	}
}

func TestEvalLLMPath(t *testing.T) {
	baseURL := os.Getenv("GS_LLM_URL")
	if baseURL == "" {
		t.Skip("GS_LLM_URL is not set")
	}
	timeout := 8 * time.Second
	if value := os.Getenv("GS_LLM_TIMEOUT"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	client := NewLLMClient(baseURL, os.Getenv("GS_LLM_MODEL"), timeout)
	client.Cooldown = -1
	cases := loadEvalCases(t)
	passed, _, fallback := runEval(t, &Compiler{LLM: client}, cases, false)
	t.Logf("LLM path: %d/%d (%.1f%%), rules fallbacks: %d", passed, len(cases), percent(passed, len(cases)), fallback)
	if passed < rulesEvalFloor {
		t.Fatalf("LLM evaluation scored %d/48, below rules floor %d", passed, rulesEvalFloor)
	}
}

func loadEvalCases(t *testing.T) []evalCase {
	t.Helper()
	data, err := os.ReadFile("testdata/eval_set.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func runEval(t *testing.T, compiler *Compiler, cases []evalCase, logFields bool) (int, map[string]int, int) {
	t.Helper()
	passed, fallback := 0, 0
	fields := map[string]int{"boost": 0, "suppress": 0, "attrs": 0, "sources": 0, "withinMin": 0, "durationMin": 0, "only": 0, "blocked": 0}
	totals := make(map[string]int, len(fields))
	for _, test := range cases {
		compiled := compiler.Compile(context.Background(), test.Text)
		if compiled.Path == "rules" {
			fallback++
		}
		got := scoreResult(compiled)
		blocked := expectBool(test.Expect, "blocked")
		caseOK := true
		checks := make(map[string]bool)
		checks["blocked"] = got.blocked == blocked
		if blocked {
			caseOK = checks["blocked"]
		} else {
			checks["boost"] = reflect.DeepEqual(got.boost, expectSet(test.Expect, "boost"))
			checks["suppress"] = reflect.DeepEqual(got.suppress, expectSet(test.Expect, "suppress"))
			checks["attrs"] = reflect.DeepEqual(got.attrs, expectSet(test.Expect, "attrs"))
			checks["sources"] = reflect.DeepEqual(got.sources, expectSet(test.Expect, "sources"))
			checks["withinMin"] = got.within == expectInt(test.Expect, "withinMin")
			checks["durationMin"] = got.duration == expectInt(test.Expect, "durationMin")
			checks["only"] = got.only == expectBool(test.Expect, "only")
			for key, ok := range checks {
				if key != "blocked" && !ok {
					caseOK = false
				}
			}
		}
		for key, ok := range checks {
			totals[key]++
			if ok {
				fields[key]++
			}
		}
		if caseOK {
			passed++
		} else {
			t.Logf("case %s failed: expected=%v got=%+v checks=%v path=%s", test.ID, test.Expect, got, checks, compiled.Path)
		}
	}
	t.Logf("%s path: %d/%d (%.1f%%)", map[bool]string{true: "rules", false: "compiled"}[logFields], passed, len(cases), percent(passed, len(cases)))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	accuracies := make([]string, 0, len(keys))
	for _, key := range keys {
		accuracies = append(accuracies, fmt.Sprintf("%s=%d/%d (%.1f%%)", key, fields[key], totals[key], percent(fields[key], totals[key])))
	}
	t.Logf("field accuracies: %s", strings.Join(accuracies, ", "))
	return passed, fields, fallback
}

func scoreResult(compiled Compiled) evalResult {
	got := evalResult{boost: map[string]bool{}, suppress: map[string]bool{}, attrs: map[string]bool{}, sources: map[string]bool{}}
	got.duration, got.blocked = compiled.DurationMin, compiled.Blocked
	for _, rule := range compiled.Rules {
		switch rule.Type {
		case "boost":
			for _, class := range rule.Classes {
				got.boost[class] = true
			}
			for _, attr := range rule.Attrs {
				got.attrs[attr] = true
			}
			got.only = got.only || rule.Only
		case "suppress":
			for _, class := range rule.Classes {
				got.suppress[class] = true
			}
		}
		for _, source := range rule.Sources {
			got.sources[source] = true
		}
		if rule.WithinMin > got.within {
			got.within = rule.WithinMin
		}
	}
	return got
}

func expectSet(expect map[string]any, key string) map[string]bool {
	result := map[string]bool{}
	if values, ok := expect[key].([]any); ok {
		for _, value := range values {
			result[fmt.Sprint(value)] = true
		}
	}
	return result
}

func expectInt(expect map[string]any, key string) int {
	value, _ := expect[key].(float64)
	return int(value)
}

func expectBool(expect map[string]any, key string) bool {
	value, _ := expect[key].(bool)
	return value
}

func percent(value, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}
