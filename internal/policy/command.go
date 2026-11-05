package policy

import (
	"fmt"
	"strings"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type RuleLabels struct {
	Type    string
	Weight  float64
	Tier    int
	Classes []string
	Attrs   []string
}

func CheckCommandRules(locks []Lock, echelon pb.Echelon, rules []RuleLabels) ([]string, bool) {
	var warnings []string
	blocked := false
	echelonName := strings.ToLower(strings.TrimPrefix(echelon.String(), "ECHELON_"))
	for i, rule := range rules {
		labels := append(append([]string(nil), rule.Classes...), rule.Attrs...)
		interestRule := &pb.InterestRule{Id: fmt.Sprintf("new:%d", i), Weight: rule.Weight}
		if rule.Tier > 0 {
			interestRule.TierLabels = map[string]int32{}
			for _, label := range labels {
				interestRule.TierLabels[normalizeLabel(label)] = int32(rule.Tier)
			}
		} else {
			switch rule.Type {
			case "boost":
				interestRule.BoostLabels = labels
			case "suppress":
				interestRule.SuppressLabels = labels
			default:
				continue
			}
		}
		if conflict := CheckRule(locks, echelon, interestRule); conflict != nil {
			warnings = append(warnings, conflict.Reason+" — "+strings.Join(labels, ", ")+" can't be overridden at "+echelonName+" level")
			blocked = true
		}
	}
	return warnings, blocked
}
