// Package policy merges echelon policy layers and validates signed policy state.
//
// Unlike dynamic-interest, this package uses rule weight instead of
// FLASH/ROUTINE precedence, trims shared labels instead of replacing only
// ignore-only/equal-label-set rules, and does not merge version, limits, or
// subscriptions. Layers are sorted by echelon rather than used in input order;
// expiry and target filters run here rather than in store.active; EMCON
// conflicts are emitted only when modes differ.
package policy

import (
	"sort"
	"strings"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

const DefaultRuleWeight = 2.0

type Accepted struct {
	Rule  *pb.InterestRule
	Layer *pb.PolicyLayer
}

type Lock struct {
	RuleID      string
	LockedBy    string
	Echelon     pb.Echelon
	BoostLabels []string
	Weight      float64
	Targets     []string
	Emcon       bool
	Mode        pb.EmconMode
}

func MergeRules(layers []*pb.PolicyLayer, nodeID string, now time.Time) (accepted []Accepted, emcon *pb.EmconSetting, conflicts []*pb.MergeConflict) {
	ordered := sortedLayers(layers)
	for _, layer := range ordered {
		if layer == nil {
			continue
		}
		for _, rule := range layer.Rules {
			if rule == nil || expiredRule(rule, now) || !targetsNode(rule.Targets, nodeID) {
				continue
			}
			if conflict := CheckRule(acceptedLocks(accepted), layer.Echelon, rule); conflict != nil {
				conflicts = append(conflicts, conflict)
				continue
			}

			copyRule := proto.Clone(rule).(*pb.InterestRule)
			copyLayer := proto.Clone(layer).(*pb.PolicyLayer)
			accepted = append(accepted, Accepted{Rule: copyRule, Layer: copyLayer})
			for i := 0; i < len(accepted)-1; {
				higher := accepted[i]
				if higher.Layer.Echelon <= layer.Echelon || higher.Rule.Locked {
					i++
					continue
				}
				if higher.Rule.Id == rule.Id {
					accepted = append(accepted[:i], accepted[i+1:]...)
					continue
				}
				if !targetsOverlap(higher.Rule.Targets, rule.Targets) {
					i++
					continue
				}
				trim := labelSet(append(append([]string(nil), rule.BoostLabels...), rule.SuppressLabels...))
				higher.Rule.BoostLabels = trimLabels(higher.Rule.BoostLabels, trim)
				higher.Rule.SuppressLabels = trimLabels(higher.Rule.SuppressLabels, trim)
				if len(higher.Rule.BoostLabels) == 0 && len(higher.Rule.SuppressLabels) == 0 {
					accepted = append(accepted[:i], accepted[i+1:]...)
					continue
				}
				i++
			}
		}
	}

	emcon, emconConflicts := mergeEmcon(ordered)
	conflicts = append(conflicts, emconConflicts...)
	return accepted, emcon, conflicts
}

func Merge(layers []*pb.PolicyLayer, nodeID string, now time.Time) (*pb.Interest, *pb.EmconSetting, []*pb.MergeConflict) {
	accepted, emcon, conflicts := MergeRules(layers, nodeID, now)
	interest := &pb.Interest{}
	seenBoost, seenSuppress := map[string]bool{}, map[string]bool{}
	for _, item := range accepted {
		for _, label := range item.Rule.BoostLabels {
			key := normalizeLabel(label)
			if key == "" || seenBoost[key] {
				continue
			}
			seenBoost[key] = true
			interest.BoostLabels = append(interest.BoostLabels, strings.TrimSpace(label))
		}
		for _, label := range item.Rule.SuppressLabels {
			key := normalizeLabel(label)
			if key == "" || seenSuppress[key] {
				continue
			}
			seenSuppress[key] = true
			interest.SuppressLabels = append(interest.SuppressLabels, strings.TrimSpace(label))
		}
		if len(item.Rule.BoostLabels) > 0 && item.Rule.Weight > 0 && item.Rule.Weight > interest.BoostFactor {
			interest.BoostFactor = item.Rule.Weight
		}
	}
	return interest, emcon, conflicts
}

func Flatten(p *pb.Policy, nodeID string, now time.Time) *pb.Policy {
	if p == nil || len(p.Layers) == 0 {
		return p
	}
	out := proto.Clone(p).(*pb.Policy)
	interest, emcon, conflicts := Merge(out.Layers, nodeID, now)
	if out.Interest == nil {
		out.Interest = &pb.Interest{}
	}
	inputBoost := append([]string(nil), out.Interest.BoostLabels...)
	inputSuppress := append([]string(nil), out.Interest.SuppressLabels...)
	out.Interest.BoostLabels = interest.BoostLabels
	out.Interest.SuppressLabels = interest.SuppressLabels
	out.Interest.BoostFactor = interest.BoostFactor
	if !sameLabelSet(inputBoost, interest.BoostLabels) || !sameLabelSet(inputSuppress, interest.SuppressLabels) {
		out.Interest.PositiveEmbedding = nil
		out.Interest.NegativeEmbedding = nil
	}
	if emcon != nil {
		out.Emcon = emcon.Mode
		out.EmconSetting = emcon
	}
	out.Conflicts = conflicts
	return out
}

func sameLabelSet(a, b []string) bool {
	left, right := labelSet(a), labelSet(b)
	if len(left) != len(right) {
		return false
	}
	for label := range left {
		if !right[label] {
			return false
		}
	}
	return true
}

func Locks(layers []*pb.PolicyLayer) []Lock {
	ordered := sortedLayers(layers)
	var locks []Lock
	for _, layer := range ordered {
		if layer == nil {
			continue
		}
		for _, rule := range layer.Rules {
			if rule == nil || !rule.Locked {
				continue
			}
			lockedBy := layer.IssuedBy
			if lockedBy == "" {
				lockedBy = rule.Author
			}
			if lockedBy == "" {
				lockedBy = "higher echelon"
			}
			locks = append(locks, Lock{RuleID: rule.Id, LockedBy: lockedBy, Echelon: layer.Echelon,
				BoostLabels: append([]string(nil), rule.BoostLabels...), Weight: effectiveWeight(rule.Weight),
				Targets: append([]string(nil), rule.Targets...)})
		}
		if setting := layer.Emcon; setting != nil && setting.Locked {
			lockedBy := setting.SetBy
			if lockedBy == "" {
				lockedBy = layer.IssuedBy
			}
			if lockedBy == "" {
				lockedBy = "higher echelon"
			}
			locks = append(locks, Lock{RuleID: "emcon", LockedBy: lockedBy, Echelon: layer.Echelon, Emcon: true, Mode: setting.Mode})
		}
	}
	return locks
}

func CheckRule(locks []Lock, echelon pb.Echelon, r *pb.InterestRule) *pb.MergeConflict {
	if r == nil {
		return nil
	}
	for _, lock := range locks {
		if lock.Emcon || lock.Echelon <= echelon || !targetsOverlap(lock.Targets, r.Targets) {
			continue
		}
		blocked := lock.RuleID == r.Id
		if !blocked && intersects(lock.BoostLabels, r.SuppressLabels) {
			blocked = true
		}
		if !blocked {
			weight := effectiveWeight(r.Weight)
			for _, boost := range r.BoostLabels {
				if containsLabel(lock.BoostLabels, boost) && weight < lock.Weight {
					blocked = true
					break
				}
			}
		}
		if blocked {
			by := lock.LockedBy
			if by == "" {
				by = "higher echelon"
			}
			return &pb.MergeConflict{RuleId: r.Id, Reason: "Locked by " + by, BlockedBy: lock.RuleID}
		}
	}
	return nil
}

func acceptedLocks(accepted []Accepted) []Lock {
	var locks []Lock
	for _, a := range accepted {
		if !a.Rule.Locked {
			continue
		}
		by := a.Layer.IssuedBy
		if by == "" {
			by = a.Rule.Author
		}
		if by == "" {
			by = "higher echelon"
		}
		locks = append(locks, Lock{RuleID: a.Rule.Id, LockedBy: by, Echelon: a.Layer.Echelon,
			BoostLabels: a.Rule.BoostLabels, Weight: effectiveWeight(a.Rule.Weight), Targets: a.Rule.Targets})
	}
	return locks
}

func mergeEmcon(layers []*pb.PolicyLayer) (*pb.EmconSetting, []*pb.MergeConflict) {
	lockIndex := -1
	for i, layer := range layers {
		if layer != nil && layer.Emcon != nil && layer.Emcon.Locked && layer.Emcon.Mode != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
			lockIndex = i
			break
		}
	}
	if lockIndex >= 0 {
		winner := layers[lockIndex]
		setting := proto.Clone(winner.Emcon).(*pb.EmconSetting)
		fillEmcon(setting, winner)
		var conflicts []*pb.MergeConflict
		for _, layer := range layers[lockIndex+1:] {
			if layer == nil || layer.Emcon == nil || layer.Emcon.Mode == pb.EmconMode_EMCON_MODE_UNSPECIFIED || layer.Emcon.Mode == setting.Mode {
				continue
			}
			conflicts = append(conflicts, &pb.MergeConflict{RuleId: "emcon", Reason: "Locked by " + emconLockBy(winner), BlockedBy: "emcon"})
		}
		return setting, conflicts
	}

	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		if layer == nil || layer.Emcon == nil || layer.Emcon.Mode == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
			continue
		}
		setting := proto.Clone(layer.Emcon).(*pb.EmconSetting)
		fillEmcon(setting, layer)
		return setting, nil
	}
	return nil, nil
}

