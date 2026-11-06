//go:build !prod

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/retune"
)

func TestRetuneDemoCreatesNonemptyReviewsAndProposals(t *testing.T) {
	dir := t.TempDir()
	input, report, weights := filepath.Join(dir, "demo.jsonl"), filepath.Join(dir, "report.md"), filepath.Join(dir, "weights.json")
	stdout := invokeRetune(t, "-demo", input, "-out", report, "-weights-out", weights)
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(data)), "\n")); got != 300 {
		t.Fatalf("demo reviews = %d", got)
	}
	if !strings.Contains(stdout, "Records: 300 unique log events; 300 effective reviews; 300 usable labels") {
		t.Fatal("demo did not feed usable reviews into retuning")
	}
	trained := false
	for _, match := range regexp.MustCompile(`Train items: ([0-9]+); holdout items: ([0-9]+)`).FindAllStringSubmatch(stdout, -1) {
		train, _ := strconv.Atoi(match[1])
		holdout, _ := strconv.Atoi(match[2])
		trained = trained || train > 0 && holdout > 0
	}
	if !trained {
		t.Fatal("demo did not exercise both training and holdout data")
	}
	data, err = os.ReadFile(report)
	if err != nil || string(data) != stdout {
		t.Fatalf("report/stdout mismatch: %v", err)
	}
	data, err = os.ReadFile(weights)
	if err != nil {
		t.Fatal(err)
	}
	var proposals map[string]retune.Proposal
	if err := json.Unmarshal(data, &proposals); err != nil {
		t.Fatal(err)
	}
	if len(proposals["maritime"].W) == 0 {
		t.Fatal("no maritime weights proposed")
	}
}

func TestRetuneDemoNeverOverwritesExistingReviews(t *testing.T) {
	dir := t.TempDir()
	input, report, weights := filepath.Join(dir, "demo.jsonl"), filepath.Join(dir, "report.md"), filepath.Join(dir, "weights.json")
	if err := os.WriteFile(input, []byte("preserve reviews"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestRetuneCommandProcess$", "--", "-demo", input, "-out", report, "-weights-out", weights)
	cmd.Env = append(os.Environ(), "GHOST_RETUNE_TEST_PROCESS=1")
	data, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(string(data), "file exists") {
		t.Fatalf("existing demo path: %v, %s", err, data)
	}
	data, err = os.ReadFile(input)
	if err != nil || string(data) != "preserve reviews" {
		t.Fatalf("existing reviews modified: %v", err)
	}
	for _, path := range []string{report, weights} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("output created after demo failure: %v", err)
		}
	}
}
