package shorebridge

import "strings"

// transportLabels bridges two existing machine taxonomies: the demo catalog
// calls the class fishing vessel; MobileCLIP-S1 emits fishing boat. This is
// applied after language compilation and never interprets operator phrasing.
// Keep both labels so existing policies, overrides and sidecar data still match.
func transportLabels(labels []string) []string {
	out := []string{}
	for _, label := range labels {
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "fishing vessel", "fishing boat":
			out = append(out, "fishing vessel", "fishing boat")
		default:
			out = append(out, label)
		}
	}
	return dedupe(out)
}

// displayLabels keeps transport aliases out of editable demo class lists.
func displayLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), "fishing boat") {
			label = "fishing vessel"
		}
		out = append(out, label)
	}
	return dedupe(out)
}
