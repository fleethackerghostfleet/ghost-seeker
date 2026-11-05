package shorebridge

import (
	"context"
	"time"

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
	now := time.Now()
	converted := make([]policy.RuleLabels, len(rules))
	for i, rule := range rules {
		converted[i] = policy.RuleLabels{
			Type: rule.Type, Weight: rule.Weight,
			Classes: transportLabels(rule.Classes), Attrs: rule.Attrs,
		}
	}
	c.bridge.mu.Lock()
	nodeID := c.bridge.edgeNodeID
	c.bridge.mu.Unlock()
	return policy.CheckCommandRules(policy.ActiveLocks(active.Layers, nodeID, now), c.bridge.echelon(), converted)
}
