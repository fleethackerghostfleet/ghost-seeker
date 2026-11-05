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
	TierLabels  map[string]int32
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
				if higher.Layer.Echelon < layer.Echelon || higher.Rule.Locked {
					i++
					continue
				}
				if higher.Layer.Echelon == layer.Echelon && len(higher.Rule.TierLabels) == 0 && len(rule.TierLabels) == 0 {
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
				for label := range rule.TierLabels {
					trim[normalizeLabel(label)] = true
				}
				higher.Rule.BoostLabels = trimLabels(higher.Rule.BoostLabels, trim)
				higher.Rule.SuppressLabels = trimLabels(higher.Rule.SuppressLabels, trim)
				for label := range higher.Rule.TierLabels {
					if trim[normalizeLabel(label)] {
						delete(higher.Rule.TierLabels, label)
					}
				}
				if len(higher.Rule.BoostLabels)+len(higher.Rule.SuppressLabels)+len(higher.Rule.TierLabels) == 0 {
					accepted = append(accepted[:i], accepted[i+1:]...)
					continue
				}
				i++
			}
		}
	}

	emcon, emconConflicts := mergeEmcon(ordered, now)
	conflicts = append(conflicts, emconConflicts...)
	return accepted, emcon, conflicts
}

func Merge(layers []*pb.PolicyLayer, nodeID string, now time.Time) (*pb.Interest, *pb.EmconSetting, []*pb.MergeConflict) {
	accepted, emcon, conflicts := MergeRules(layers, nodeID, now)
	interest := &pb.Interest{}
	seenBoost, seenSuppress := map[string]bool{}, map[string]bool{}
	for _, item := range accepted {
		for _, label := range append(append([]string(nil), item.Rule.BoostLabels...), item.Rule.SuppressLabels...) {
			delete(interest.TierLabels, normalizeLabel(label))
		}
		for _, label := range item.Rule.BoostLabels {
			key := normalizeLabel(label)
			if key == "" {
				continue
			}
			if !seenBoost[key] {
				seenBoost[key] = true
				interest.BoostLabels = append(interest.BoostLabels, strings.TrimSpace(label))
			}
			// Per-label weight: the strongest accepted rule for that label (a
			// lower echelon may raise a locked label's weight, never lower it).
			if item.Rule.Weight > 0 && item.Rule.Weight > interest.BoostWeights[key] {
				if interest.BoostWeights == nil {
					interest.BoostWeights = map[string]float64{}
				}
				interest.BoostWeights[key] = item.Rule.Weight
			}
		}
		for _, label := range item.Rule.SuppressLabels {
			key := normalizeLabel(label)
			if key == "" || seenSuppress[key] {
				continue
			}
			seenSuppress[key] = true
			interest.SuppressLabels = append(interest.SuppressLabels, strings.TrimSpace(label))
		}
		for label, tier := range item.Rule.TierLabels {
			key := normalizeLabel(label)
			if interest.TierLabels == nil {
				interest.TierLabels = map[string]int32{}
			}
			interest.TierLabels[key] = tier
			interest.BoostLabels = trimLabels(interest.BoostLabels, map[string]bool{key: true})
			interest.SuppressLabels = trimLabels(interest.SuppressLabels, map[string]bool{key: true})
			delete(seenBoost, key)
			delete(seenSuppress, key)
			delete(interest.BoostWeights, key)
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
	if onlyLocalLayers(out.Layers) {
		// A flat (layer-less) shore policy plus the ship console's own rules: keep both.
		interest.BoostLabels = unionLabels(out.GetInterest().GetBoostLabels(), interest.BoostLabels)
		interest.SuppressLabels = unionLabels(out.GetInterest().GetSuppressLabels(), interest.SuppressLabels)
		pins := map[string]int32{}
		for label, tier := range out.GetInterest().GetTierLabels() {
			pins[normalizeLabel(label)] = tier
		}
		for _, layer := range out.Layers {
			for _, rule := range layer.Rules {
				if rule == nil || expiredRule(rule, now) || !targetsNode(rule.Targets, nodeID) {
					continue
				}
				for _, label := range append(append([]string(nil), rule.BoostLabels...), rule.SuppressLabels...) {
					delete(pins, normalizeLabel(label))
				}
			}
		}
		for label, tier := range interest.TierLabels {
			pins[label] = tier
		}
		interest.TierLabels = pins
		for label := range pins {
			interest.BoostLabels = trimLabels(interest.BoostLabels, map[string]bool{label: true})
			interest.SuppressLabels = trimLabels(interest.SuppressLabels, map[string]bool{label: true})
		}
	}
	if out.Interest == nil {
		out.Interest = &pb.Interest{}
	}
	inputBoost := append([]string(nil), out.Interest.BoostLabels...)
	inputSuppress := append([]string(nil), out.Interest.SuppressLabels...)
	out.Interest.BoostLabels = interest.BoostLabels
	out.Interest.SuppressLabels = interest.SuppressLabels
	// BoostFactor is left as configured on the policy: it still scales the
	// embedding-similarity path and unweighted labels. Per-label weights come
	// from the merged rules.
	out.Interest.BoostWeights = interest.BoostWeights
	out.Interest.TierLabels = interest.TierLabels
	if !sameLabelSet(inputBoost, interest.BoostLabels) {
		out.Interest.PositiveEmbedding = nil
	}
	if !sameLabelSet(inputSuppress, interest.SuppressLabels) {
		out.Interest.NegativeEmbedding = nil
	}
	if emcon != nil {
		out.Emcon = emcon.Mode
		out.EmconSetting = emcon
	} else {
		for _, layer := range out.Layers {
			if layer != nil && layer.Emcon != nil && layer.Emcon.Mode != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
				out.Emcon = pb.EmconMode_EMCON_MODE_NORMAL
				out.EmconSetting = nil
				break
			}
		}
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
			locks = append(locks, ruleLock(layer, rule))
		}
		if layer.Emcon != nil && layer.Emcon.Locked {
			locks = append(locks, emconLock(layer, layer.Emcon))
		}
	}
	return locks
}

