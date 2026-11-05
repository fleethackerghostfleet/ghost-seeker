package policy

import (
	"fmt"
	"strings"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type RuleLabels struct {
	Type    string
	Weight  float64
	Classes []string
	Attrs   []string
}

func CheckCommandRules(locks []Lock, echelon pb.Echelon, rules []RuleLabels) ([]string, bool) {
	var warnings []string
	blocked := false
	for i, rule := range rules {
		labels := append(append([]string(nil), rule.Classes...), rule.Attrs...)
		interestRule := &pb.InterestRule{Id: fmt.Sprintf("new:%d", i), Weight: rule.Weight}
		switch rule.Type {
		case "boost":
			interestRule.BoostLabels = labels
		case "suppress":
			interestRule.SuppressLabels = labels
		default:
			continue
		}
		if conflict := CheckRule(locks, echelon, interestRule); conflict != nil {
			warnings = append(warnings, conflict.Reason+" — "+strings.Join(labels, ", ")+" can't be overridden at ship level")
			blocked = true
		}
	}
	return warnings, blocked
}
