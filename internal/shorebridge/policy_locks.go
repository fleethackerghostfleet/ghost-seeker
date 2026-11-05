package shorebridge

import (
	"context"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
)

type shoreLockChecker struct {
	bridge *Bridge
}

func (b *Bridge) PolicyLocks() command.LockChecker {
	return shoreLockChecker{bridge: b}
}

func (c shoreLockChecker) CheckLocks(rules []command.Rule) ([]string, bool) {
	if c.bridge == nil {
		return nil, false
	}
	active, err := c.bridge.getPolicy(context.Background())
	if err != nil || active == nil {
		return nil, false
	}
	converted := make([]policy.RuleLabels, len(rules))
	for i, rule := range rules {
		converted[i] = policy.RuleLabels{
			Type: rule.Type, Weight: rule.Weight,
			Classes: rule.Classes, Attrs: rule.Attrs,
		}
	}
	return policy.CheckCommandRules(policy.Locks(active.Layers), pb.Echelon_ECHELON_SHIP, converted)
}
