package shorebridge

import (
	"context"
	"fmt"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"math"
	"net/http"
	"strings"
)

// rulesFromBody turns a directive (free text or explicit rules) into the
// shore's suppress/boost label lists.
func rulesFromBody(text string, rules *[]command.Rule) (suppress, boost []string, parsed []command.Rule) {
	tiers := map[string]int32{}
	if rules != nil && len(*rules) > 0 {
		for _, r := range *rules {
			labels := r.Classes
			if len(labels) == 0 {
				labels = r.Attrs
			}
			for _, label := range transportLabels(labels) {
				key := normLabel(label)
				if r.Tier > 0 {
					tiers[key] = int32(r.Tier)
					suppress = withoutLabels(suppress, map[string]bool{key: true})
					boost = withoutLabels(boost, map[string]bool{key: true})
				} else {
					delete(tiers, key)
				}
			}
			if r.Tier > 0 {
				continue
			}
			switch r.Type {
			case "suppress":
				suppress = append(suppress, labels...)
			default:
				boost = append(boost, labels...)
			}
		}
	} else if strings.TrimSpace(text) != "" {
		suppress, boost = Interpret(text)
	}
	suppress, boost = transportLabels(suppress), transportLabels(boost)
	return suppress, boost, policyRules(flatDirectiveID, suppress, boost, tiers)
}

func ruleTiers(rules []command.Rule) map[string]int32 {
	tiers := map[string]int32{}
	for _, rule := range rules {
		if rule.Tier > 0 {
			for _, label := range transportLabels(rule.Classes) {
				tiers[normLabel(label)] = int32(rule.Tier)
			}
		}
	}
	return tiers
}

func tierReadback(suppress, boost []string, tiers map[string]int32) string {
	if len(tiers) == 0 {
		return Readback(suppress, boost)
	}
	return (&command.Compiler{}).Review("", policyRules(flatDirectiveID, suppress, boost, tiers), 0).Readback
}

// compiled runs the shared NL compiler (#54) when the bridge has one, so the
// console gets the same readback/warnings/guardrails as the simulation. The
// edge scorer only knows flat class labels, so scoped rules are dropped with a
// warning; EMCON phrases become Policy.Emcon. Without a compiler it falls
// back to the shore interpreter.
type compiledDirective struct {
	Suppress, Boost []string
	Rules           []command.Rule
	Compiled        *command.Compiled
	Emcon           pb.EmconMode
	DurationMin     int
	Readback        string
	Warnings        []string
	Blocked         bool
	Path            string
}

const scopeWarning = "Not applied in shore mode: source, attribute and time scopes are not supported by the edge scorer."

// flatRules keeps the rules the edge scorer can honour (plain class labels)
// and reports whether any scoped rule was dropped.
func flatRules(rules []command.Rule) (kept []command.Rule, dropped bool) {
	kept = make([]command.Rule, 0, len(rules))
	for _, rule := range rules {
		if len(rule.Sources) == 0 && len(rule.Attrs) == 0 && rule.WithinMin == 0 {
			kept = append(kept, rule)
		} else {
			dropped = true
		}
	}
	return kept, dropped
}

