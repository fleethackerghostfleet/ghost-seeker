package policy

import (
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

var mergeNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func layer(e pb.Echelon, by string, rules ...*pb.InterestRule) *pb.PolicyLayer {
	return &pb.PolicyLayer{Echelon: e, IssuedBy: by, Rules: rules}
}

func rule(id string, boost, suppress []string, weight float64) *pb.InterestRule {
	return &pb.InterestRule{Id: id, BoostLabels: boost, SuppressLabels: suppress, Weight: weight}
}

func TestMergeRulesLocksAndPrecedence(t *testing.T) {
	command := layer(pb.Echelon_ECHELON_COMMAND, "CTF-72",
		&pb.InterestRule{Id: "sub", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3},
		rule("tanker", []string{"tanker"}, nil, 0))
	command.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, Locked: true}
	ship := layer(pb.Echelon_ECHELON_SHIP, "DDG-51",
		rule("light-sub", []string{"submarine"}, nil, 2),
		rule("suppress-sub", nil, []string{" submarine "}, 0),
		rule("tanker", []string{"tanker", "aircraft"}, nil, 4))
	ship.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_BURST}
	accepted, emcon, conflicts := MergeRules([]*pb.PolicyLayer{ship, command}, "", mergeNow)
	if len(conflicts) != 3 {
		t.Fatalf("conflicts=%v, want two rule conflicts and emcon", conflicts)
	}
	for _, c := range conflicts[:2] {
		if c.Reason != "Locked by CTF-72" || c.BlockedBy != "sub" {
			t.Fatalf("rule conflict=%+v", c)
		}
	}
	if conflicts[2].RuleId != "emcon" || conflicts[2].Reason != "Locked by CTF-72" {
		t.Fatalf("emcon conflict=%+v", conflicts[2])
	}
	if emcon == nil || emcon.Mode != pb.EmconMode_EMCON_MODE_SILENT || !emcon.Locked || emcon.Echelon != pb.Echelon_ECHELON_COMMAND {
		t.Fatalf("emcon=%+v", emcon)
	}
	if len(accepted) != 2 || accepted[0].Rule.Id != "sub" || accepted[1].Rule.Id != "tanker" {
		t.Fatalf("accepted=%v, want locked rule and ship override", accepted)
	}
	if got := accepted[1].Rule.BoostLabels; len(got) != 2 || got[0] != "tanker" || got[1] != "aircraft" {
		t.Fatalf("ship rule=%v", got)
	}
}

func TestMergeEmptyAndUnlockedEMCON(t *testing.T) {
	interest, emcon, conflicts := Merge(nil, "", mergeNow)
	if interest == nil || emcon != nil || len(conflicts) != 0 {
		t.Fatalf("empty merge = %+v %v %v", interest, emcon, conflicts)
	}
	layers := []*pb.PolicyLayer{
		{Echelon: pb.Echelon_ECHELON_COMMAND, Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT}},
		{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_BURST}},
	}
	_, emcon, conflicts = Merge(layers, "", mergeNow)
	if emcon == nil || emcon.Mode != pb.EmconMode_EMCON_MODE_BURST || emcon.SetBy != "TAO" || emcon.Echelon != pb.Echelon_ECHELON_SHIP || len(conflicts) != 0 {
		t.Fatalf("unlocked emcon=%+v conflicts=%v", emcon, conflicts)
	}
}

