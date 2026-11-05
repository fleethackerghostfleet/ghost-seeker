package reviews

import "errors"

type Effect struct {
	Tier, HumanTier int
	Dismissed, Sent *bool
}

func ReviewEffect(action string, tier, suggested int) (Effect, error) {
	e := Effect{Tier: tier}
	yes := true
	switch action {
	case "tier":
		if tier < 1 || tier > 4 {
			return e, errors.New("tier must be 1–4")
		}
	case "accept":
		e.Tier = suggested
	case "dismiss":
		e.Tier, e.Dismissed = 4, &yes
	case "cdr":
		e.Tier, e.Sent = 1, &yes
	default:
		return e, errors.New("unknown review action")
	}
	e.HumanTier = e.Tier
	if action == "dismiss" {
		e.HumanTier = 0
	}
	return e, nil
}
