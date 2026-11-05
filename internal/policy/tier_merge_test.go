package policy

import (
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestTierMergePrecedence(t *testing.T) {
	pin := func(id string, tier int32) *pb.InterestRule {
		return &pb.InterestRule{Id: id, TierLabels: map[string]int32{" Tanker ": tier}}
	}
	for _, tc := range []struct {
		name     string
		rules    []*pb.InterestRule
		tier     int32
		suppress bool
	}{
		{"pin", []*pb.InterestRule{pin("first", 2)}, 2, false},
		{"new pin", []*pb.InterestRule{pin("first", 2), pin("last", 3)}, 3, false},
		{"new suppress", []*pb.InterestRule{pin("first", 2), {Id: "last", SuppressLabels: []string{"tanker"}}}, 0, true},
		{"new boost", []*pb.InterestRule{pin("first", 2), {Id: "last", BoostLabels: []string{"tanker"}}}, 0, false},
		{"pin replaces suppress", []*pb.InterestRule{{Id: "first", SuppressLabels: []string{"tanker"}}, pin("last", 2)}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _, conflicts := Merge([]*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_SHIP, Rules: tc.rules}}, "edge", time.Now())
			if in.TierLabels["tanker"] != tc.tier || (len(in.SuppressLabels) > 0) != tc.suppress || len(conflicts) > 0 {
				t.Fatalf("interest=%v conflicts=%v", in, conflicts)
			}
		})
	}
	high := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, Rules: []*pb.InterestRule{pin("high", 2)}}
	low := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, Rules: []*pb.InterestRule{pin("low", 3)}}
	in, _, _ := Merge([]*pb.PolicyLayer{low, high}, "edge", time.Now())
	if in.TierLabels["tanker"] != 3 {
		t.Fatal(in)
	}
	high.Rules[0].Locked = true
	in, _, conflicts := Merge([]*pb.PolicyLayer{low, high}, "edge", time.Now())
	if in.TierLabels["tanker"] != 2 || len(conflicts) != 1 || conflicts[0].RuleId != "low" {
		t.Fatalf("%v %v", in, conflicts)
	}
	low.Rules[0].TierLabels = map[string]int32{"tanker": 1}
	in, _, conflicts = Merge([]*pb.PolicyLayer{high, low}, "edge", time.Now())
	if in.TierLabels["tanker"] != 2 || len(conflicts) != 1 {
		t.Fatalf("%v %v", in, conflicts)
	}
	high.Rules[0].TierLabels = nil
	high.Rules[0].BoostLabels = []string{"tanker"}
	low.Rules[0].TierLabels = map[string]int32{"tanker": 2}
	_, _, conflicts = Merge([]*pb.PolicyLayer{high, low}, "edge", time.Now())
	if len(conflicts) != 1 {
		t.Fatal("pin must not lower a locked boost", conflicts)
	}
}

func TestLockedPinsRejectLowerEchelonGenericAndTierOverrides(t *testing.T) {
	for lockedTier := int32(1); lockedTier <= 4; lockedTier++ {
		for _, kind := range []string{"boost", "suppress", "pin"} {
			high := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, Rules: []*pb.InterestRule{{Id: "locked", Locked: true, TierLabels: map[string]int32{"tanker": lockedTier}}}}
			lower := &pb.InterestRule{Id: "override"}
			switch kind {
			case "boost":
				lower.BoostLabels = []string{" Tanker "}
			case "suppress":
				lower.SuppressLabels = []string{"tanker"}
			case "pin":
				lower.TierLabels = map[string]int32{"tanker": lockedTier%4 + 1}
			}
			low := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, Rules: []*pb.InterestRule{lower}}
			in, _, conflicts := Merge([]*pb.PolicyLayer{high, low}, "edge", time.Now())
			if in.TierLabels["tanker"] != lockedTier || len(conflicts) != 1 || conflicts[0].BlockedBy != "locked" || conflicts[0].RuleId != "override" {
				t.Fatalf("P%d %s bypassed lock: %v %v", lockedTier, kind, in, conflicts)
			}
		}
	}
}

func TestTierFlattenExpiryAndTargets(t *testing.T) {
	now := time.Now()
	p := &pb.Policy{Interest: &pb.Interest{TierLabels: map[string]int32{"stale": 2}}, Layers: []*pb.PolicyLayer{{Rules: []*pb.InterestRule{
		{TierLabels: map[string]int32{"expired": 1}, ExpiresAtUnixMs: now.UnixMilli()},
		{TierLabels: map[string]int32{"other": 2}, Targets: []string{"other-edge"}},
	}}}}
	got := Flatten(p, "edge", now)
	if len(got.Interest.TierLabels) != 0 || len(p.Interest.TierLabels) != 1 {
		t.Fatalf("flatten must clear stale pins without mutating input: %v", got)
	}
}

func TestFlatShorePinsSurviveShipConsoleOverlay(t *testing.T) {
	base := &pb.Policy{Interest: &pb.Interest{TierLabels: map[string]int32{"tanker": 2, "submarine": 1}}}
	local := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: LocalIssuer, Rules: []*pb.InterestRule{{Id: "bird", BoostLabels: []string{"bird"}}}}
	got := Flatten(WithLocalLayer(base, local), "edge", time.Now())
	if got.Interest.TierLabels["tanker"] != 2 || got.Interest.TierLabels["submarine"] != 1 || len(got.Interest.BoostLabels) != 1 {
		t.Fatalf("local overlay lost shore pins: %v", got)
	}
	local.Rules = append(local.Rules, &pb.InterestRule{Id: "suppress", SuppressLabels: []string{"tanker"}})
	got = Flatten(WithLocalLayer(base, local), "edge", time.Now())
	if got.Interest.TierLabels["tanker"] != 0 || got.Interest.TierLabels["submarine"] != 1 {
		t.Fatalf("new generic must cancel only its matching pin: %v", got)
	}
}