func TestMergeTargetScopesReplacementAndSuppressOnly(t *testing.T) {
	command := layer(pb.Echelon_ECHELON_COMMAND, "CTF-72",
		rule("broad", []string{"submarine", "aircraft"}, []string{"fishing"}, 3))
	ship := layer(pb.Echelon_ECHELON_SHIP, "DDG-51",
		rule("local", nil, []string{"pirate"}, 0),
		rule("replace", []string{"submarine"}, nil, 0))
	command.Rules[0].Targets = []string{"USV-1"}
	ship.Rules[0].Targets = []string{"USV-2"}
	ship.Rules[1].Targets = []string{"USV-2"}
	accepted, _, _ := MergeRules([]*pb.PolicyLayer{command, ship}, "", mergeNow)
	if len(accepted) != 3 || !containsLabel(accepted[0].Rule.BoostLabels, "submarine") {
		t.Fatalf("distinct scopes should both remain; accepted=%v", accepted)
	}
	_, _, _ = MergeRules([]*pb.PolicyLayer{command, ship}, "USV-2", mergeNow)

	trimmed := layer(pb.Echelon_ECHELON_COMMAND, "CTF-72", rule("wide", []string{"Submarine", "Aircraft"}, []string{"Fishing"}, 3))
	replacer := layer(pb.Echelon_ECHELON_SHIP, "TAO", rule("local", []string{" submarine "}, []string{"Pirate"}, 0))
	accepted, _, _ = MergeRules([]*pb.PolicyLayer{trimmed, replacer}, "DDG-51", mergeNow)
	if len(accepted) != 2 || len(accepted[0].Rule.BoostLabels) != 1 || accepted[0].Rule.BoostLabels[0] != "Aircraft" ||
		len(accepted[0].Rule.SuppressLabels) != 1 || accepted[0].Rule.SuppressLabels[0] != "Fishing" {
		t.Fatalf("per-label trim=%+v", accepted)
	}
	suppressRules := []*pb.PolicyLayer{
		layer(pb.Echelon_ECHELON_COMMAND, "A", rule("s1", nil, []string{"boats"}, 0)),
		layer(pb.Echelon_ECHELON_SHIP, "B", rule("s2", nil, []string{"people"}, 0)),
	}
	accepted, _, _ = MergeRules(suppressRules, "", mergeNow)
	if len(accepted) != 2 {
		t.Fatalf("distinct suppress-only rules=%d, want 2", len(accepted))
	}
}

func TestScopedLocks(t *testing.T) {
	tests := []struct {
		name    string
		targets []string
		blocked bool
	}{
		{name: "unscoped lock", blocked: true},
		{name: "matching target", targets: []string{"USV-1"}, blocked: true},
		{name: "separate target", targets: []string{"USV-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := Lock{RuleID: "lock", LockedBy: "CTF-72", Echelon: pb.Echelon_ECHELON_COMMAND,
				BoostLabels: []string{"submarine"}, Weight: 3, Targets: tt.targets}
			lower := rule("lower", nil, []string{"submarine"}, 0)
			if tt.name == "separate target" {
				lock.Targets = []string{"USV-1"}
				lower.Targets = []string{"USV-2"}
			}
			conflict := CheckRule([]Lock{lock}, pb.Echelon_ECHELON_SHIP, lower)
			if (conflict != nil) != tt.blocked {
				t.Fatalf("conflict=%v, blocked=%v", conflict, tt.blocked)
			}
		})
	}
}

func TestActiveLocksFilterExpiredAndOffTargetRules(t *testing.T) {
	tests := []struct {
		name   string
		rule   *pb.InterestRule
		nodeID string
		want   bool
	}{
		{
			name: "expired",
			rule: &pb.InterestRule{Id: "expired", Locked: true, BoostLabels: []string{"submarine"},
				ExpiresAtUnixMs: mergeNow.Add(-time.Minute).UnixMilli()},
			nodeID: "USV-1",
		},
		{
			name:   "targeted to another node",
			rule:   &pb.InterestRule{Id: "other-node", Locked: true, Targets: []string{"USV-2"}},
			nodeID: "USV-1",
		},
		{
			name:   "active lock",
			rule:   &pb.InterestRule{Id: "active", Locked: true, Targets: []string{"USV-1"}},
			nodeID: "USV-1",
			want:   true,
		},
		{
			name: "unknown node ignores target-scoped lock",
			rule: &pb.InterestRule{Id: "unknown-node", Locked: true, Targets: []string{"USV-2"}},
			want: false,
		},
		{
			name: "unknown node keeps unscoped lock",
			rule: &pb.InterestRule{Id: "unscoped", Locked: true},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locks := ActiveLocks([]*pb.PolicyLayer{layer(pb.Echelon_ECHELON_COMMAND, "CTF-72", tt.rule)}, tt.nodeID, mergeNow)
			if (len(locks) == 1) != tt.want {
				t.Fatalf("active locks=%+v, want lock=%t", locks, tt.want)
			}
			if tt.want && locks[0].RuleID != tt.rule.Id {
				t.Fatalf("active lock=%+v, want rule %q", locks[0], tt.rule.Id)
			}
		})
	}

	command := layer(pb.Echelon_ECHELON_COMMAND, "CTF-72",
		&pb.InterestRule{Id: "expired-rule", Locked: true, ExpiresAtUnixMs: mergeNow.Add(-time.Minute).UnixMilli()})
	command.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, Locked: true}
	locks := ActiveLocks([]*pb.PolicyLayer{command}, "", mergeNow)
	if len(locks) != 1 || !locks[0].Emcon || locks[0].Mode != pb.EmconMode_EMCON_MODE_SILENT {
		t.Fatalf("active EMCON locks=%+v, want locked SILENT setting", locks)
	}
}

