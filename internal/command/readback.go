package command

import (
	"fmt"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func readback(rules []Rule, durationMin int) string {
	boost, suppress, targets := make([]string, 0), make([]string, 0), make([]string, 0)
	focus := false
	for _, rule := range rules {
		phrase := readbackRule(rule)
		if rule.Tier >= 1 && rule.Tier <= 4 {
			targets = append(targets, "Set "+phrase)
			continue
		}
		if rule.Type == "boost" {
			boost = append(boost, phrase)
			focus = focus || rule.Only
		} else if rule.Type == "suppress" {
			suppress = append(suppress, phrase)
		}
	}
	if len(boost) == 0 && len(suppress) == 0 && len(targets) == 0 {
		return "No rules recognized."
	}
	parts := make([]string, 0, 4)
	if len(targets) > 0 {
		parts = append(parts, strings.Join(targets, "; ")+".")
	}
	if len(boost) > 0 {
		parts = append(parts, "Boost: "+strings.Join(boost, "; ")+".")
	}
	if focus {
		parts = append(parts, "Focus: other contacts ×0.3.")
	}
	if len(suppress) > 0 {
		parts = append(parts, "Suppress: "+strings.Join(suppress, "; ")+".")
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
