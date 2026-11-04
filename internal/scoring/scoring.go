// Package scoring implements the command-level prioritization model that
// rescores edge-triaged imagery, optionally reweighted by operator directives.
package scoring

import (
	"math"
	"slices"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

// RuleRef points at a rule inside a directive.
type RuleRef struct {
	Directive string `json:"d"`
	Rule      int    `json:"r"`
}

// DetScore is the command-model score for a single detection.
type DetScore struct {
	S       float64   `json:"s"`
	Matched bool      `json:"matched"`
	Sup     bool      `json:"sup"`
	Focus   bool      `json:"focus"`
	Notes   []RuleRef `json:"notes"`
}

// Result is the command-model output for one image.
type Result struct {
	Det     []DetScore `json:"det"`
	Central float64    `json:"central"`
	Base    float64    `json:"base"`
}

// Config is the active command model plus operator directives.
type Config struct {
	Model      string      `json:"model"`
	Directives []Directive `json:"directives"`
}

type ruleWithRef struct {
	Rule
	ref RuleRef
}

func (c Config) rules() []ruleWithRef {
	var out []ruleWithRef
	for _, d := range c.Directives {
		for i, r := range d.Rules {
			out = append(out, ruleWithRef{r, RuleRef{d.ID, i}})
		}
	}
	return out
}

func matches(r Rule, d catalog.Detection) bool {
	if len(r.Classes) > 0 && !slices.Contains(r.Classes, d.Class) {
		return false
	}
	if len(r.Attrs) == 0 {
		return true
	}
	for _, a := range r.Attrs {
		if d.Attrs[a] {
			return true
		}
	}
	return false
}

func scoreDet(model string, d catalog.Detection, rules []ruleWithRef) DetScore {
	w := catalog.Models[model].Weights[d.Class]
	g := catalog.Classes[d.Class].Group
	if g == "vessel" || g == "aircraft" {
		if d.Attrs["approaching"] {
			w = math.Min(1, w*1.15)
		}
		if d.Attrs["dark"] {
			w = math.Min(1, w*1.2)
		}
	}
	boosts, only := 0, false
	for _, r := range rules {
		if r.Type == "boost" {
			boosts++
		}
		only = only || r.Only
	}
	out := DetScore{Notes: []RuleRef{}}
	for _, r := range rules {
		if !matches(r.Rule, d) {
			continue
		}
		if r.Type == "boost" {
			out.Matched = true
			k := .22
			if len(r.Classes) > 0 && len(r.Attrs) > 0 {
				k = .06
			}
			w = 1 - (1-w)*k
		} else {
			out.Sup = true
			w *= .12
		}
		out.Notes = append(out.Notes, r.ref)
	}
	if boosts > 0 && !out.Matched && !out.Sup {
		if only {
			w *= .3
		} else {
			w *= .6
		}
		out.Focus = true
	}
	out.S = math.Min(.99, w*d.Conf)
	return out
}

// NoisyOr combines independent per-object scores into one image score.
func NoisyOr(s []float64) float64 {
	p := 1.0
	for _, v := range s {
		p *= 1 - v
	}
	return 1 - p
}

// Score rescores one image's detections under the config.
func Score(c Config, dets []catalog.Detection) Result {
	rules := c.rules()
	res := Result{Det: make([]DetScore, len(dets))}
	s, b := make([]float64, len(dets)), make([]float64, len(dets))
	for i, d := range dets {
		res.Det[i] = scoreDet(c.Model, d, rules)
		s[i] = res.Det[i].S
		b[i] = scoreDet(c.Model, d, nil).S
	}
	res.Central, res.Base = NoisyOr(s), NoisyOr(b)
	return res
}

// Tier maps a command score to P1 (critical) .. P4 (low).
func Tier(s float64) int {
	switch {
	case s >= .8:
		return 1
	case s >= .6:
		return 2
	case s >= .35:
		return 3
	}
	return 4
}

// ClassWeight is the effective model weight of a class after directives.
type ClassWeight struct {
	Class string  `json:"k"`
	Base  float64 `json:"base"`
	Eff   float64 `json:"eff"`
	Cond  bool    `json:"cond"`
}

// Weights reports per-class weights for a confident detection with no
// behavior flags, sorted by effective weight.
func Weights(c Config) []ClassWeight {
	rules := c.rules()
	out := make([]ClassWeight, 0, len(catalog.ClassOrder))
	for _, k := range catalog.ClassOrder {
		d := catalog.Detection{Class: k, Conf: 1, Attrs: map[string]bool{}}
		cw := ClassWeight{Class: k, Base: catalog.Models[c.Model].Weights[k], Eff: scoreDet(c.Model, d, rules).S}
		for _, r := range rules {
			if r.Type == "boost" && len(r.Attrs) > 0 && (len(r.Classes) == 0 || slices.Contains(r.Classes, k)) {
				cw.Cond = true
			}
		}
		out = append(out, cw)
	}
	slices.SortStableFunc(out, func(a, b ClassWeight) int {
		switch {
		case a.Eff > b.Eff:
			return -1
		case a.Eff < b.Eff:
			return 1
		}
		return 0
	})
	return out
}
