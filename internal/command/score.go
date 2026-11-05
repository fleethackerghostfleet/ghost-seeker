package command

import (
	"math"
	"slices"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

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
		case "transient":
			attrMatch = attrMatch || d.Attrs.Transient
		}
	}
	return classMatch && attrMatch
}

func ScoreDet(d Detection, rules []Rule, model string, opticalNight ...bool) DetectionScore {
	w := ContactWeight(d, model)
	boosts, only, matched, suppressed := 0, false, false, false
	targetTier := 0
	notes := make([]Rule, 0)
	for _, r := range rules {
		if r.Only {
			only = true
		}
		if r.Type == "boost" && r.Tier == 0 {
			boosts++
		}
		if !RuleMatch(r, d) {
			continue
		}
		if r.Tier >= 1 && r.Tier <= 4 {
			targetTier = r.Tier
			matched = true
			notes = append(notes, r)
			continue
		}
		if r.Type == "boost" {
			matched = true
			factor := .22
			if len(r.Classes) > 0 && len(r.Attrs) > 0 {
				factor = .06
			}
			if r.Weight != 0 && r.Weight != 1 {
				factor = math.Pow(factor, r.Weight)
			}
			w = 1 - (1-w)*factor
			notes = append(notes, r)
		} else {
			suppressed = true
			factor := .12
			if r.Weight != 0 && r.Weight != 1 {
				factor = math.Pow(factor, r.Weight)
			}
			w *= factor
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
	signal := gateLightSignal(d.Lights, d.Box, d.Signal)
	light := len(opticalNight) > 0 && opticalNight[0] && signal != nil && signal.Boost > 0
	if light {
		w = 1 - (1-w)*(1-min(1, max(0, signal.Boost)))
	}
	confidence := ConfFactor(d.Conf)
	if d.Class == "unknown" {
		// Classification confidence is not evidence that a persistent contact is noise.
		confidence = 1
	}
	score := min(.99, w*confidence)
	if targetTier != 0 {
		score = [...]float64{0, .9, .7, .475, .175}[targetTier]
		suppressed = false
	}
	return DetectionScore{Score: score, Matched: matched, Sup: suppressed, Focus: focus, Notes: notes, Light: light}
}

// ConfSat is the detector confidence treated as certain; lower confidences scale the score down linearly.
var ConfSat = .7

// CrowdShare is how much of the noisy-or of a frame's secondary contacts adds to its top contact.
var CrowdShare = .1

func ConfFactor(conf float64) float64 { return min(1, conf/ConfSat) }

// ContactWeight is the model's class weight raised by the model's attribute lifts.
func ContactWeight(d Detection, model string) float64 {
	m := catalog.Models[model]
	w := m.W[d.Class]
	if d.Class == "unknown" && w > 0 {
		if d.Attrs.Transient {
			return .05
		}
		if d.Attrs.Approaching || d.Attrs.Dark {
			return max(w, .9)
		}
		return w
	}
	if !slices.Contains(m.LiftGroups, catalog.Classes[d.Class].Group) {
		return w
	}
	for attr, on := range map[string]bool{"approaching": d.Attrs.Approaching, "dark": d.Attrs.Dark, "loitering": d.Attrs.Loitering} {
		if on {
			w = 1 - (1-w)*(1-m.Lift[attr])
		}
	}
	return w
}

// FrameScore is the top contact's score plus a damped noisy-or of the rest, so crowds of minor contacts do not stack into P1.
func FrameScore(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(scores))
	top := sorted[len(sorted)-1]
	return top + (1-top)*CrowdShare*NoisyOr(sorted[:len(sorted)-1])
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
	RescoreAt(item, model, directives, time.Now())
}

func RescoreAt(item *Item, model string, directives []Directive, now time.Time) {
	rules := make([]Rule, 0)
	for _, directive := range directives {
		if directive.Blocked != "" {
			continue
		}
		for _, rule := range directive.Rules {
			if rule.Blocked != "" {
				continue
			}
			if len(rule.Sources) > 0 && !contains(rule.Sources, item.SourceID) {
				continue
			}
			if rule.WithinMin > 0 && item.TS.Before(now.Add(-time.Duration(rule.WithinMin)*time.Minute)) {
				continue
			}
			rules = append(rules, rule)
		}
	}
	item.Det = make([]DetectionScore, len(item.Dets))
	eligible := OpticalNight(item)
	if eligible {
		for i := range item.Dets {
			item.Dets[i].Signal = gateLightSignal(item.Dets[i].Lights, item.Dets[i].Box, item.Dets[i].Signal)
		}
	}
	if !eligible {
		for i := range item.Dets {
			item.Dets[i].Lights = nil
			item.Dets[i].Signal = nil
		}
		for _, source := range catalog.Sources {
			if source.ID == item.SourceID && source.Mode == "sar" {
				item.Night = nil
				item.Basis = "not_applicable_radar"
				item.Lum = 0
			}
		}
	}
	scores, baseScores := make([]float64, 0, len(item.Dets)), make([]float64, 0, len(item.Dets))
	for i, detection := range item.Dets {
		item.Det[i] = ScoreDet(detection, rules, model, eligible)
		scores = append(scores, item.Det[i].Score)
		baseScores = append(baseScores, ScoreDet(detection, nil, model, eligible).Score)
	}
	item.Central = FrameScore(scores)
	if len(rules) == 0 {
		item.Base = item.Central
	} else {
		item.Base = FrameScore(baseScores)
	}
	item.BaseTier = TierFor(item.Base)
	item.CentralTier = TierFor(item.Central)
	item.Tier = TierFor(item.Central)
	if item.Filtered != "" {
		item.Tier = 4
	}
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
		if directive.Blocked != "" {
			continue
		}
		for _, rule := range directive.Rules {
			if rule.Blocked == "" {
				rules = append(rules, rule)
			}
		}
	}
	result := make(map[string]Weight, len(catalog.Models[model].W))
	for class, base := range catalog.Models[model].W {
		unconditional := make([]Rule, 0, len(rules))
		conditional := false
		for _, rule := range rules {
			if len(rule.Attrs) == 0 && len(rule.Sources) == 0 && rule.WithinMin == 0 {
				unconditional = append(unconditional, rule)
			} else if len(rule.Classes) == 0 || contains(rule.Classes, class) {
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
