package command

import (
	"math"
	"slices"
	"sort"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

type Light struct {
	Colour   string  `json:"colour"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Flashing bool    `json:"flashing"`
}

type LightSignal struct {
	RuleID         string  `json:"ruleId"`
	Line           string  `json:"line"`
	Meaning        string  `json:"meaning"`
	Ref            string  `json:"ref"`
	Severity       string  `json:"severity"`
	Aspect         string  `json:"aspect"`
	Boost          float64 `json:"boost"`
	Assisted       bool    `json:"assisted,omitempty"`
	LowConfidence  bool    `json:"lowConfidence,omitempty"`
	ConfidenceNote string  `json:"confidenceNote,omitempty"`
}

func gateLightSignal(lights []Light, signal *LightSignal) *LightSignal {
	if signal == nil {
		return nil
	}
	for _, rule := range catalog.LightRules {
		if rule.ID != signal.RuleID {
			continue
		}
		result := *signal
		colours := make([]string, 0, len(lights))
		for _, light := range lights {
			colours = append(colours, light.Colour)
		}
		want := append([]string(nil), rule.Pattern.Colours...)
		slices.Sort(colours)
		slices.Sort(want)
		assisted := rule.Pattern.Signal != "" && result.Assisted
		result.LowConfidence = !assisted && (rule.Pattern.Signal != "" || !slices.Equal(colours, want))
		result.ConfidenceNote = ""
		result.Boost = rule.Boost
		if result.LowConfidence {
			result.Boost = 0
			result.ConfidenceNote = "extra unexplained lights — verify, no priority boost"
		}
		return &result
	}
	return nil
}

func OpticalNight(item *Item) bool {
	if item.Night == nil || !*item.Night {
		return false
	}
	for _, source := range catalog.Sources {
		if source.ID == item.SourceID {
			return source.Mode == "eo"
		}
	}
	return false
}

func InterpretLights(lights []Light, box Box, signal string) *LightSignal {
	if box.W <= 0 || box.H <= 0 {
		return nil
	}
	near := func(a, b Light) bool { return math.Abs(a.X-b.X)/box.W < .09 }
	isolated := func(i int) bool {
		for j := range lights {
			if i != j && near(lights[i], lights[j]) {
				return false
			}
		}
		return true
	}
	aspect := "unknown"
	red, green := false, false
	var whites []Light
	var groups [][]Light
	for i, p := range lights {
		if isolated(i) {
			red = red || p.Colour == "red"
			green = green || p.Colour == "green"
		}
		if p.Colour == "white" {
			whites = append(whites, p)
		}
		found := false
		for j := range groups {
			if near(groups[j][0], p) {
				groups[j] = append(groups[j], p)
				found = true
				break
			}
		}
		if !found {
			groups = append(groups, []Light{p})
		}
	}
	switch {
	case red && green:
		aspect = "head_on"
	case red:
		aspect = "port_side_crossing_right_to_left"
	case green:
		aspect = "starboard_side_crossing_left_to_right"
	}
	if !red && !green && len(whites) == 2 {
		sort.SliceStable(whites, func(i, j int) bool { return whites[i].Y < whites[j].Y })
		if math.Abs(whites[1].X-whites[0].X)/box.W > .15 {
			aspect = "port_side_crossing_right_to_left"
			if whites[1].X > whites[0].X {
				aspect = "starboard_side_crossing_left_to_right"
			}
		}
	}
	if signal != "" {
		aspect = "unknown"
	}
	for _, rule := range catalog.LightRules {
		pattern := rule.Pattern
		matched := false
		switch {
		case signal != "":
			matched = pattern.Signal == signal
		case pattern.Signal != "":
			continue
		case pattern.Layout == "vertical":
			for _, group := range groups {
				ordered := append([]Light(nil), group...)
				sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Y < ordered[j].Y })
				var colors []string
				for _, p := range ordered {
					colors = append(colors, p.Colour)
				}
				matched = matched || slices.Equal(colors, pattern.Colours)
			}
		default:
			var colors []string
			for i, p := range lights {
				if pattern.Isolated && !isolated(i) {
					continue
				}
				if len(pattern.Select) > 0 && !slices.Contains(pattern.Select, p.Colour) {
					continue
				}
				colors = append(colors, p.Colour)
			}
			want := append([]string(nil), pattern.Colours...)
			slices.Sort(colors)
			slices.Sort(want)
			matched = slices.Equal(colors, want)
		}
		if matched && rule.Flashing != nil {
			matched = slices.ContainsFunc(lights, func(p Light) bool { return p.Flashing == *rule.Flashing })
		}
		if matched {
			return gateLightSignal(lights, &LightSignal{RuleID: rule.ID, Line: rule.Line, Meaning: rule.Meaning, Ref: rule.Ref, Severity: rule.Severity, Aspect: aspect, Boost: rule.Boost, Assisted: signal != ""})
		}
	}
	return nil
}

func NightDetections(image catalog.NightImage) []Detection {
	result := make([]Detection, 0, len(image.Dets))
	for _, d := range image.Dets {
		det := Detection{Class: d.Cls, Box: Box{X: d.B.X, Y: d.B.Y, W: d.B.W, H: d.B.H}, Conf: .85}
		for _, p := range d.Lights {
			det.Lights = append(det.Lights, Light{Colour: p.Colour, X: p.X / 2, Y: p.Y / 2, Flashing: p.Flashing})
		}
		signal := ""
		if d.Assisted {
			signal = d.Signal
		}
		det.Signal = InterpretLights(det.Lights, det.Box, signal)
		result = append(result, det)
	}
	return result
}
