package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/retune"
)

func invokeRetune(t *testing.T, args ...string) string {
	t.Helper()
	oldFlags, oldArgs, oldStdout := flag.CommandLine, os.Args, os.Stdout
	t.Cleanup(func() { flag.CommandLine, os.Args, os.Stdout = oldFlags, oldArgs, oldStdout })
	flag.CommandLine = flag.NewFlagSet("retune", flag.ExitOnError)
	os.Args = append([]string{"retune"}, args...)
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	os.Stdout = f
	main()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRetuneEmptyInputWritesReportAndUnchangedWeights(t *testing.T) {
	dir := t.TempDir()
	input, report, weights := filepath.Join(dir, "reviews.jsonl"), filepath.Join(dir, "report.md"), filepath.Join(dir, "weights.json")
	if err := os.WriteFile(input, nil, 0600); err != nil {
		t.Fatal(err)
	}
	stdout := invokeRetune(t, "-out", report, "-weights-out", weights, input)
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != string(data) || !strings.Contains(stdout, "Records: 0 unique log events; 0 effective reviews") {
		t.Fatal("stdout must equal the empty-input report")
	}
	var proposals map[string]retune.Proposal
	data, err = os.ReadFile(weights)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &proposals); err != nil {
		t.Fatal(err)
	}
	if len(proposals) != len(catalog.Models) || len(proposals) == 0 {
		t.Fatalf("got %d model proposals", len(proposals))
	}
	for key, model := range catalog.Models {
		proposal, ok := proposals[key]
		if !ok || !reflect.DeepEqual(proposal.W, model.W) || !reflect.DeepEqual(proposal.Lift, model.Lift) {
			t.Errorf("empty input changed model %s", key)
		}
		if !strings.Contains(stdout, "## "+key) || !strings.Contains(stdout, "Too little data") {
			t.Errorf("missing model/insufficient-data report for %s", key)
		}
	}
	for _, path := range []string{report, weights} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("output mode = %o", info.Mode().Perm())
		}
	}
	if data, err := os.ReadFile(input); err != nil || len(data) != 0 {
		t.Fatalf("input modified: %v", err)
	}
}

func TestRetuneCommandProcess(t *testing.T) {
	if os.Getenv("GHOST_RETUNE_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"retune"}, os.Args[i+1:]...)
			flag.CommandLine = flag.NewFlagSet("retune", flag.ExitOnError)
			main()
			return
		}
	}
	t.Fatal("missing command arguments separator")
}

func TestRetuneCommandErrors(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"no-input", "missing-input", "invalid-minimum", "malformed-input", "aliased-output", "report-directory", "weights-directory"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input, report, weights := filepath.Join(dir, "input.jsonl"), filepath.Join(dir, "report.md"), filepath.Join(dir, "weights.json")
			if err := os.WriteFile(input, nil, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"-out", report, "-weights-out", weights, input}
			want := ""
			switch name {
			case "no-input":
				args, want = nil, "supply review JSONL paths"
			case "missing-input":
				if err := os.Remove(input); err != nil {
					t.Fatal(err)
				}
				want = "no such file or directory"
			case "invalid-minimum":
				args = append([]string{"-min-records=0"}, args...)
				want = "min-records must be positive"
			case "malformed-input":
				if err := os.WriteFile(input, []byte("{invalid}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "invalid character"
			case "aliased-output":
				args[1], want = input, "input/output paths must be distinct"
			case "report-directory":
				if err := os.Mkdir(report, 0700); err != nil {
					t.Fatal(err)
				}
				want = "is a directory"
			case "weights-directory":
				if err := os.Mkdir(weights, 0700); err != nil {
					t.Fatal(err)
				}
				want = "is a directory"
			}
			cmd := exec.Command(exe, append([]string{"-test.run=^TestRetuneCommandProcess$", "--"}, args...)...)
			cmd.Env = append(os.Environ(), "GHOST_RETUNE_TEST_PROCESS=1")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), want) {
				t.Fatalf("error = %v, stderr = %q, want %q", err, stderr.String(), want)
			}
			if name == "weights-directory" {
				if data, err := os.ReadFile(report); err != nil || string(data) != stdout.String() || len(data) == 0 {
					t.Fatalf("report should be written before weights error: %v", err)
				}
			} else if name != "report-directory" {
				if _, err := os.Stat(report); !os.IsNotExist(err) {
					t.Fatalf("report created before validation: %v", err)
				}
			}
		})
	}
}

func TestSafeOutputsNormalizesPathsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	input, alias := filepath.Join(dir, "input"), filepath.Join(dir, "alias")
	if err := os.WriteFile(input, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(input, alias); err != nil {
		t.Fatal(err)
	}
	for _, outputs := range [][]string{{alias}, {filepath.Join(dir, ".", "input")}, {alias, input}} {
		if err := safeOutputs([]string{input}, outputs...); err == nil {
			t.Fatalf("accepted aliases %v", outputs)
		}
	}
	if err := safeOutputs(nil); err != nil {
		t.Fatal(err)
	}
	if err := safeOutputs([]string{filepath.Join(dir, "missing-input")}, filepath.Join(dir, "new-report")); err != nil {
		t.Fatal(err)
	}
	if err := safeOutputs(nil, filepath.Join(input, "not-a-directory")); err == nil {
		t.Fatal("accepted invalid output parent")
	}
	data, err := os.ReadFile(input)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("input changed: %v", err)
	}
}
