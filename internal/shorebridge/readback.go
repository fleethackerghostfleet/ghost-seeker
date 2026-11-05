package shorebridge

import (
	"sort"
	"strings"
)

// Readback is the deterministic sentence the console shows after a directive
// is applied, so the operator can confirm what the shore actually took.
func Readback(suppress, boost []string) string {
	var parts []string
	if len(boost) > 0 {
		parts = append(parts, "Boost: "+joinLabels(boost)+".")
	}
	if len(suppress) > 0 {
		parts = append(parts, "Deprioritize (not delete): "+joinLabels(suppress)+".")
	}
	if len(parts) == 0 {
		return "No standing directive. Feed ranked by the edge priority score only."
	}
	return strings.Join(parts, " ") + " Until cleared."
}

func joinLabels(labels []string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(strings.ToLower(l))
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