func TestMergeWeightExpiryTargetsLocksAndFlatten(t *testing.T) {
	layers := []*pb.PolicyLayer{
		layer(pb.Echelon_ECHELON_COMMAND, "CTF-72", &pb.InterestRule{Id: "locked", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3}),
		layer(pb.Echelon_ECHELON_SHIP, "TAO",
			rule("small", []string{"submarine"}, nil, 2),
			rule("equal", []string{"submarine"}, nil, 3),
			rule("greater", []string{"submarine"}, nil, 4),
			&pb.InterestRule{Id: "old", BoostLabels: []string{"old"}, ExpiresAtUnixMs: mergeNow.UnixMilli()},
			&pb.InterestRule{Id: "other", BoostLabels: []string{"other"}, Targets: []string{"USV-2"}}),
	}
	accepted, _, conflicts := MergeRules(layers, "USV-1", mergeNow)
	if len(accepted) != 3 || len(conflicts) != 1 || conflicts[0].RuleId != "small" {
		t.Fatalf("accepted=%v conflicts=%v", accepted, conflicts)
	}
	interest, _, _ := Merge(layers, "USV-1", mergeNow)
	if interest.BoostFactor != 4 {
		t.Fatalf("boost factor=%v, want 4", interest.BoostFactor)
	}
	locks := Locks([]*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "r", Locked: true}}, Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, Locked: true}}})
	if len(locks) != 2 || locks[0].RuleID != "r" || !locks[1].Emcon || locks[1].RuleID != "emcon" {
		t.Fatalf("locks=%+v", locks)
	}

	empty := &pb.Policy{Interest: &pb.Interest{PositiveEmbedding: []float32{1, 2}, SuppressFactor: 0.25, BoostLabels: []string{"old"}}}
	if got := Flatten(empty, "", mergeNow); got != empty {
		t.Fatal("flatten without layers must retain pointer")
	}
	original := &pb.Policy{Interest: &pb.Interest{PositiveEmbedding: []float32{1, 2}, SuppressFactor: 0.25, BoostLabels: []string{"old"}},
		Thresholds: &pb.Thresholds{UnknownMin: 0.3}, Layers: []*pb.PolicyLayer{
			layer(pb.Echelon_ECHELON_SHIP, "TAO", rule("new", []string{"new"}, []string{"avoid"}, 0)),
		}}
	original.Layers[0].Rules[0].ExpiresAtUnixMs = mergeNow.Add(time.Hour).UnixMilli()
	flattened := Flatten(original, "", mergeNow)
	if flattened == original || !proto.Equal(original.Interest, &pb.Interest{PositiveEmbedding: []float32{1, 2}, SuppressFactor: 0.25, BoostLabels: []string{"old"}}) {
		t.Fatal("flatten mutated input")
	}
	if flattened.Interest.BoostLabels[0] != "new" || flattened.Interest.SuppressLabels[0] != "avoid" ||
		flattened.Interest.SuppressFactor != 0.25 || flattened.Interest.PositiveEmbedding != nil ||
		flattened.Thresholds.UnknownMin != 0.3 || len(flattened.Conflicts) != 0 {
		t.Fatalf("flattened=%+v", flattened)
	}
}

