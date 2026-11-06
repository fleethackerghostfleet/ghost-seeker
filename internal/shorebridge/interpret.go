package shorebridge

import (
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

var suppressVerbs = []string{"ignore", "suppress", "deprioritize", "deprioritise", "mute", "skip", "drop", "hide", "stop showing", "don't show", "do not show", "not interested in"}
var boostVerbs = []string{"prioritize", "prioritise", "boost", "focus on", "look for", "find", "watch for", "flag", "escalate", "show me", "interested in", "want"}

// Interpret turns a free-text directive into shore policy label lists.
// "ignore cargo ships; prioritize submarines" -> suppress [cargo ship],
// boost [submarine]. Labels are singularised to match detector labels.
func Interpret(text string) (suppress, boost []string) {
	for _, clause := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return r == ';' || r == ',' || r == '.' || r == '\n' }) {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if verb, rest, ok := leadVerb(clause, suppressVerbs); ok {
			_ = verb
			suppress = append(suppress, labels(rest)...)
		} else if verb, rest, ok := leadVerb(clause, boostVerbs); ok {
			_ = verb
			boost = append(boost, labels(rest)...)
		} else if classes := command.ObservationClasses(clause); len(classes) > 0 {
			boost = append(boost, classes...)
		}
	}
	return
}

func interpretKnown(text string) (suppress, boost []string) {
	suppress, boost = Interpret(text)
	for _, label := range append(append([]string{}, suppress...), boost...) {
		if _, ok := catalog.Classes[label]; !ok && !catalog.EdgeSubtypes[label] {
			return nil, nil
		}
	}
	return suppress, boost
}

func leadVerb(clause string, verbs []string) (string, string, bool) {
	for _, v := range verbs {
		if strings.HasPrefix(clause, v+" ") {
			return v, strings.TrimSpace(clause[len(v):]), true
		}
	}
	return "", "", false
}

func labels(rest string) []string {
	rest = strings.TrimPrefix(rest, "the ")
	rest = strings.TrimPrefix(rest, "all ")
	rest = strings.TrimPrefix(rest, "any ")
	var out []string
	for _, part := range strings.Split(rest, " and ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		words := strings.Fields(part)
		last := words[len(words)-1]
		switch {
		case strings.HasSuffix(last, "ies"):
			last = strings.TrimSuffix(last, "ies") + "y"
		case strings.HasSuffix(last, "sses"):
			last = strings.TrimSuffix(last, "es")
		case strings.HasSuffix(last, "s") && !strings.HasSuffix(last, "ss"):
			last = strings.TrimSuffix(last, "s")
		}
		words[len(words)-1] = last
		label := strings.Join(words, " ")
		if alias, ok := catalog.CanonicalAlias(part); ok {
			label = alias
		}
		out = append(out, label)
	}
	return out
}