func ActiveLocks(layers []*pb.PolicyLayer, nodeID string, now time.Time) []Lock {
	ordered := sortedLayers(layers)
	var locks []Lock
	for _, layer := range ordered {
		if layer == nil {
			continue
		}
		for _, rule := range layer.Rules {
			if rule == nil || !rule.Locked || expiredRule(rule, now) || !targetsNode(rule.Targets, nodeID) {
				continue
			}
			locks = append(locks, ruleLock(layer, rule))
		}
		if layer.Emcon != nil && layer.Emcon.Locked && !expiredEmcon(layer.Emcon, now) {
			locks = append(locks, emconLock(layer, layer.Emcon))
		}
	}
	return locks
}

func ruleLock(layer *pb.PolicyLayer, rule *pb.InterestRule) Lock {
	lockedBy := layer.IssuedBy
	if lockedBy == "" {
		lockedBy = rule.Author
	}
	if lockedBy == "" {
		lockedBy = "higher echelon"
	}
	return Lock{RuleID: rule.Id, LockedBy: lockedBy, Echelon: layer.Echelon,
		BoostLabels: append([]string(nil), rule.BoostLabels...), TierLabels: rule.TierLabels, Weight: effectiveWeight(rule.Weight),
		Targets: append([]string(nil), rule.Targets...)}
}

func emconLock(layer *pb.PolicyLayer, setting *pb.EmconSetting) Lock {
	lockedBy := setting.SetBy
	if lockedBy == "" {
		lockedBy = layer.IssuedBy
	}
	if lockedBy == "" {
		lockedBy = "higher echelon"
	}
	return Lock{RuleID: "emcon", LockedBy: lockedBy, Echelon: layer.Echelon, Emcon: true, Mode: setting.Mode}
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
		for label, tier := range r.TierLabels {
			if containsLabel(lock.BoostLabels, label) && tier > 1 {
				blocked = true
			}
			for lockedLabel, lockedTier := range lock.TierLabels {
				if normalizeLabel(label) == normalizeLabel(lockedLabel) && tier != lockedTier {
					blocked = true
				}
			}
		}
		for label := range lock.TierLabels {
			if containsLabel(r.SuppressLabels, label) || containsLabel(r.BoostLabels, label) {
				blocked = true
			}
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
			BoostLabels: a.Rule.BoostLabels, TierLabels: a.Rule.TierLabels, Weight: effectiveWeight(a.Rule.Weight), Targets: a.Rule.Targets})
	}
	return locks
}

func mergeEmcon(layers []*pb.PolicyLayer, now time.Time) (*pb.EmconSetting, []*pb.MergeConflict) {
	lockIndex := -1
	for i, layer := range layers {
		if layer != nil && layer.Emcon != nil && layer.Emcon.Locked &&
			layer.Emcon.Mode != pb.EmconMode_EMCON_MODE_UNSPECIFIED && !expiredEmcon(layer.Emcon, now) {
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
			if layer == nil || layer.Emcon == nil || layer.Emcon.Mode == pb.EmconMode_EMCON_MODE_UNSPECIFIED ||
				expiredEmcon(layer.Emcon, now) || layer.Emcon.Mode == setting.Mode {
				continue
			}
			conflicts = append(conflicts, &pb.MergeConflict{RuleId: "emcon", Reason: "Locked by " + emconLockBy(winner), BlockedBy: "emcon"})
		}
		return setting, conflicts
	}

	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		if layer == nil || layer.Emcon == nil || layer.Emcon.Mode == pb.EmconMode_EMCON_MODE_UNSPECIFIED ||
			expiredEmcon(layer.Emcon, now) {
			continue
		}
		setting := proto.Clone(layer.Emcon).(*pb.EmconSetting)
		fillEmcon(setting, layer)
		return setting, nil
	}
	return nil, nil
}

func expiredEmcon(setting *pb.EmconSetting, now time.Time) bool {
	return setting != nil && setting.ExpiresAtUnixMs > 0 && !now.Before(time.UnixMilli(setting.ExpiresAtUnixMs))
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
