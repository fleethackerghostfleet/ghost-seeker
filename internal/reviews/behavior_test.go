package reviews

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestReviewActionEffects(t *testing.T) {
	for _, tc := range []struct {
		action                           string
		tier, suggested, wantTier, human int
		dismissed, sent, invalid         bool
	}{
		{"tier", 1, 3, 1, 1, false, false, false},
		{"tier", 4, 1, 4, 4, false, false, false},
		{"accept", 4, 2, 2, 2, false, false, false},
		{"dismiss", 1, 1, 4, 0, true, false, false},
		{"cdr", 4, 4, 1, 1, false, true, false},
		{"tier", 0, 1, 0, 0, false, false, true},
		{"tier", 5, 1, 0, 0, false, false, true},
		{"unknown", 1, 1, 0, 0, false, false, true},
	} {
		effect, err := ReviewEffect(tc.action, tc.tier, tc.suggested)
		if tc.invalid {
			if err == nil {
				t.Fatalf("accepted invalid action %+v", tc)
			}
			continue
		}
		if err != nil || effect.Tier != tc.wantTier || effect.HumanTier != tc.human || (effect.Dismissed != nil) != tc.dismissed || (effect.Sent != nil) != tc.sent {
			t.Fatalf("%+v: effect=%+v err=%v", tc, effect, err)
		}
		if effect.Dismissed != nil && !*effect.Dismissed || effect.Sent != nil && !*effect.Sent {
			t.Fatal("action flag is false")
		}
	}
	for _, tc := range []struct {
		text  string
		limit int
		want  string
	}{{"  船🚢港  ", 2, "船🚢"}, {" abc ", 5, "abc"}, {"abc", 0, ""}, {" \t", 3, ""}} {
		if got := Trim(tc.text, tc.limit); got != tc.want {
			t.Fatalf("Trim(%q)=%q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestSummaryScoresMovementAndReviewerPairs(t *testing.T) {
	l := &Log{}
	if got := l.Summary(); got.Count != 0 || got.Agreement != 0 || got.PairAgreement != 0 || got.Reviewers == nil || got.Classes == nil {
		t.Fatalf("empty summary=%+v", got)
	}
	for i, tier := range []int{1, 1, 2} {
		l.events = append(l.events, Record{ID: string(rune('a' + i)), ItemID: "shared", Reviewer: string(rune('A' + i)), ModelTier: 2, HumanTier: tier, Agreed: tier == 2,
			Detections: []command.Detection{{Class: "person", Conf: .9}, {Class: "boat", Conf: .2}}, Scores: []command.DetectionScore{{Score: .1}, {Score: .8}}})
	}
	l.events = append(l.events, Record{ID: "dismissed", ItemID: "other", Reviewer: "A", HumanTier: 0})
	s := l.Summary()
	if s.Count != 4 || s.Agreed != 1 || s.Agreement != .25 || s.MultiReviewerItems != 1 || s.PairCount != 3 || s.PairAgreement != 1.0/3 || s.Confusion[1][0] != 2 || s.Confusion[1][1] != 1 {
		t.Fatalf("summary=%+v", s)
	}
	if s.Reviewers["A"].Count != 2 || s.Reviewers["C"].Agreement != 1 || s.Classes["boat"].Raised != 2 || s.Classes[""].Lowered != 1 {
		t.Fatalf("aggregation=%+v", s)
	}
}

type rejectingReviewWriter struct{}

func (rejectingReviewWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReviewLogRejectsCorruptionAndPropagatesFailures(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{"malformed": []byte("{broken}\n")} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if l, err := Open(path); err == nil {
			l.Close()
			t.Fatalf("accepted %s log", name)
		}
	}
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(blocker, "log"), dir} {
		if l, err := Open(path); err == nil {
			l.Close()
			t.Fatalf("opened %s", path)
		}
	}
	l, err := Open(filepath.Join(dir, "valid"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Append(Record{ModelScore: math.NaN()}); err == nil || len(l.Effective()) != 0 {
		t.Fatal("non-JSON record changed log")
	}
	if _, err := l.Append(Record{ItemID: "one"}); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "jsonl"} {
		if err := l.Export(rejectingReviewWriter{}, format); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("%s export error=%v", format, err)
		}
	}
	if _, err := l.Correct("missing", "A", "undo", "", ""); err == nil || len(l.Effective()) != 1 {
		t.Fatal("correction of missing ID changed log")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(Record{ItemID: "closed"}); err == nil || len(l.Effective()) != 1 {
		t.Fatal("closed-log append accepted")
	}
}

func TestReadEffectiveMalformedInputAndEmptyInputs(t *testing.T) {
	if rs, n, err := ReadEffective(nil); err != nil || len(rs) != 0 || n != 0 {
		t.Fatalf("empty inputs=%v %d %v", rs, n, err)
	}
	for name, data := range map[string][]byte{"json": []byte("{\"id\":\"valid\"}\n{broken}\n"), "missing-id": []byte("{}\n")} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if rs, n, err := ReadEffective([]string{path}); err == nil || rs != nil || n != 0 {
			t.Fatalf("accepted %s: %v %d %v", name, rs, n, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, data) {
			t.Fatalf("input changed: %v", err)
		}
	}
	paths := []string{filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "z")}
	for i, path := range paths {
		data, err := json.Marshal(Record{ID: "same", Reason: string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, order := range [][]string{paths, {paths[1], paths[0]}} {
		rs, n, err := ReadEffective(order)
		if err != nil || n != 1 || len(rs) != 1 || rs[0].Reason != "b" {
			t.Fatalf("tie merge=%v %d %v", rs, n, err)
		}
	}
}

func TestReviewLogSizeBoundaries(t *testing.T) {
	const limit = 4 << 20
	for _, size := range []int{limit - 1, limit + 1} {
		prefix, suffix := []byte(`{"id":"size-boundary","note":"`), []byte("\"}\n")
		data := append(prefix, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
		data = append(data, suffix...)
		var record Record
		if err := json.Unmarshal(data, &record); err != nil || record.ID != "size-boundary" || len(data) != size {
			t.Fatalf("invalid boundary fixture: size=%d id=%q err=%v", len(data), record.ID, err)
		}
		path := filepath.Join(t.TempDir(), "reviews.jsonl")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		l, err := Open(path)
		if size > limit {
			if err == nil {
				l.Close()
				t.Fatal("Open accepted an oversized valid record")
			}
			if err.Error() != "review log line too large" {
				t.Fatalf("Open rejected oversized input for the wrong reason: %v", err)
			}
		} else {
			if err != nil {
				t.Fatalf("Open rejected a just-under-limit record: %v", err)
			}
			rs := l.Effective()
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			if len(rs) != 1 || rs[0].ID != record.ID || rs[0].Note != record.Note {
				t.Fatal("Open did not preserve the just-under-limit record")
			}
		}
		rs, n, err := ReadEffective([]string{path})
		if size > limit {
			if !errors.Is(err, bufio.ErrTooLong) || rs != nil || n != 0 {
				t.Fatalf("ReadEffective size error: records=%d n=%d err=%v", len(rs), n, err)
			}
		} else if err != nil || n != 1 || len(rs) != 1 || rs[0].ID != record.ID || rs[0].Note != record.Note {
			t.Fatalf("ReadEffective just-under-limit record: records=%d n=%d err=%v", len(rs), n, err)
		}
	}
}