func (c *Console) compileDirective(ctx context.Context, text string, rules *[]command.Rule, durationMin int, rulesOnly bool) compiledDirective {
	var out compiledDirective
	out.Path = "rules"
	out.Warnings = []string{}
	mode, ruleText := command.SplitEmcon(text)
	if mode != "" {
		out.Emcon = pb.EmconMode(pb.EmconMode_value["EMCON_MODE_"+mode])
	}
	if rules != nil && len(*rules) > 0 {
		kept, dropped := flatRules(*rules)
		out.Suppress, out.Boost, out.Rules = rulesFromBody("", &kept)
		out.Path = "edited"
		out.DurationMin = durationMin
		if dropped {
			out.Warnings = append(out.Warnings, scopeWarning)
		}
		out.Readback = tierReadback(out.Suppress, out.Boost, ruleTiers(out.Rules))
	} else if c.b.Compiler != nil && strings.TrimSpace(ruleText) != "" {
		var comp command.Compiled
		if rulesOnly && c.b.Compiler.Research == nil {
			comp = c.b.Compiler.CompileRules(ruleText)
		} else {
			comp = c.b.Compiler.Compile(ctx, ruleText)
		}
		if !comp.Blocked {
			kept, dropped := flatRules(comp.Rules)
			if dropped {
				prior, path := comp.Warnings, comp.Path
				comp = c.b.Compiler.Review(ruleText, kept, comp.DurationMin, comp.Unknown...)
				comp.Path = path
				comp.Warnings = mergeWarnings(prior, comp.Warnings, []string{scopeWarning})
			}
		}
		out.Suppress, out.Boost, out.Rules = rulesFromBody("", &comp.Rules)
		out.Compiled = &comp
		out.Path, out.Warnings, out.Blocked, out.DurationMin = comp.Path, comp.Warnings, comp.Blocked, comp.DurationMin
		out.Readback = strings.TrimSpace(comp.Readback)
		if len(out.Rules) == 0 && !out.Blocked && mode == "" && !strings.Contains(strings.Join(comp.Warnings, "\n"), "Not applied") {
			out.Suppress, out.Boost = interpretKnown(ruleText)
			out.Suppress, out.Boost = dedupe(out.Suppress), dedupe(out.Boost)
			out.Rules = policyRules(flatDirectiveID, out.Suppress, out.Boost)
			if len(out.Rules) > 0 {
				out.Readback = Readback(out.Suppress, out.Boost)
				comp.Unknown = nil // the compiler's unmatched words were just interpreted
			}
		}
	} else {
		out.Suppress, out.Boost, out.Rules = rulesFromBody(ruleText, nil)
		out.Readback = Readback(out.Suppress, out.Boost)
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if out.DurationMin == 0 && durationMin > 0 {
		out.DurationMin = durationMin
	}
	if out.DurationMin == 0 {
		out.DurationMin = command.ExtractDurationMin(text)
	}
	out.Readback = withDuration(out.Readback, out.DurationMin)
	if mode != "" {
		out.Readback = emconReadback(strings.TrimSuffix(out.Readback, "Feed ranked by the edge priority score only."),
			mode, out.DurationMin)
	}
	return out
}

// withDuration swaps the interpreter's open-ended "Until cleared." for the
// requested expiry so the readback says what the policy will actually do.
func withDuration(readback string, durationMin int) string {
	if durationMin <= 0 || !strings.Contains(readback, "Until cleared.") {
		return readback
	}
	return strings.Replace(readback, "Until cleared.", fmt.Sprintf("For %dm.", durationMin), 1)
}

type previewItem struct {
	ID      string  `json:"id"`
	Central float64 `json:"central"`
	Tier    int     `json:"tier"`
	Contrib []any   `json:"contrib"`
}

type candidateResult struct {
	Key     string `json:"key"`
	Counts  [4]int `json:"counts"`
	Changed int    `json:"changed"`
}

// predict applies draft rules on top of the standing policy to the current
// items: a newly suppressed object sinks to P4 (still listed), a newly boosted
// one rises a band. It is the live stand-in for the simulation's rescoring.
func (c *Console) predictLocked(suppress, boost []string, pins ...map[string]int32) ([]previewItem, [4]int, int) {
	var tiers map[string]int32
	if len(pins) > 0 {
		tiers = pins[0]
	}
	var counts [4]int
	changed := 0
	out := make([]previewItem, 0, len(c.order))
	for _, id := range c.order {
		item := c.withOverlay(c.base[id])
		live := item.Live
		tier, central := item.Tier, item.Central
		if item.Manual == 0 && !isScene(&item) {
			o := c.objects[id]
			if o == nil {
				o = &Object{Label: live.Label, Subtype: live.Subtype, Best: &Card{Label: live.Label, Override: live.Override}}
			}
			_, target := objectTierPin(o, tiers)
			if target > 0 {
				tier = int(target)
				central = [...]float64{0, .9, .7, .475, .175}[target]
			} else {
				switch {
				case !live.Unknown && matchLabel(suppress, live.Override, live.Subtype, live.Label) != "":
					tier, central = 4, round2(0.05+0.1*item.Edge)
				case live.Suppressed && matchLabel(suppress, live.Override, live.Subtype, live.Label) == "" && matchLabel(c.policy.Suppress, live.Override, live.Subtype, live.Label) != "":
					tier, central = item.BaseTier, item.Base // the draft lifts the standing suppression
				}
				if matchLabel(boost, live.Override, live.Subtype, live.Label) != "" && tier > 1 && (!live.Suppressed || matchLabel(suppress, live.Override, live.Subtype, live.Label) == "") {
					tier--
					central = round2(math.Min(0.98, central+0.2))
				}
			}
		}
		if tier != item.Tier {
			changed++
		}
		counts[tier-1]++
		out = append(out, previewItem{ID: id, Central: central, Tier: tier, Contrib: []any{}})
	}
	return out, counts, changed
}

func (c *Console) preview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text       *string         `json:"text"`
		Rules      *[]command.Rule `json:"rules"`
		Candidates []struct {
			Key   string         `json:"key"`
			Rules []command.Rule `json:"rules"`
		} `json:"candidates"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, httpx.RequestBodyStatus(err), "invalid request body")
		return
	}
	var checkRules []command.Rule
	if body.Rules != nil {
		checkRules = append(checkRules, (*body.Rules)...)
	}
	for _, candidate := range body.Candidates {
		checkRules = append(checkRules, candidate.Rules...)
	}
	for _, rule := range checkRules {
		if rule.Tier < 0 || rule.Tier > 4 {
			writeError(w, http.StatusBadRequest, "target tier must be between 1 and 4")
			return
		}
	}
	text := ""
	if body.Text != nil {
		text = *body.Text
	}
	var compiled *command.Compiled
	var cd compiledDirective
	suppress, boost, rules := []string{}, []string{}, []command.Rule{}
	if body.Rules != nil {
		suppress, boost, rules = rulesFromBody("", body.Rules)
	} else if c.b.Compiler != nil && strings.TrimSpace(text) != "" {
		// The model request must not hold the console's snapshot/overlay lock.
		cd = c.compileDirective(r.Context(), text, nil, 0, true)
		compiled = cd.Compiled
		if !cd.Blocked {
			suppress, boost, rules = cd.Suppress, cd.Boost, cd.Rules
		}
	} else {
		suppress, boost, rules = rulesFromBody(text, nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	items, counts, _ := c.predictLocked(suppress, boost, ruleTiers(rules))
	if cd.Blocked {
		counts = [4]int{}
		for i, id := range c.order {
			item := c.withOverlay(c.base[id])
			items[i].Central, items[i].Tier = item.Central, item.Tier
			counts[item.Tier-1]++
		}
	}
	cands := []candidateResult{}
	for _, cand := range body.Candidates {
		rs := cand.Rules
		s, b, combined := rulesFromBody("", &rs)
		_, cc, changed := c.predictLocked(s, b, ruleTiers(combined))
		cands = append(cands, candidateResult{Key: cand.Key, Counts: cc, Changed: changed})
	}
	unknown := unknownWords(text, suppress, boost)
	resolutions := []command.ResearchResolution{}
	if compiled != nil {
		if compiled.Unknown != nil {
			unknown = compiled.Unknown
		}
		resolutions = compiled.Resolutions
	}
	writeJSON(w, map[string]any{"rules": rules, "unknown": unknown, "items": items, "counts": counts, "candidates": cands,
		"path": cd.Path, "readback": cd.Readback, "warnings": cd.Warnings, "blocked": cd.Blocked, "resolutions": resolutions})
}

// unknownWords lists the directive's nouns the shore interpreter did not map.
func unknownWords(text string, suppress, boost []string) []string {
	out := []string{}
	if strings.TrimSpace(text) == "" || len(suppress)+len(boost) > 0 {
		return out
	}
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ",.;:!?\"'")
		switch w {
		case "", "ignore", "suppress", "prioritize", "prioritise", "boost", "focus", "on", "and", "the", "all", "any", "ships", "ship", "contacts":
			continue
		}
		out = append(out, w)
	}
	return out
}

func (c *Console) compile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text        string          `json:"text"`
		Rules       *[]command.Rule `json:"rules"`
		DurationMin int             `json:"durationMin"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, httpx.RequestBodyStatus(err), "invalid request body")
		return
	}
	if body.DurationMin < 0 || body.DurationMin > 10080 {
		writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
		return
	}
	cd := c.compileDirective(r.Context(), body.Text, body.Rules, body.DurationMin, false)
	warnings := cd.Warnings
	unknown := unknownWords(body.Text, cd.Suppress, cd.Boost)
	if cd.Compiled != nil && cd.Compiled.Unknown != nil {
		unknown = cd.Compiled.Unknown
	}
	resolutions := []command.ResearchResolution{}
	if cd.Compiled != nil {
		resolutions = cd.Compiled.Resolutions
	}
	writeJSON(w, map[string]any{
		"rules": cd.Rules, "unknown": unknown, "durationMin": cd.DurationMin, "path": cd.Path,
		"readback": cd.Readback, "warnings": warnings, "blocked": cd.Blocked, "resolutions": resolutions,
	})
}
