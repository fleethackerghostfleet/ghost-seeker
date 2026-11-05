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
	active, _ := c.rt.guard.Active(time.Now())
	if active == nil {
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
