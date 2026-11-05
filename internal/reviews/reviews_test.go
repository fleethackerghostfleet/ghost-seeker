package reviews

import (
	"bytes"
	"path/filepath"
	"testing"
)

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
