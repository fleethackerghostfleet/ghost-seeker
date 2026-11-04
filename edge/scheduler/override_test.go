package scheduler

import (
	"context"
	"encoding/json"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestEdgeOverrideResendsFact(t *testing.T) {
	ctx := context.Background()
	s, st, sh, clk := setup(t, Prioritized)
	img := addImage(t, s, st, clk, "", 0, 0.9, 30000)
	for i := 0; i < 3; i++ {
		s.Tick(ctx)
		clk.Advance(s.cfg.Tick)
	}
	if n := countForm(sh, img.Item.Id, pb.FormKind_FORM_KIND_FACT); n != 1 {
		t.Fatalf("want 1 fact chunk before override, got %d", n)
	}
	if err := s.Override(ctx, &pb.LabelOverride{ItemId: img.Item.Id, Label: "whale", By: "oow"}); err != nil {
		t.Fatal(err)
	}
	s.Tick(ctx)
	var last *pb.Chunk
	for _, c := range sh.chunks {
		if c.ItemId == img.Item.Id && c.Form == pb.FormKind_FORM_KIND_FACT {
			last = c
		}
	}
	if countForm(sh, img.Item.Id, pb.FormKind_FORM_KIND_FACT) != 2 || last == nil {
		t.Fatal("fact not re-sent after override")
	}
	var f map[string]any
	if err := json.Unmarshal(last.Data, &f); err != nil || f["override"] != "whale" {
		t.Fatalf("re-sent fact lacks override: %s", last.Data)
	}
	if len(sh.overrides) != 1 || sh.overrides[0].Label != "whale" {
		t.Fatalf("override not uplinked explicitly: %+v", sh.overrides)
	}
	if pend, _ := st.PendingOverrides(ctx); len(pend) != 0 {
		t.Fatalf("override still pending after successful exchange: %+v", pend)
	}
	// An override on an item shore hasn't been told about waits for its manifest.
	fresh := addImage(t, s, st, clk, "", 0, 0.2, 1000)
	sh.overrides = nil
	if err := s.Override(ctx, &pb.LabelOverride{ItemId: fresh.Item.Id, Label: "buoy", By: "oow"}); err != nil {
		t.Fatal(err)
	}
	s.Tick(ctx)
	if len(sh.overrides) != 1 || sh.overrides[0].ItemId != fresh.Item.Id {
		t.Fatalf("override should ride with the manifest in the same exchange: %+v", sh.overrides)
	}
	// Overrides that came down from shore are not echoed back.
	sh.overrides = nil
	if err := s.applyOverride(ctx, &pb.LabelOverride{ItemId: img.Item.Id, Label: "dolphin", By: "shore"}); err != nil {
		t.Fatal(err)
	}
	s.Tick(ctx)
	if len(sh.overrides) != 0 {
		t.Fatalf("shore override echoed back: %+v", sh.overrides)
	}
}

func countForm(sh *fakeShore, id string, k pb.FormKind) int {
	n := 0
	for _, c := range sh.chunks {
		if c.ItemId == id && c.Form == k {
			n++
		}
	}
	return n
}
