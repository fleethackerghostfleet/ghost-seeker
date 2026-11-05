package ingest

import "testing"

func TestFrameLogRingAndFolds(t *testing.T) {
	l := NewFrameLog(3)
	l.add(FrameEvent{Outcome: "new", ItemID: "a", Bytes: 10, thumb: []byte{1}})
	for i := 0; i < 4; i++ {
		l.add(FrameEvent{Outcome: "folded", ItemID: "a", Bytes: 10})
	}
	got, counts, raw := l.Since(0, 10)
	if len(got) != 3 || got[0].Seq != 3 || counts["folded"] != 4 || counts["new"] != 1 || raw != 50 || l.Folds("a") != 4 {
		t.Fatalf("got %+v counts %v raw %d folds %d", got, counts, raw, l.Folds("a"))
	}
	if l.Thumb(1) != nil {
		t.Fatal("evicted frame still has a thumb")
	}
	if got, _, _ := l.Since(4, 10); len(got) != 1 || got[0].Seq != 5 {
		t.Fatalf("since 4: %+v", got)
	}
}
