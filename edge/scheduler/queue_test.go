package scheduler

import (
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestQueueIncludesImagesDeferredBehindFacts(t *testing.T) {
	for _, preview := range []bool{false, true} {
		s, st, _, clk := setup(t, Prioritized)
		image := addImage(t, s, st, clk, "boat", .9, 0, 30000)
		metadata := addImage(t, s, st, clk, "tanker", .9, 0, 30000)
		rows := mustRows(t, st)
		for _, row := range rows {
			row.ManifestAcked = true
			fact := row.Form(pb.FormKind_FORM_KIND_FACT)
			if row.Item.Id == image.Item.Id {
				fact.Complete = true
			} else {
				fact.Bytes = 30000
			}
		}
		var plan *Plan
		if preview {
			plan = s.PreviewPlan(rows, 8000, clk.Now())
		} else {
			plan = s.BuildPlan(rows, 8000, clk.Now())
		}
		if len(plan.Queue) != 2 || plan.Queue[0].ItemId != metadata.Item.Id || plan.Queue[0].Form != pb.FormKind_FORM_KIND_FACT || plan.Queue[1].ItemId != image.Item.Id || plan.Queue[1].Form != pb.FormKind_FORM_KIND_THUMB || plan.Queue[1].BytesRemaining != 2000 {
			t.Fatalf("preview=%v: missing deferred image in queue: %+v", preview, plan.Queue)
		}
		for _, chunk := range plan.Chunks {
			if chunk.Form != pb.FormKind_FORM_KIND_FACT {
				t.Fatal("deferred image transmitted before pending FACT")
			}
		}
		if empty := s.BuildPlan([]*store.Row{}, 8000, clk.Now()); len(empty.Queue) != 0 {
			t.Fatal("empty queue has entries")
		}
	}
}
