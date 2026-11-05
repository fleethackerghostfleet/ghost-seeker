package reviews

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnrelatedCorrectionDoesNotResurrectStaleLabel(t *testing.T) {
	dir := t.TempDir()
	raw, export, corrections := filepath.Join(dir, "raw"), filepath.Join(dir, "export"), filepath.Join(dir, "corrections")
	target := Record{ID: "target", ItemID: "item", Reviewer: "A", HumanTier: 1, ReviewedAt: time.Unix(10, 0)}
	updated := target
	updated.Reason = "wrong_detection"
	write := func(p string, rs ...Record) {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, r := range rs {
			if err = json.NewEncoder(f).Encode(r); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(raw, target, Record{ID: "other", ItemID: "other"}, Record{ID: "unrelated", Ref: "other", Action: "reason", Reason: "debris"})
	write(export, updated)
	for _, paths := range [][]string{{raw, export}, {export, raw}} {
		rs, _, err := ReadEffective(paths)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.ID == target.ID && r.Reason != "wrong_detection" {
				t.Fatalf("stale label resurrected: %+v", r)
			}
		}
	}
	write(corrections, Record{ID: "target-correction", Ref: target.ID, Action: "reason", Reason: "debris", ReviewedAt: time.Unix(1, 0)})
	for _, paths := range [][]string{{raw, export, corrections}, {corrections, export, raw}} {
		rs, _, err := ReadEffective(paths)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.ID == target.ID && r.Reason != "debris" {
				t.Fatalf("cross-input correction not replayed: %+v", r)
			}
		}
	}
}

func TestMergeRawAndCorrectedExport(t *testing.T) {
	dir := t.TempDir()
	raw, export := filepath.Join(dir, "raw"), filepath.Join(dir, "export")
	l, err := Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := l.Append(Record{ItemID: "a", Reviewer: "A"})
	l.Correct(a.ID, "A", "reason", "debris", "corrected")
	f, err := os.Create(export)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Export(f, "jsonl"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	l.Close()
	for _, paths := range [][]string{{raw, export}, {export, raw}} {
		rs, n, err := ReadEffective(paths)
		if err != nil || n != 2 || len(rs) != 1 || rs[0].Reason != "debris" || rs[0].Note != "corrected" {
			t.Fatalf("%+v %d %v", rs, n, err)
		}
	}
}

func TestReadEffective(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	l, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := l.Append(Record{ItemID: "a", Reviewer: "A"})
	b, _ := l.Append(Record{ItemID: "b", Reviewer: "A"})
	l.Correct(a.ID, "A", "reason", "debris", "note")
	l.Correct(b.ID, "A", "undo", "", "")
	l.Close()
	before, _ := os.ReadFile(p)
	rs, n, err := ReadEffective([]string{p, p})
	if err != nil || n != 4 || len(rs) != 1 || rs[0].Reason != "debris" {
		t.Fatalf("%+v %d %v", rs, n, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("input modified")
	}
	if _, _, err = ReadEffective([]string{p + "missing"}); err == nil {
		t.Fatal("missing input accepted")
	}
}