func TestFlattenEmbeddingsAndExpiredBoost(t *testing.T) {
	same := &pb.Policy{Interest: &pb.Interest{
		BoostLabels: []string{" Submarine "}, SuppressLabels: []string{" AVOID "},
		PositiveEmbedding: []float32{1}, NegativeEmbedding: []float32{2},
	}, Layers: []*pb.PolicyLayer{layer(pb.Echelon_ECHELON_SHIP, "TAO",
		rule("same", []string{"submarine"}, []string{"avoid"}, 2))}}
	got := Flatten(same, "USV-1", mergeNow)
	if got.Interest.PositiveEmbedding == nil || got.Interest.NegativeEmbedding == nil {
		t.Fatal("normalized-equivalent labels cleared embeddings")
	}

	boostOnly := &pb.Policy{Interest: &pb.Interest{
		BoostLabels: []string{"old"}, SuppressLabels: []string{"avoid"},
		PositiveEmbedding: []float32{1}, NegativeEmbedding: []float32{2},
	}, Layers: []*pb.PolicyLayer{layer(pb.Echelon_ECHELON_SHIP, "TAO",
		rule("boost-only", []string{"new"}, []string{"avoid"}, 2))}}
	got = Flatten(boostOnly, "USV-1", mergeNow)
	if got.Interest.PositiveEmbedding != nil || got.Interest.NegativeEmbedding == nil {
		t.Fatalf("boost-only change embeddings = %+v", got.Interest)
	}

	suppressOnly := &pb.Policy{Interest: &pb.Interest{
		BoostLabels: []string{"submarine"}, SuppressLabels: []string{"old"},
		PositiveEmbedding: []float32{1}, NegativeEmbedding: []float32{2},
	}, Layers: []*pb.PolicyLayer{layer(pb.Echelon_ECHELON_SHIP, "TAO",
		rule("suppress-only", []string{"submarine"}, []string{"avoid"}, 2))}}
	got = Flatten(suppressOnly, "USV-1", mergeNow)
	if got.Interest.PositiveEmbedding == nil || got.Interest.NegativeEmbedding != nil {
		t.Fatalf("suppress-only change embeddings = %+v", got.Interest)
	}

	expired := &pb.Policy{Interest: &pb.Interest{
		BoostLabels: []string{"submarine"}, PositiveEmbedding: []float32{1}, NegativeEmbedding: []float32{2},
	}, Layers: []*pb.PolicyLayer{layer(pb.Echelon_ECHELON_SHIP, "TAO",
		&pb.InterestRule{Id: "short", BoostLabels: []string{"submarine"}, Weight: 7, ExpiresAtUnixMs: mergeNow.UnixMilli()})}}
	got = Flatten(expired, "USV-1", mergeNow)
	if len(got.Interest.BoostLabels) != 0 || got.Interest.BoostFactor != 0 ||
		got.Interest.PositiveEmbedding != nil || got.Interest.NegativeEmbedding == nil {
		t.Fatalf("expired rule retained stale interest: %+v", got.Interest)
	}

	targeted := &pb.Policy{Interest: &pb.Interest{
		BoostLabels: []string{"submarine"}, PositiveEmbedding: []float32{1},
	}, Layers: []*pb.PolicyLayer{layer(pb.Echelon_ECHELON_SHIP, "TAO",
		&pb.InterestRule{Id: "other-node", BoostLabels: []string{"submarine"}, Targets: []string{"USV-2"}, Weight: 2})}}
	got = Flatten(targeted, "USV-1", mergeNow)
	if got.Interest.PositiveEmbedding != nil || len(got.Interest.BoostLabels) != 0 {
		t.Fatalf("other-node rule retained stale embedding: %+v", got.Interest)
	}
}

func TestCheckRuleSameIDAndWeightBoundary(t *testing.T) {
	lock := Lock{RuleID: "same", LockedBy: "CTF-72", Echelon: pb.Echelon_ECHELON_COMMAND,
		BoostLabels: []string{"submarine"}, Weight: 3}
	if c := CheckRule([]Lock{lock}, pb.Echelon_ECHELON_SHIP, rule("same", nil, nil, 0)); c == nil || c.BlockedBy != "same" {
		t.Fatalf("same id conflict=%v", c)
	}
	if c := CheckRule([]Lock{lock}, pb.Echelon_ECHELON_SHIP, rule("equal", []string{"submarine"}, nil, 3)); c != nil {
		t.Fatalf("equal weight blocked: %v", c)
	}
	if c := CheckRule([]Lock{lock}, pb.Echelon_ECHELON_SHIP, rule("greater", []string{"submarine"}, nil, 4)); c != nil {
		t.Fatalf("greater weight blocked: %v", c)
	}
}
