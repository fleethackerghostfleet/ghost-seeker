package command

import (
	"regexp"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/openvocab"
)

var conceptJoin = regexp.MustCompile(`\b(and|or)\b`)
var requestWords = regexp.MustCompile(`\b(can you|could you|i am|i'm|look for|watch for|search for|keep an eye out for|detect|find|spot|look|watch|search|for|interested in|interested|want|see|send|me|us)\b`)

func openConcepts(clause string) ([]string, string) {
	out := []string{}
	rejected := []string{}
	clause = requestWords.ReplaceAllString(negativePattern.ReplaceAllString(clause, " "), " ")
	for _, part := range conceptJoin.Split(clause, -1) {
		part = grammarPattern.ReplaceAllString(part, " ")
		part = strings.Trim(part, " \t\r\n?!:\"'()")
		if strings.TrimSpace(part) == "" {
			continue
		}
		if label, ok := openvocab.Normalize(part); ok {
			out = append(out, label)
		} else {
			rejected = append(rejected, part)
		}
	}
	return out, strings.Join(rejected, " ")
}

func (c *Compiler) openVocabulary() bool {
	return c != nil && c.Capabilities != nil && c.Capabilities.Capabilities().OpenVocabulary
}

func (c *Compiler) parse(text string) ([]Rule, []string) {
	return parseDirectiveVocabulary(text, c != nil && c.EdgeSubtypes, c.openVocabulary())
}

func openVocabularyCount(rules []Rule) int {
	seen := map[string]bool{}
	for _, rule := range rules {
		for _, label := range rule.Classes {
			if !openvocab.Closed(strings.ToLower(label)) {
				seen[label] = true
			}
		}
	}
	return len(seen)
}
