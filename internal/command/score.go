package command

import "github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"

func RuleMatch(r Rule, d Detection) bool {
	classMatch := len(r.Classes) == 0
	for _, class := range r.Classes {
		if class == d.Class {
			classMatch = true
			break
		}
	}
	attrMatch := len(r.Attrs) == 0
	for _, attr := range r.Attrs {
		switch attr {
		case "approaching":
			attrMatch = attrMatch || d.Attrs.Approaching
		case "new":
			attrMatch = attrMatch || d.Attrs.New
		case "dark":
			attrMatch = attrMatch || d.Attrs.Dark
		case "moving":
			attrMatch = attrMatch || d.Attrs.Moving
		case "loitering":
			attrMatch = attrMatch || d.Attrs.Loitering
		}
	}
	return classMatch && attrMatch
}

func ScoreDet(d Detection, rules []Rule, model string) DetectionScore {
	cls := catalog.Classes[d.Class]
	w := catalog.Models[model].W[d.Class]
	if cls.Group == "vessel" || cls.Group == "aircraft" {
		if d.Attrs.Approaching {
			w = min(1, w*1.15)
		}
		if d.Attrs.Dark {
			w = min(1, w*1.2)
		}
	}
	boosts, only, matched, suppressed := 0, false, false, false
	notes := make([]Rule, 0)
	for _, r := range rules {
		if r.Only {
			only = true
		}
		if r.Type == "boost" {
			boosts++
		}
		if !RuleMatch(r, d) {
			continue
		}
		if r.Type == "boost" {
			matched = true
			factor := .22
			if len(r.Classes) > 0 && len(r.Attrs) > 0 {
				factor = .06
			}
			w = 1 - (1-w)*factor
			notes = append(notes, r)
		} else {
			suppressed = true
			w *= .12
			notes = append(notes, r)
		}
	}
	focus := boosts > 0 && !matched && !suppressed
	if focus {
		if only {
			w *= .3
		} else {
			w *= .6
		}
	}
	return DetectionScore{Score: min(.99, w*d.Conf), Matched: matched, Sup: suppressed, Focus: focus, Notes: notes}
}

func NoisyOr(scores []float64) float64 {
	remaining := 1.0
	for _, score := range scores {
		remaining *= 1 - score
	}
	return 1 - remaining
}

func TierFor(score float64) int {
	switch {
	case score >= .8:
		return 1
	case score >= .6:
		return 2
	case score >= .35:
		return 3
	default:
		return 4
	}
}

func Rescore(item *Item, model string, directives []Directive) {
	rules := make([]Rule, 0)
	for _, directive := range directives {
		rules = append(rules, directive.Rules...)
	}
	item.Det = make([]DetectionScore, len(item.Dets))
	scores, baseScores := make([]float64, 0, len(item.Dets)), make([]float64, 0, len(item.Dets))
	for i, detection := range item.Dets {
		item.Det[i] = ScoreDet(detection, rules, model)
		scores = append(scores, item.Det[i].Score)
		baseScores = append(baseScores, ScoreDet(detection, nil, model).Score)
	}
	item.Central = NoisyOr(scores)
	if len(rules) == 0 {
		item.Base = item.Central
	} else {
		item.Base = NoisyOr(baseScores)
	}
	item.BaseTier = TierFor(item.Base)
	item.CentralTier = TierFor(item.Central)
	item.Tier = TierFor(item.Central)
	if item.Manual != 0 {
		item.Tier = item.Manual
	}
}

type Weight struct {
	Base        float64 `json:"base"`
	Effective   float64 `json:"effective"`
	Conditional bool    `json:"conditional"`
}

func EffectiveWeights(model string, directives []Directive) map[string]Weight {
	rules := make([]Rule, 0)
	for _, directive := range directives {
		rules = append(rules, directive.Rules...)
	}
	result := make(map[string]Weight, len(catalog.Models[model].W))
	for class, base := range catalog.Models[model].W {
		unconditional := make([]Rule, 0, len(rules))
		conditional := false
		for _, rule := range rules {
			if len(rule.Attrs) == 0 {
				unconditional = append(unconditional, rule)
			} else if rule.Type == "boost" && (len(rule.Classes) == 0 || contains(rule.Classes, class)) {
				conditional = true
			}
		}
		detection := Detection{Class: class, Conf: 1}
		result[class] = Weight{Base: base, Effective: ScoreDet(detection, unconditional, model).Score, Conditional: conditional}
	}
	return result
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
