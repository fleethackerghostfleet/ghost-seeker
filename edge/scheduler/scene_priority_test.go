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

func TestPulledSceneFullKeepsOperatorPriorityAtLowRate(t *testing.T) {
	s, st, _, clk := setup(t, Prioritized)
	ctx := context.Background()
	scene := addImage(t, s, st, clk, "scene", 0, 0, 5000)
	scene.Item.Features.Tags = map[string]string{"event": "scene"}
	if err := st.UpdateItem(ctx, scene.Item); err != nil {
		t.Fatal(err)
	}
	if err := s.ScoreItem(ctx, scene); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []pb.FormKind{pb.FormKind_FORM_KIND_FACT, pb.FormKind_FORM_KIND_THUMB} {
		f := scene.Form(kind)
		if err := st.SetAcked(ctx, scene.Item.Id, kind, f.Bytes, true); err != nil {
			t.Fatal(err)
		}
	}
	ship := addImage(t, s, st, clk, "submarine", .9, .8, 30000)
	if err := st.MarkManifestAcked(ctx, []string{scene.Item.Id, ship.Item.Id}); err != nil {
		t.Fatal(err)
	}
	if err := s.Pull(ctx, &pb.Pull{ItemId: scene.Item.Id, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: "operator-ui"}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pulled *store.Row
	for _, row := range rows {
		if row.Item.Id == scene.Item.Id {
			pulled = row
		}
	}
	if pulled == nil || pulled.Score.Priority <= ship.Score.Priority || pulled.Score.Priority != .0001+s.cfg.PullBoost {
		t.Fatalf("scene lost operator boost: %+v", pulled)
	}
	for _, shipForm := range []pb.FormKind{pb.FormKind_FORM_KIND_FACT, pb.FormKind_FORM_KIND_THUMB, pb.FormKind_FORM_KIND_FULL} {
		for _, row := range rows {
			if row.Item.Id == ship.Item.Id {
				for i := range row.Forms {
					row.Forms[i].Complete = row.Forms[i].Kind < shipForm
				}
			}
		}
		plan := s.BuildPlan(rows, 9600, clk.Now())
		first := scene.Item.Id
		if shipForm == pb.FormKind_FORM_KIND_FACT {
			first = ship.Item.Id
		}
		if len(plan.Queue) != 2 || plan.Queue[0].ItemId != first {
			t.Fatalf("pending ship %v outranked operator scene pull: %+v", shipForm, plan.Queue)
		}
		sentScene := false
		for _, chunk := range plan.Chunks {
			sentScene = sentScene || chunk.ItemId == scene.Item.Id && chunk.Form == pb.FormKind_FORM_KIND_FULL
		}
		if !sentScene {
			t.Fatalf("low-rate budget did not send scene FULL: %+v", plan.Chunks)
		}
	}
	for i := 0; i < 10 && !pulled.Form(pb.FormKind_FORM_KIND_FULL).Complete; i++ {
		plan := s.BuildPlan(rows, 9600, clk.Now())
		for _, chunk := range plan.Chunks {
			if chunk.ItemId == scene.Item.Id {
				form := pulled.Form(chunk.Form)
				form.AckedThrough = chunk.Offset + int64(len(chunk.Data))
				form.Complete = form.AckedThrough == form.Bytes
			}
		}
		clk.Advance(s.cfg.Tick)
	}
	if !pulled.Form(pb.FormKind_FORM_KIND_FULL).Complete {
		t.Fatal("scene FULL did not finish at 9.6 kbps")
	}
}
