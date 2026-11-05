package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

type Capabilities struct {
	OpenVocabulary bool     `json:"openVocabulary"`
	FixedLabels    []string `json:"fixedLabels"`
}

type CapabilityProvider interface {
	Capabilities() Capabilities
}

type LockChecker interface {
	CheckLocks(rules []Rule) (warnings []string, blocked bool)
}

type Compiler struct {
	LLM          *LLMClient
	Research     *ResearchClient
	Capabilities CapabilityProvider
	Locks        LockChecker
}

type Compiled struct {
	Rules       []Rule               `json:"rules"`
	Unknown     []string             `json:"unknown"`
	DurationMin int                  `json:"durationMin,omitempty"`
	Path        string               `json:"path"`
	Readback    string               `json:"readback"`
	Warnings    []string             `json:"warnings"`
	Blocked     bool                 `json:"blocked"`
	Resolutions []ResearchResolution `json:"resolutions,omitempty"`
}

func (c *Compiler) Compile(ctx context.Context, text string) Compiled {
	if c != nil && c.Research != nil {
		return c.compileResearch(ctx, text)
	}
	durationMin, rest := extractDuration(text)
	warnings := make([]string, 0)
	if c != nil && c.LLM != nil {
		draft, err := c.LLM.Draft(ctx, text)
		if err == nil {
			rules, validateErr := validateDraft(draft)
			if validateErr == nil && len(rules) > 0 {
				compiledDuration := durationMin
				if compiledDuration == 0 && draft.DurationMin != nil {
					compiledDuration = *draft.DurationMin
				}
				return c.finish(text, rules, []string{}, compiledDuration, "llm", warnings)
			}
			if validateErr != nil {
				err = validateErr
			} else {
				err = fmt.Errorf("no rules")
			}
		}
		warnings = append(warnings, fmt.Sprintf("Local LLM unavailable (%s); used rule-based parser.", shortReason(err.Error(), 100)))
	}
	rules, unknown := ParseDirectiveDetailed(rest)
	return c.finish(text, rules, unknown, durationMin, "rules", warnings)
}

func (c *Compiler) CompileRules(text string) Compiled {
	durationMin, rest := extractDuration(text)
	rules, unknown := ParseDirectiveDetailed(rest)
	return c.finish(text, rules, unknown, durationMin, "rules", []string{})
}

func (c *Compiler) Review(text string, rules []Rule, durationMin int) Compiled {
	if durationMin == 0 {
		durationMin, _ = extractDuration(text)
	}
	return c.finish(text, rules, []string{}, durationMin, "edited", []string{})
}

func (c *Compiler) finish(text string, rules []Rule, unknown []string, durationMin int, path string, warnings []string) Compiled {
	rules = cloneRules(rules)
	if unknown == nil {
		unknown = []string{}
	}
	warnings = append([]string{}, warnings...)
	caps, locks := CapabilityProvider(nil), LockChecker(nil)
	if c != nil {
		caps, locks = c.Capabilities, c.Locks
	}
	guardWarnings, blocked := guardrails(text, rules, caps, locks)
	warnings = uniqueStrings(append(warnings, guardWarnings...))
	return Compiled{
		Rules: rules, Unknown: append([]string{}, unknown...), DurationMin: durationMin,
		Path: path, Readback: readback(rules, durationMin), Warnings: warnings, Blocked: blocked,
	}
}

func cloneRules(rules []Rule) []Rule {
	cloned := make([]Rule, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].Classes = append([]string{}, rule.Classes...)
		cloned[i].Attrs = append([]string{}, rule.Attrs...)
		cloned[i].Sources = append([]string{}, rule.Sources...)
	}
	return cloned
}

func validateDraft(draft llmDraft) ([]Rule, error) {
	if draft.DurationMin != nil && (*draft.DurationMin < 1 || *draft.DurationMin > 10080) {
		return nil, fmt.Errorf("durationMin must be between 1 and 10080")
	}
	attrs := make(map[string]bool)
	for _, name := range attrNames() {
		attrs[name] = true
	}
	rules := make([]Rule, 0, len(draft.Rules))
	for _, rule := range draft.Rules {
		if rule.Type != "boost" && rule.Type != "suppress" {
			return nil, fmt.Errorf("unknown rule type %q", rule.Type)
		}
		classes := make([]string, 0, len(rule.Classes))
		classSeen := make(map[string]bool)
		for _, label := range rule.Classes {
			label = strings.ToLower(strings.TrimSpace(label))
			class, ok := canonicalClass(label)
			if !ok {
				return nil, fmt.Errorf("unknown class %q", label)
			}
			if !classSeen[class] {
				classes = append(classes, class)
				classSeen[class] = true
			}
		}
		cleanAttrs := make([]string, 0, len(rule.Attrs))
		attrSeen := make(map[string]bool)
		for _, attr := range rule.Attrs {
			attr = strings.ToLower(strings.TrimSpace(attr))
			if !attrs[attr] {
				return nil, fmt.Errorf("unknown attribute %q", attr)
			}
			if !attrSeen[attr] {
				cleanAttrs = append(cleanAttrs, attr)
				attrSeen[attr] = true
			}
		}
		sources := make([]string, 0, len(rule.Sources))
		sourceSeen := make(map[string]bool)
		for _, id := range rule.Sources {
			canonical, ok := canonicalSource(id)
			if !ok {
				return nil, fmt.Errorf("unknown source %q", id)
			}
			if !sourceSeen[canonical] {
				sources = append(sources, canonical)
				sourceSeen[canonical] = true
			}
		}
		if rule.WithinMin < 0 || rule.WithinMin > 1440 {
			return nil, fmt.Errorf("withinMin must be between 0 and 1440")
		}
		if len(classes) == 0 && len(cleanAttrs) == 0 && len(sources) == 0 && rule.WithinMin == 0 {
			continue
		}
		rule.Classes = classes
		rule.Attrs = cleanAttrs
		rule.Sources = sources
		if rule.Type == "suppress" {
			rule.Only = false
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func canonicalClass(label string) (string, bool) {
	label = strings.ToLower(strings.TrimSpace(label))
	if _, ok := catalog.Classes[label]; ok {
		return label, true
	}
	matches := make([]string, 0, 1)
	for _, pattern := range synonymPatterns {
		if pattern.re.FindString(label) == label {
			matches = append(matches, pattern.class)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

func canonicalSource(id string) (string, bool) {
	for _, source := range catalog.Sources {
		if strings.EqualFold(strings.TrimSpace(id), source.ID) {
			return source.ID, true
		}
	}
	return "", false
}

func shortReason(reason string, limit int) string {
	reason = strings.TrimSpace(reason)
	runes := []rune(reason)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	if reason == "" {
		return "request failed"
	}
	return reason
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}
