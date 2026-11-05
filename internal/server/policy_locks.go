package server

import (
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
)

type runtimeLockChecker struct {
	rt *policyRuntime
}

func (s *Server) PolicyLocks() command.LockChecker {
	return runtimeLockChecker{rt: s.policy}
}

func (c runtimeLockChecker) CheckLocks(rules []command.Rule) ([]string, bool) {
	if c.rt == nil || c.rt.guard == nil {
		return nil, false
	}
	now := time.Now()
	active, _ := c.rt.guard.Active(now)
	if active == nil {
		return nil, false
	}
	converted := make([]policy.RuleLabels, len(rules))
	for i, rule := range rules {
		converted[i] = policy.RuleLabels{
			Type: rule.Type, Weight: rule.Weight, Tier: rule.Tier,
			Classes: rule.Classes, Attrs: rule.Attrs,
		}
	}
	return policy.CheckCommandRules(policy.ActiveLocks(active.Layers, c.rt.nodeID, now), pb.Echelon_ECHELON_SHIP, converted)
}
