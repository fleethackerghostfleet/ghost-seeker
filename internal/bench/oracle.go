// Package bench scores the edge triage and command models against a mission oracle on synthetic frames.
package bench

import "github.com/fleethackerghostfleet/ghost-seeker/internal/command"

// Missions maps each command model to the mission whose oracle it is judged against.
var Missions = map[string]string{"maritime": "maritime", "air": "air", "sar": "sar"}

// OracleTier is the precedence a commander running the mission would want for one true contact.
// It is a written spec, independent of model weights: confidence and false positives are ignored.
func OracleTier(mission string, d command.Detection) int {
	a := d.Attrs
	if d.Class == "unknown" {
		if a.Transient {
			return 4
		}
		return pick(a.Approaching || a.Dark, 1, 2)
	}
	switch mission {
	case "air":
		switch d.Class {
		case "military aircraft":
			return 1
		case "drone":
			return pick(a.Approaching || a.Loitering || a.Dark, 1, 2)
		case "helicopter":
			return pick(a.Approaching || a.Dark, 1, 2)
		case "civil aircraft":
			if a.Dark {
				return 1
			}
			return pick(a.Approaching, 3, 4)
		case "submarine":
			return 2
		case "warship", "person":
			return 3
		}
		return 4
	case "sar":
		switch d.Class {
		case "person":
			return 1
		case "debris":
			return 2
		case "small boat", "fishing vessel":
			if a.Loitering || a.Dark {
				return 2
			}
			return pick(d.Class == "small boat", 3, 4)
		case "helicopter":
			return 3
		}
		return 4
	}
	switch d.Class {
	case "submarine", "person":
		return 1
	case "warship", "military aircraft":
		return pick(a.Approaching || a.Dark, 1, 2)
	case "drone":
		return pick(a.Approaching || a.Loitering, 1, 2)
	case "small boat":
		if a.Approaching && a.Dark {
			return 1
		}
		return pick(a.Approaching || a.Loitering || a.Dark, 2, 3)
	case "cargo ship", "tanker", "fishing vessel":
		if a.Dark {
			return 2
		}
		return pick(a.Approaching || a.Loitering, 3, 4)
	case "helicopter":
		return pick(a.Approaching || a.Dark, 2, 3)
	case "civil aircraft":
		return pick(a.Dark, 2, 4)
	case "debris":
		return 3
	}
	return 4
}

// FrameTier is the most urgent oracle tier among a frame's true contacts (4 when it has none).
func FrameTier(mission string, dets []command.Detection, fp []bool) int {
	tier := 4
	for i, d := range dets {
		if !fp[i] {
			tier = min(tier, OracleTier(mission, d))
		}
	}
	return tier
}

func pick(cond bool, yes, no int) int {
	if cond {
		return yes
	}
	return no
}
