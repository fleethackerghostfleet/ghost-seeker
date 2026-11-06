package ingest

import (
	"context"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestNewContactTagOnlyOnOpen(t *testing.T) {
	for _, event := range []pb.Sighting_Event{pb.Sighting_EVENT_OPEN, pb.Sighting_EVENT_UPDATE, pb.Sighting_EVENT_CLOSE, pb.Sighting_EVENT_DUPLICATE} {
		for _, fresh := range []bool{false, true} {
			t.Run(event.String()+map[bool]string{false: "/old", true: "/new"}[fresh], func(t *testing.T) {
				s := sighting("s1", event, true, "boat")
				s.NewContact = fresh
				an := &scripted{script: map[int][]*pb.Sighting{1: {s}}}
				frame := uint8(1)
				if event == pb.Sighting_EVENT_CLOSE {
					prior := sighting("s1", pb.Sighting_EVENT_OPEN, true, "boat")
					prior.NewContact = true
					an.script[1], an.script[2] = []*pb.Sighting{prior}, []*pb.Sighting{s}
					frame = 2
				}
				g, _, clk := newIngester(t, DedupSightings, an)
				if frame == 2 {
					if _, err := g.Ingest(context.Background(), capture(clk, 1)); err != nil {
						t.Fatal(err)
					}
				}
				row, err := g.Ingest(context.Background(), capture(clk, frame))
				if err != nil {
					t.Fatal(err)
				}
				if event == pb.Sighting_EVENT_DUPLICATE {
					if row != nil {
						t.Fatal("duplicate created an item")
					}
					return
				}
				if row == nil || (row.Item.GetFeatures().GetTags()["new_contact"] == "1") != (event == pb.Sighting_EVENT_OPEN && fresh) {
					t.Fatalf("event=%s fresh=%v row=%v", event, fresh, row)
				}
			})
		}
	}
}

func TestNewContactClearedOnMetadataAndKeyUpdates(t *testing.T) {
	for _, key := range []bool{false, true} {
		open := sighting("s1", pb.Sighting_EVENT_OPEN, true, "boat")
		open.NewContact = true
		update := sighting("s1", pb.Sighting_EVENT_UPDATE, key, "boat")
		update.NewContact = true
		g, _, clk := newIngester(t, DedupSightings, &scripted{script: map[int][]*pb.Sighting{1: {open}, 2: {update}}})
		if _, err := g.Ingest(context.Background(), capture(clk, 1)); err != nil {
			t.Fatal(err)
		}
		row, err := g.Ingest(context.Background(), capture(clk, 2))
		if err != nil || row == nil || row.Item.GetFeatures().GetTags()["new_contact"] != "" {
			t.Fatalf("update key=%v row=%v err=%v", key, row, err)
		}
	}
}

func TestThermalNewContactSurvivesOpenVocabularyMatch(t *testing.T) {
	open := sighting("thermal", pb.Sighting_EVENT_OPEN, true, "thermal contact")
	open.KeyDetection.Label = "thermal contact"
	open.NewContact = true
	an := &scripted{script: map[int][]*pb.Sighting{1: {open}},
		detections: map[int][]*pb.Detection{1: {{Label: "life raft", Confidence: .8}}}}
	g, st, clk := newIngester(t, DedupSightings, an)
	ctx := context.Background()
	if _, _, err := g.sch.ApplyPolicy(ctx, &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"life raft"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Ingest(ctx, capture(clk, 1)); err != nil {
		t.Fatal(err)
	}
	row, err := st.Get(ctx, g.sightings["thermal"])
	if err != nil || row == nil || row.Item.Features.Tags["new_contact"] != "1" || row.Item.Features.Detections[0].Label != "thermal contact" {
		t.Fatalf("thermal OPEN mangled by open-vocabulary handling: %v %v", row, err)
	}
}
