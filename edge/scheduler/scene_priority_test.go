package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestScenesBelowEveryContact(t *testing.T) {
	s, st, _, clk := setup(t, Prioritized)
	ctx := context.Background()
	scene := addImage(t, s, st, clk, "scene", 0, 1, 8)
	scene.Item.Features.Tags = map[string]string{"event": "scene"}
	scene.PullBoost, scene.PullForm = 100, pb.FormKind_FORM_KIND_FULL
	if err := s.ScoreItem(ctx, scene); err != nil {
		t.Fatal(err)
	}
	engine := scoring.Default()
	for _, kind := range []pb.ItemType{pb.ItemType_ITEM_TYPE_IMAGE, pb.ItemType_ITEM_TYPE_RECORD, pb.ItemType_ITEM_TYPE_TELEMETRY} {
		contact := &pb.Item{Type: kind, CapturedAtUnixMs: clk.Now().Add(-365 * 24 * time.Hour).UnixMilli()}
		if score := engine.Score(contact, &scoring.Context{Now: clk.Now()}, 0); score.Priority <= scene.Score.Priority {
			t.Fatalf("contact %v score=%v scene=%v", kind, score.Priority, scene.Score.Priority)
		}
	}
	for _, label := range []string{"cargo ship", "submarine", "unknown"} {
		contact := addImage(t, s, st, clk, label, .01, 0, 1_000_000)
		contact.Score = &pb.Score{Priority: .001, Suppressed: true}
		contact.ManifestAcked = true
		scene.ManifestAcked = true
		contact.Form(pb.FormKind_FORM_KIND_FACT).Complete = true
		scene.Form(pb.FormKind_FORM_KIND_FACT).Complete = true
		plan := s.BuildPlan([]*store.Row{scene, contact}, 2_000_000, clk.Now())
		if len(plan.Queue) != 2 || plan.Queue[0].ItemId != contact.Item.Id || plan.Queue[1].ItemId != scene.Item.Id {
			t.Fatalf("scene outranked suppressed contact: %+v", plan.Queue)
		}
		contact.ManifestAcked, scene.ManifestAcked = false, false
		plan = s.BuildPlan([]*store.Row{scene, contact}, 2_000_000, clk.Now())
		if len(plan.Manifest) != 2 || plan.Manifest[0].ItemId != contact.Item.Id {
			t.Fatalf("manifest order: %+v", plan.Manifest)
		}
	}
}
