package policy

import (
	"context"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestShipConsoleLayerSurvivesDownlinkAndKeepsFlatInterest(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store, err := NewStore(ctx, NewMemBackend(), Options{TTL: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	flat := &pb.Policy{Id: "scenario", Interest: &pb.Interest{SuppressLabels: []string{"cruise ship"}}}
	if _, err := store.Accept(ctx, flat, "downlink", t0); err != nil {
		t.Fatal(err)
	}
	ship := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, Rules: []*pb.InterestRule{{Id: "ship-1", BoostLabels: []string{"submarine"}}}}
	local, err := store.AcceptLocal(ctx, WithLocalLayer(flat, ship), "ship-console", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got := Flatten(local, "edge", t0.Add(time.Minute)).Interest
	if len(got.BoostLabels) != 1 || got.BoostLabels[0] != "submarine" || len(got.SuppressLabels) != 1 || got.SuppressLabels[0] != "cruise ship" {
		t.Fatalf("flat + ship interest = boost %v suppress %v", got.BoostLabels, got.SuppressLabels)
	}
	command := &pb.Policy{Id: "command", Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "c1", SuppressLabels: []string{"tanker"}}}}}}
	accepted, err := store.Accept(ctx, command, "downlink", t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if LocalLayer(accepted) == nil || len(accepted.Layers) != 2 {
		t.Fatalf("ship layer dropped by downlink: %v", accepted.Layers)
	}
	got = Flatten(accepted, "edge", t0.Add(2*time.Minute)).Interest
	if len(got.BoostLabels) != 1 || got.BoostLabels[0] != "submarine" || len(got.SuppressLabels) != 1 || got.SuppressLabels[0] != "tanker" {
		t.Fatalf("layered + ship interest = boost %v suppress %v", got.BoostLabels, got.SuppressLabels)
	}
	if cleared := WithLocalLayer(accepted, nil); LocalLayer(cleared) != nil || len(cleared.Layers) != 1 || LocalLayer(accepted) == nil {
		t.Fatalf("clear: %v (input %v)", cleared.Layers, accepted.Layers)
	}
}
