package reviews

import (
	"os"
	"path/filepath"
	"testing"
)

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
