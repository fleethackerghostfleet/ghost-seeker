package reviews

import (
	"bytes"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failingFile struct {
	*os.File
	writeFail, syncFail bool
}

func (f *failingFile) Write(b []byte) (int, error) {
	if f.writeFail {
		f.writeFail = false
		n, _ := f.File.Write(b[:len(b)/2])
		return n, errors.New("write failed")
	}
	return f.File.Write(b)
}
func (f *failingFile) Sync() error {
	if f.syncFail {
		f.syncFail = false
		return errors.New("sync failed")
	}
	return f.File.Sync()
}

func TestAppendRollback(t *testing.T) {
	for _, failure := range []string{"write", "sync"} {
		t.Run(failure, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "log")
			l, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			l.Append(Record{ItemID: "original"})
			before, _ := os.ReadFile(p)
			l.f = &failingFile{File: l.f.(*os.File), writeFail: failure == "write", syncFail: failure == "sync"}
			if _, err = l.Append(Record{ItemID: "failed"}); err == nil {
				t.Fatal("expected failure")
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) || len(l.Effective()) != 1 {
				t.Fatal("failed append changed log")
			}
			if _, err = l.Append(Record{ItemID: "next"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenTruncatedTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	l, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	l.Append(Record{ItemID: "original"})
	l.Close()
	before, _ := os.ReadFile(p)
	os.WriteFile(p, append(before, []byte(`{"id":"partial`)...), 0600)
	l, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Effective()) != 1 {
		t.Fatal("lost valid prefix")
	}
	if _, err = l.Append(Record{ItemID: "next"}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if len(l.Effective()) != 2 {
		t.Fatal("append after recovery failed")
	}
}

func TestOpenPreservesValidUnterminatedTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	l, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.Append(Record{ItemID: "valid"})
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, bytes.TrimSuffix(b, []byte{'\n'}), 0600); err != nil {
		t.Fatal(err)
	}
	l, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Effective(); len(got) != 1 || got[0].ID != r.ID {
		t.Fatal("valid final record lost")
	}
	repaired, _ := os.ReadFile(p)
	if !bytes.Equal(repaired, b) {
		t.Fatal("missing newline not repaired")
	}
	if _, err = l.Append(Record{ItemID: "next"}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if len(l.Effective()) != 2 {
		t.Fatal("append after repair failed")
	}
}

func TestCSVNeutralizesFormulas(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, prefix := range []string{"=", "+", "-", "@", "\t", "\r"} {
		value := prefix + "formula"
		if _, err = l.Append(Record{ItemID: value, Reviewer: value, Model: value, Reason: value, Note: value}); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	if err = l.Export(&b, "csv"); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows[1:] {
		for _, i := range []int{1, 2, 4, 8, 9} {
			if row[i][0] != '\'' {
				t.Fatalf("unsafe cell: %q", row[i])
			}
		}
	}
}

func TestReplay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "reviews.jsonl")
	l, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	if l.Summary().Count != 0 {
		t.Fatal("not empty")
	}
	a, e := l.Append(Record{ItemID: "i", Reviewer: "A", ModelTier: 2, HumanTier: 1})
	if e != nil {
		t.Fatal(e)
	}
	l.Append(Record{ItemID: "i", Reviewer: "B", ModelTier: 2, HumanTier: 1})
	if _, e = l.Correct(a.ID, "B", "undo", "", ""); e == nil {
		t.Fatal("accepted wrong owner")
	}
	l.Correct(a.ID, "A", "reason", "boat", "note")
	l.Close()
	l, e = Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	s := l.Summary()
	if s.Count != 2 || s.PairAgreement != 1 || s.Confusion[1][0] != 2 {
		t.Fatalf("%+v", s)
	}
	if l.Effective()[0].Reason != "boat" {
		t.Fatal("lost reason")
	}
	for _, f := range []string{"csv", "jsonl"} {
		var b bytes.Buffer
		if e = l.Export(&b, f); e != nil || b.Len() == 0 {
			t.Fatal(e)
		}
	}
	if _, e = l.Correct(a.ID, "A", "undo", "", ""); e != nil {
		t.Fatal(e)
	}
	if l.Summary().Count != 1 {
		t.Fatal("undo failed")
	}
}
