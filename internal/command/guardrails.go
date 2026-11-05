package command

import (
	"regexp"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

var (
	everythingPattern           = regexp.MustCompile(`\b(everything|anything|all (contacts|objects|targets|classes|tracks)|every (contact|object|target|track))\b`)
	dropEverythingPrefixPattern = regexp.MustCompile(`\bdrop\s+$`)
)

func guardrails(text string, rules []Rule, caps CapabilityProvider, locks LockChecker) ([]string, bool) {
	warnings := make([]string, 0)
	blocked := false
	classSet := make(map[string]bool)
	for _, rule := range rules {
		if rule.Type != "boost" || len(rule.Attrs) > 0 || len(rule.Sources) > 0 || rule.WithinMin != 0 {
			continue
		}
		for _, class := range rule.Classes {
			classSet[class] = true
		}
	}
	if len(classSet)*5 >= len(catalog.Classes)*4 {
		blocked = true
	}
	for _, clause := range splitPattern.Split(strings.ToLower(text), -1) {
		for _, match := range everythingPattern.FindAllStringIndex(clause, -1) {
			if negativePattern.MatchString(clause[:match[0]]) {
				continue
			}
			if strings.EqualFold(clause[match[0]:match[1]], "everything") &&
				dropEverythingPrefixPattern.MatchString(clause[:match[0]]) {
				continue
			}
			blocked = true
			break
		}
		if blocked {
			break
		}
	}
	if blocked {
		warnings = append(warnings, "Blocked: this would make everything top priority — name the contacts that matter most.")
	}
	if caps != nil {
		capabilities := caps.Capabilities()
		if !capabilities.OpenVocabulary {
			labels := make(map[string]bool)
			for _, label := range capabilities.FixedLabels {
				labels[strings.ToLower(label)] = true
			}
			classes := make(map[string]bool)
			for _, rule := range rules {
				if rule.Type != "boost" {
					continue
				}
				for _, class := range rule.Classes {
					if !labels[strings.ToLower(class)] && !classes[strings.ToLower(class)] {
						warnings = append(warnings, "Cannot detect "+class+": the analyzer only knows "+strings.Join(capabilities.FixedLabels, ", ")+".")
						classes[strings.ToLower(class)] = true
					}
				}
			}
		}
	}
	if locks != nil {
		lockWarnings, lockBlocked := locks.CheckLocks(rules)
		warnings = append(warnings, lockWarnings...)
		blocked = blocked || lockBlocked
	}
	return uniqueStrings(warnings), blocked
}
