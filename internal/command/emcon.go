package command

import (
	"regexp"
	"sort"
	"strings"
)

// EMCON (transmit posture) phrases, ported from dynamic-interest's compiler
// vocabulary. A directive like "go quiet" or "resume normal ops" changes the
// edge's transmit posture instead of (or in addition to) its interest rules.
var emconPhrases = map[string][]string{
	"SILENT": {"go quiet", "going quiet", "go silent", "going silent", "radio silence", "emcon silent",
		"set emcon silent", "stop transmitting", "cease transmitting", "cease transmission",
		"cease transmissions", "go dark", "going dark", "zip lip", "hold all traffic"},
	"BURST": {"burst mode", "emcon burst", "burst only", "flash traffic only", "only flash traffic",
		"river city", "minimize traffic", "minimal traffic"},
	"NORMAL": {"go loud", "going loud", "break silence", "resume normal", "resume normal ops",
		"back to normal", "normal ops", "lift emcon", "emcon off", "emcon normal", "resume transmitting"},
}

var emconMatchers = func() []struct {
	mode string
	re   *regexp.Regexp
} {
	type entry struct {
		mode, phrase string
	}
	var entries []entry
	for mode, phrases := range emconPhrases {
		for _, p := range phrases {
			entries = append(entries, entry{mode, p})
		}
	}
	// Longest phrase first so "emcon normal" beats "emcon silent" prefixes
	// and "resume normal ops" beats "resume normal".
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].phrase) != len(entries[j].phrase) {
			return len(entries[i].phrase) > len(entries[j].phrase)
		}
		return entries[i].phrase < entries[j].phrase
	})
	out := make([]struct {
		mode string
		re   *regexp.Regexp
	}, 0, len(entries))
	for _, e := range entries {
		out = append(out, struct {
			mode string
			re   *regexp.Regexp
		}{e.mode, regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(e.phrase) + `\b`)})
	}
	return out
}()

// ParseEmcon returns the EMCON mode ("SILENT", "BURST" or "NORMAL") a
// directive asks for, or "" when it says nothing about transmit posture.
// When several phrases appear the last one in the text wins.
func ParseEmcon(text string) string {
	mode, _ := SplitEmcon(text)
	return mode
}

// SplitEmcon extracts the requested transmit posture and removes all EMCON
// phrases from the text passed to the directive rules parser.
func SplitEmcon(text string) (mode, rest string) {
	best, bestPos := "", -1
	spans := make([][2]int, 0)
	for _, m := range emconMatchers {
		for _, loc := range m.re.FindAllStringIndex(text, -1) {
			if loc[0] > bestPos {
				best, bestPos = m.mode, loc[0]
			}
			spans = append(spans, [2]int{loc[0], loc[1]})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i][0] != spans[j][0] {
			return spans[i][0] < spans[j][0]
		}
		return spans[i][1] > spans[j][1]
	})
	var out strings.Builder
	cursor := 0
	for _, span := range spans {
		if span[0] < cursor {
			continue
		}
		out.WriteString(text[cursor:span[0]])
		out.WriteByte(' ')
		cursor = span[1]
	}
	out.WriteString(text[cursor:])
	return best, strings.TrimSpace(out.String())
}
