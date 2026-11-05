package reviews

import (
	"bytes"
	"encoding/csv"
	"path/filepath"
	"testing"
)

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
