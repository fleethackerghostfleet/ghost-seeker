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
	// EdgeSubtypes accepts edge sidecar subtypes (catalog.EdgeSubtypes) that only live shore mode can match.
	EdgeSubtypes bool
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
	if c != nil {
		rules, unknown, resolutions := parseOpen(rest, c.EdgeSubtypes)
		for _, rule := range rules {
			for _, class := range rule.Classes {
				// Fully understood mammal directives stay deterministic; typos
				// and nearest-class guesses still consult the LLM.
				if (c.EdgeSubtypes && catalog.EdgeSubtypes[class]) || (class == "marine mammal" && len(unknown) == 0 && len(resolutions) == 0) {
					return c.finishResolved(text, rules, unknown, durationMin, "rules", warnings, resolutions)
				}
			}
		}
	}
	if c != nil && c.LLM != nil {
		draft, err := c.LLM.Draft(ctx, text)
		if err == nil {
			if compiled, ok := c.fromDraft(text, draft, durationMin, warnings); ok {
				return compiled
			}
			_, err = validateDraft(draft)
			if err == nil {
				err = fmt.Errorf("no rules")
			}
		}
		warnings = append(warnings, fmt.Sprintf("%s unavailable (%s); used rule-based parser.", c.LLM.label(), shortReason(err.Error(), 100)))
	}
	return c.compileOpen(text, rest, durationMin, warnings)
}

func (c *Compiler) compileOpen(text, rest string, durationMin int, warnings []string) Compiled {
	rules, unknown, resolutions := parseOpen(rest, c != nil && c.EdgeSubtypes)
	return c.finishResolved(text, rules, unknown, durationMin, "rules", warnings, resolutions)
}

func (c *Compiler) fromDraft(text string, draft llmDraft, durationMin int, warnings []string) (Compiled, bool) {
	rules, err := validateDraft(draft)
	if err != nil || len(rules) == 0 {
		return Compiled{}, false
	}
	if durationMin == 0 && draft.DurationMin != nil {
		durationMin = *draft.DurationMin
	}
	resolutions := make([]ResearchResolution, 0, len(draft.Mappings))
	if len(draft.Mappings) == 0 {
		// The model did not explain its mappings; disclose the parser's
		// nearest-class guesses that the model's rules agree with.
		_, rest := extractDuration(text)
		_, _, parsed := parseOpen(rest, c != nil && c.EdgeSubtypes)
		used := make(map[string]bool)
		for _, rule := range rules {
			for _, class := range rule.Classes {
				used[class] = true
			}
		}
		for _, resolution := range parsed {
			agrees := resolution.Reason == "nearest class"
			for _, class := range strings.Split(resolution.Canonical, ", ") {
				agrees = agrees && used[class]
			}
			if agrees {
				resolutions = append(resolutions, resolution)
			}
		}
	}
	for _, mapping := range draft.Mappings {
		classes := make([]string, 0, len(mapping.Classes))
		for _, label := range mapping.Classes {
			if class, ok := canonicalClass(strings.ToLower(strings.TrimSpace(label)), false); ok {
				classes = append(classes, class)
			}
		}
		phrase := strings.TrimSpace(mapping.Phrase)
		if phrase != "" && len(classes) > 0 && len(phrase) <= 60 {
			resolutions = appendResolution(resolutions, ResearchResolution{Input: phrase, Canonical: strings.Join(classes, ", "), Reason: "nearest class"})
		}
	}
	return c.finishResolved(text, rules, []string{}, durationMin, "llm", warnings, resolutions), true
}

func hasEdgeSubtype(text string) bool {
	rules, _ := parseDirectiveDetailed(text, true)
	for _, rule := range rules {
		for _, class := range rule.Classes {
			if catalog.EdgeSubtypes[class] {
				return true
			}
		}
	}
	return false
}

// CompileRules never waits on the LLM: it returns a cached LLM compile when
// one exists, otherwise the rule parser while the LLM warms the cache.
func (c *Compiler) CompileRules(text string) Compiled {
	durationMin, rest := extractDuration(text)
	if c != nil && c.LLM != nil && !(c.EdgeSubtypes && hasEdgeSubtype(rest)) {
		if draft, ok := c.LLM.Cached(text); ok {
			if compiled, ok := c.fromDraft(text, draft, durationMin, []string{}); ok {
				return compiled
			}
		}
		c.LLM.Prefetch(text)
	}
	return c.compileOpen(text, rest, durationMin, []string{})
}

func (c *Compiler) Review(text string, rules []Rule, durationMin int, unknown ...string) Compiled {
	if durationMin == 0 {
		durationMin, _ = extractDuration(text)
	}
	return c.finish(text, rules, unknown, durationMin, "edited", []string{})
}

func (c *Compiler) finish(text string, rules []Rule, unknown []string, durationMin int, path string, warnings []string) Compiled {
	return c.finishResolved(text, rules, unknown, durationMin, path, warnings, nil)
}

func (c *Compiler) finishResolved(text string, rules []Rule, unknown []string, durationMin int, path string, warnings []string, resolutions []ResearchResolution) Compiled {
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
	readbackText := readback(rules, durationMin)
	if mapped := resolutionReadback(resolutions); mapped != "" && len(rules) > 0 {
		readbackText += " " + mapped
	}
	return Compiled{
		Rules: rules, Unknown: append([]string{}, unknown...), DurationMin: durationMin,
		Path: path, Readback: readbackText, Warnings: warnings, Blocked: blocked,
		Resolutions: resolutions,
	}
}

func (c *Compiler) unsupportedReadback(concepts []string) string {
	quoted := make([]string, len(concepts))
	for i, concept := range concepts {
		quoted[i] = fmt.Sprintf("%q", concept)
	}
	return "No matching imagery yet for " + strings.Join(quoted, ", ") + " — the feed will update as it arrives."
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
			class, ok := canonicalClass(label, false)
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

func canonicalClass(label string, edgeSubtypes bool) (string, bool) {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		return "", false
	}
	if _, ok := catalog.Classes[label]; ok {
		return label, true
	}
	if edgeSubtypes {
		for _, pattern := range edgeConceptPatterns {
			if pattern.re.FindString(label) == label {
				return pattern.class, true
			}
		}
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
	if edgeSubtypes && catalog.EdgeSubtypes[label] {
		return label, true
	}
	if alias, ok := catalog.CanonicalAlias(label); ok && alias != label {
		return canonicalClass(alias, edgeSubtypes)
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
