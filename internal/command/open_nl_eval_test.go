package command

import (
	"os"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

// openNLEvalFloor is the minimum number of open-language corpus cases the
// offline (no LLM) compiler must get exactly right.
const openNLEvalFloor = 0

func loadOpenNLCases(t *testing.T) []evalCase {
	t.Helper()
	cases := loadEvalFile(t, "testdata/open_nl_eval.json")
	if len(cases) < 60 {
		t.Fatalf("open-language corpus has %d cases; want at least 60", len(cases))
	}
	kept := cases[:0]
	for _, test := range cases {
		if _, ok := catalog.Classes[test.Requires]; test.Requires != "" && !ok {
			t.Logf("skip %s: catalog has no %q class", test.ID, test.Requires)
			continue
		}
		kept = append(kept, test)
	}
	return kept
}

func TestEvalOpenNLRulesPath(t *testing.T) {
	cases := loadOpenNLCases(t)
	passed, _, _ := runEval(t, &Compiler{}, cases, true)
	if passed < openNLEvalFloor {
		t.Fatalf("open-language rules evaluation scored %d/%d, below floor %d", passed, len(cases), openNLEvalFloor)
	}
}

// TestEvalOpenNLLLMPath runs the corpus through the configured LLM compiler
// (GS_LLM_PROVIDER=vertex or GS_LLM_URL); it is skipped offline.
func TestEvalOpenNLLLMPath(t *testing.T) {
	client := evalLLMClient(t)
	cases := loadOpenNLCases(t)
	passed, _, fallback := runEval(t, &Compiler{LLM: client}, cases, false)
	t.Logf("open-language LLM path: %d/%d (%.1f%%), rules fallbacks: %d", passed, len(cases), percent(passed, len(cases)), fallback)
}

func evalLLMClient(t *testing.T) *LLMClient {
	t.Helper()
	timeout := 8 * time.Second
	if value := os.Getenv("GS_LLM_TIMEOUT"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	var client *LLMClient
	if os.Getenv("GS_LLM_PROVIDER") == "vertex" {
		client = NewVertexClient(os.Getenv("GS_VERTEX_PROJECT"), os.Getenv("GS_VERTEX_LOCATION"), os.Getenv("GS_LLM_MODEL"), timeout, os.Getenv("GS_VERTEX_TOKEN_CMD"))
	} else if os.Getenv("GS_LLM_URL") != "" {
		client = NewLLMClient(os.Getenv("GS_LLM_URL"), os.Getenv("GS_LLM_MODEL"), timeout)
	}
	if client == nil {
		t.Skip("no LLM configured (GS_LLM_PROVIDER=vertex + GS_VERTEX_PROJECT, or GS_LLM_URL)")
	}
	client.Cooldown = -1
	return client
}
