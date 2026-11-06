package command

import (
	"fmt"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

// NoMatchReadback is shown when a directive yields no rules.
const NoMatchReadback = "No matching imagery yet — the feed will update as it arrives."

func readback(rules []Rule, durationMin int) string {
	parts := make([]string, 0, len(rules)+1)
	for _, rule := range rules {
		phrase := readbackRule(rule)
		if rule.Tier >= 1 && rule.Tier <= 4 {
			parts = append(parts, "Set "+phrase+".")
			continue
		}
		if rule.Type == "boost" {
			parts = append(parts, "Boost: "+phrase+".")
			if rule.Only {
				parts = append(parts, "Focus: other contacts ×0.3.")
			}
		} else if rule.Type == "suppress" {
			parts = append(parts, "Suppress: "+phrase+".")
		}
	}
	if len(parts) == 0 {
		return NoMatchReadback
	}
	if durationMin == 0 {
		parts = append(parts, "Until cleared.")
	} else if durationMin > 0 {
		parts = append(parts, "For "+formatDuration(durationMin)+".")
	}
	return strings.Join(parts, " ")
}

func readbackRule(rule Rule) string {
	parts := make([]string, 0, 4)
	if len(rule.Classes) == 0 {
		parts = append(parts, "any object")
	} else {
		parts = append(parts, strings.Join(rule.Classes, ", "))
	}
	if len(rule.Attrs) > 0 {
		labels := make([]string, 0, len(rule.Attrs))
		for _, attr := range rule.Attrs {
			if label, ok := catalog.AttrLabels[attr]; ok {
				labels = append(labels, label)
			} else {
				labels = append(labels, attr)
			}
		}
		parts[0] += " (" + strings.Join(labels, ", ") + ")"
	}
	if len(rule.Sources) > 0 {
		parts = append(parts, "from "+strings.Join(rule.Sources, ", "))
	}
	if rule.WithinMin > 0 {
		parts = append(parts, fmt.Sprintf("in last %d min", rule.WithinMin))
	}
	if rule.Tier >= 1 && rule.Tier <= 4 {
		parts = append(parts, fmt.Sprintf("→ P%d %s", rule.Tier, [...]string{"", "Critical", "High", "Routine", "Low"}[rule.Tier]))
	}
	return strings.Join(parts, " ")
}

func formatDuration(minutes int) string {
	if minutes >= 48*60 && minutes%(24*60) == 0 {
		return fmt.Sprintf("%dd", minutes/(24*60))
	}
	hours, remainder := minutes/60, minutes%60
	parts := make([]string, 0, 2)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if remainder > 0 {
		parts = append(parts, fmt.Sprintf("%dm", remainder))
	}
	return strings.Join(parts, " ")
}
