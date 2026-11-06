//go:build !prod

package retune

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestLatestExclusionSuppressesOlderTier(t *testing.T) {
	for _, reason := range []string{"dismiss", "wrong_detection"} {
		t.Run(reason, func(t *testing.T) {
			old := reviews.Record{ID: "old", ItemID: "item", Reviewer: "A", ModelKey: "maritime", HumanTier: 1, Detections: []command.Detection{{Class: "debris", Conf: 1}}, ReviewedAt: time.Unix(1, 0)}
			excluded := old
			excluded.ID = "excluded"
			excluded.ReviewedAt = time.Unix(2, 0)
			if reason == "dismiss" {
				excluded.HumanTier = 0
			} else {
				excluded.Reason = "wrong_detection"
			}
			other := old
			other.ID = "other"
			other.Reviewer = "B"
			other.HumanTier = 4
			var r Result
			data, err := prepare([]reviews.Record{excluded, other, old}, &r)
			if err != nil {
				t.Fatal(err)
			}
			if len(data["maritime"]) != 1 || data["maritime"][0].Human != 4 || r.Conflicts != 0 || r.Usable != 1 {
				t.Fatalf("old label resurrected: %+v %+v", r, data)
			}
		})
	}
}

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

func TestPrepareGroupsRepeatedImageAcrossItemIDs(t *testing.T) {
	rs := []reviews.Record{
		{ID: "a", ItemID: "item-a", ImageID: "image-42", Reviewer: "A", ModelKey: "maritime", HumanTier: 2, Detections: []command.Detection{{Class: "debris", Conf: 1}}, ReviewedAt: time.Unix(1, 0)},
		{ID: "b", ItemID: "item-b", ImageID: "image-42", Reviewer: "B", ModelKey: "maritime", HumanTier: 3, Detections: []command.Detection{{Class: "debris", Conf: 1}}, ReviewedAt: time.Unix(2, 0)},
	}
	var r Result
	data, err := prepare(rs, &r)
	if err != nil {
		t.Fatal(err)
	}
	examples := data["maritime"]
	if len(examples) != 1 || examples[0].ID != "image-42" {
		t.Fatalf("repeated image split into multiple examples: %+v", examples)
	}
	train, holdout := 0, 0
	for _, example := range examples {
		if Holdout(example.ID) {
			holdout++
		} else {
			train++
		}
	}
	if train+holdout != 1 || (train > 0 && holdout > 0) {
		t.Fatalf("image appeared in both partitions: train=%d holdout=%d", train, holdout)
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
