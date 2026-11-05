package retune

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestUnsafeCandidateRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	l, err := reviews.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err = l.Append(reviews.Record{ItemID: fmt.Sprintf("sub-%d", i), Reviewer: "A", ModelKey: "maritime", HumanTier: 2, Detections: []command.Detection{{Class: "submarine", Conf: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	r, err := Run([]string{p}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range r.Models {
		if m.Key == "maritime" {
			if m.Accepted || r.Weights[m.Key].W["submarine"] != catalog.Models[m.Key].W["submarine"] {
				t.Fatalf("unsafe proposal accepted: %+v", m)
			}
		}
	}
}

func TestDemoLowersClass(t *testing.T) {
	p := filepath.Join(t.TempDir(), "demo.jsonl")
	if err := Demo(p); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	weights := clone(catalog.Models["maritime"].W)
	r, err := Run([]string{p}, 10)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("input modified")
	}
	if !reflect.DeepEqual(weights, catalog.Models["maritime"].W) {
		t.Fatal("model not restored")
	}
	for _, m := range r.Models {
		if m.Key != "maritime" {
			continue
		}
		if m.TooLittle || !m.Accepted || m.After.Exact <= m.Before.Exact || r.Weights[m.Key].W["debris"] >= weights["debris"] {
			t.Fatalf("unexpected result: %+v\n%s", m, r.Markdown())
		}
	}
}
func TestPrepareFiltersAndMedians(t *testing.T) {
	rs := []reviews.Record{{ID: "1", ItemID: "a", Reviewer: "A", ModelKey: "maritime", HumanTier: 1, Detections: []command.Detection{{Class: "debris", Conf: 1}}, Reason: "debris"}, {ID: "2", ItemID: "a", Reviewer: "B", ModelKey: "maritime", HumanTier: 4, Detections: []command.Detection{{Class: "debris", Conf: 1}}}, {ID: "3", HumanTier: 0}, {ID: "4", HumanTier: 2, Reason: "wrong_detection"}}
	var r Result
	data, err := prepare(rs, &r)
	if err != nil {
		t.Fatal(err)
	}
	if r.Conflicts != 1 || r.Excluded != 2 || data["maritime"][0].Human != 4 || !data["maritime"][0].Credit["debris"] {
		t.Fatalf("%+v %+v", r, data)
	}
	rs[0].ModelKey = "bad"
	if _, err = prepare(rs, &Result{}); err == nil {
		t.Fatal("unknown model accepted")
	}
}
func TestEmptyAndErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	os.WriteFile(p, nil, 0600)
	r, err := Run([]string{p}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range r.Models {
		if !m.TooLittle {
			t.Fatal("fitted empty data")
		}
	}
	if _, err = Run([]string{p}, 0); err == nil {
		t.Fatal("accepted invalid minimum")
	}
	os.WriteFile(p, []byte("bad json\n"), 0600)
	if _, err = Run([]string{p}, 10); err == nil {
		t.Fatal("accepted malformed log")
	}
}
