package console

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
)

func TestItemDownlinksTargetOwnerWhenEdgeUnset(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := &Service{St: st, Clk: clock.NewVirtual(now)}
	const itemID = "01M6MSQ0RSB2E2N3ARPS6QMJHT"
	const unknownID = "01M6MSQ0RSB2E2N3ARPS6QMJHV"
	if err := st.UpsertManifest(ctx, "edge-1", &pb.ManifestEntry{ItemId: itemID}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestPull(ctx, connect.NewRequest(&pb.RequestPullRequest{
		Pull: &pb.Pull{ItemId: itemID},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OverrideLabel(ctx, connect.NewRequest(&pb.OverrideLabelRequest{
		Override: &pb.LabelOverride{ItemId: itemID, Label: "warship", By: "operator"},
	})); err != nil {
		t.Fatal(err)
	}
	edge2, err := st.PendingDownlink(ctx, "edge-2", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(edge2) != 0 {
		t.Fatalf("edge-2 received owned-item downlinks: %+v", edge2)
	}
	edge1, err := st.PendingDownlink(ctx, "edge-1", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(edge1) != 2 {
		t.Fatalf("edge-1 downlinks = %d, want pull and override", len(edge1))
	}
	var sawPull, sawOverride bool
	for _, msg := range edge1 {
		sawPull = sawPull || msg.GetPull() != nil
		sawOverride = sawOverride || msg.GetLabelOverride() != nil
	}
	if !sawPull || !sawOverride {
		t.Fatalf("edge-1 downlinks missing pull or override: %+v", edge1)
	}
	item, err := st.Get(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.LabelOverride != "warship" {
		t.Fatalf("console override = %q, want warship", item.LabelOverride)
	}
	if _, err := svc.RequestPull(ctx, connect.NewRequest(&pb.RequestPullRequest{
		Pull: &pb.Pull{ItemId: unknownID},
	})); err != nil {
		t.Fatal(err)
	}
	edge2, err = st.PendingDownlink(ctx, "edge-2", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(edge2) != 1 || edge2[0].GetPull() == nil || edge2[0].GetPull().ItemId != unknownID {
		t.Fatalf("unknown-item pull was not broadcast: %+v", edge2)
	}
}