func fillEmcon(setting *pb.EmconSetting, layer *pb.PolicyLayer) {
	if setting.Echelon == pb.Echelon_ECHELON_UNSPECIFIED {
		setting.Echelon = layer.Echelon
	}
	if setting.SetBy == "" {
		setting.SetBy = layer.IssuedBy
	}
}

func emconLockBy(layer *pb.PolicyLayer) string {
	if layer.Emcon.SetBy != "" {
		return layer.Emcon.SetBy
	}
	if layer.IssuedBy != "" {
		return layer.IssuedBy
	}
	return "higher echelon"
}

func sortedLayers(layers []*pb.PolicyLayer) []*pb.PolicyLayer {
	out := append([]*pb.PolicyLayer(nil), layers...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == nil {
			return false
		}
		if out[j] == nil {
			return true
		}
		return out[i].Echelon > out[j].Echelon
	})
	return out
}

func expiredRule(rule *pb.InterestRule, now time.Time) bool {
	return rule.ExpiresAtUnixMs > 0 && now.UnixMilli() >= rule.ExpiresAtUnixMs
}

func targetsNode(targets []string, nodeID string) bool {
	return len(targets) == 0 || nodeID == "" || containsString(targets, nodeID)
}

func targetsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, target := range a {
		if containsString(b, target) {
			return true
		}
	}
	return false
}

func effectiveWeight(weight float64) float64 {
	if weight > 0 {
		return weight
	}
	return DefaultRuleWeight
}

func normalizeLabel(label string) string { return strings.ToLower(strings.TrimSpace(label)) }

func labelSet(labels []string) map[string]bool {
	out := make(map[string]bool, len(labels))
	for _, label := range labels {
		if key := normalizeLabel(label); key != "" {
			out[key] = true
		}
	}
	return out
}

func trimLabels(labels []string, trim map[string]bool) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if !trim[normalizeLabel(label)] {
			out = append(out, label)
		}
	}
	return out
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if containsLabel(b, x) {
			return true
		}
	}
	return false
}

func containsLabel(labels []string, label string) bool {
	key := normalizeLabel(label)
	for _, candidate := range labels {
		if normalizeLabel(candidate) == key {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
