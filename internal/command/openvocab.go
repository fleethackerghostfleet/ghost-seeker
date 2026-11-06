package command

import (
	"regexp"
	"slices"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/openvocab"
)

var conceptJoin = regexp.MustCompile(`\b(and|or)\b`)
var requestWords = regexp.MustCompile(`\b(can you|could you|i am|i'm|look for|watch for|search for|keep an eye out for|detect|find|spot|look|watch|search|for|interested in|interested|want|see|send|me|us)\b`)

// Preserve a qualified noun before the closed-set matcher erases its noun.
func qualifiedConcepts(clause string, patterns []classPattern) (concepts, attrs []string, rest string) {
	rest = clause
	for _, part := range conceptJoin.Split(clause, -1) {
		residual, visual := part, part
		known := false
		for _, pattern := range patterns {
			if pattern.re.MatchString(residual) {
				known = true
				residual = pattern.re.ReplaceAllString(residual, " ")
			}
		}
		for _, pattern := range genericPatterns {
			if pattern.re.MatchString(residual) {
				known = true
				residual = pattern.re.ReplaceAllString(residual, " ")
			}
		}
		if !known {
			continue
		}
		var partAttrs []string
		for _, pattern := range attributePatterns {
			if pattern.re.MatchString(residual) {
				partAttrs = append(partAttrs, pattern.name)
				residual = pattern.re.ReplaceAllString(residual, " ")
				visual = pattern.re.ReplaceAllString(visual, " ")
			}
		}
		qualifiers, _ := openConcepts(residual)
		phrases, rejected := openConcepts(visual)
		if len(qualifiers) == 0 || len(phrases) != 1 || rejected != "" {
			continue
		}
		concepts = append(concepts, phrases[0])
		for _, attr := range partAttrs {
			if !slices.Contains(attrs, attr) {
				attrs = append(attrs, attr)
			}
		}
		rest = strings.Replace(rest, part, " ", 1)
	}
	return
}

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
