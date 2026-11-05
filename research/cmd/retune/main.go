// Command retune proposes weights offline; it never edits catalog source.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/fleethackerghostfleet/ghost-seeker/research/internal/retune"
)

func main() {
	out := flag.String("out", "retune-report.md", "Markdown report path (also printed to stdout)")
	weights := flag.String("weights-out", "proposed-weights.json", "proposed weights JSON path")
	minRecords := flag.Int("min-records", 10, "minimum credited training items per class")
	demo := flag.String("demo", "", "create 300 synthetic reviews at this new path, then tune them")
	flag.Parse()
	paths := flag.Args()
	if *demo != "" {
		paths = append(paths, *demo)
	}
	if len(paths) == 0 {
		log.Fatal("supply review JSONL paths or -demo PATH")
	}
	if err := safeOutputs(paths, *out, *weights); err != nil {
		log.Fatal(err)
	}
	if *demo != "" {
		if err := retune.Demo(*demo); err != nil {
			log.Fatal(err)
		}
	}
	r, err := retune.Run(paths, *minRecords)
	if err != nil {
		log.Fatal(err)
	}
	report := r.Markdown()
	fmt.Print(report)
	if err = os.WriteFile(*out, []byte(report), 0600); err != nil {
		log.Fatal(err)
	}
	b, err := json.MarshalIndent(r.Weights, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(*weights, append(b, '\n'), 0600); err != nil {
		log.Fatal(err)
	}
}

func safeOutputs(inputs []string, outputs ...string) error {
	seen := map[string]bool{}
	for _, p := range append(append([]string{}, inputs...), outputs...) {
		a, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if target, err := filepath.EvalSymlinks(a); err == nil {
			a = target
		}
		if seen[a] {
			return fmt.Errorf("input/output paths must be distinct: %s", p)
		}
		seen[a] = true
	}
	var outputFiles []os.FileInfo
	for _, o := range outputs {
		oi, err := os.Stat(o)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, previous := range outputFiles {
			if os.SameFile(previous, oi) {
				return fmt.Errorf("outputs alias the same file: %s", o)
			}
		}
		outputFiles = append(outputFiles, oi)
		for _, p := range inputs {
			pi, err := os.Stat(p)
			if err == nil && os.SameFile(pi, oi) {
				return fmt.Errorf("output aliases input: %s", o)
			}
		}
	}
	return nil
}
