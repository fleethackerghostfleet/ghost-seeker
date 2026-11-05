package server

import (
	"fmt"
	"math"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

type contribution struct {
	RuleIndex int     `json:"ruleIndex"`
	Delta     float64 `json:"delta"`
}

type previewItem struct {
	ID      string         `json:"id"`
	Central float64        `json:"central"`
	Tier    int            `json:"tier"`
	Contrib []contribution `json:"contrib"`
}

type candidateResult struct {
	Key     string `json:"key"`
	Counts  [4]int `json:"counts"`
	Changed int    `json:"changed"`
}

func validateRules(rules []command.Rule) error {
	if len(rules) > 64 {
		return fmt.Errorf("at most 64 rules are supported")
	}
	for _, rule := range rules {
		if rule.Type != "boost" && rule.Type != "suppress" {
			return fmt.Errorf("unknown rule type")
		}
		if math.IsNaN(rule.Weight) || math.IsInf(rule.Weight, 0) || rule.Weight < 0 || rule.Weight > 10 {
			return fmt.Errorf("weight must be between 0 and 10")
		}
		if rule.WithinMin < 0 || rule.WithinMin > 525600 {
			return fmt.Errorf("time window must be between 0 and 525600 minutes")
		}
		for _, class := range rule.Classes {
			if _, ok := catalog.Classes[class]; !ok {
				return fmt.Errorf("unknown class %q", class)
			}
		}
		for _, attr := range rule.Attrs {
			if _, ok := catalog.AttrLabels[attr]; !ok {
				return fmt.Errorf("unknown condition %q", attr)
			}
		}
		for _, id := range rule.Sources {
			found := false
			for _, source := range catalog.Sources {
				if source.ID == id {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown source %q", id)
			}
		}
	}
	return nil
}

func (s *Server) previewDirectives(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text       *string         `json:"text"`
		Rules      *[]command.Rule `json:"rules"`
		Candidates []struct {
			Key   string         `json:"key"`
			Rules []command.Rule `json:"rules"`
		} `json:"candidates"`
	}
	if err := decodeJSON(http.MaxBytesReader(w, r.Body, 512<<10), &body); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	rules, unknown := []command.Rule{}, []string{}
	if body.Text != nil {
		if utf8.RuneCountInString(*body.Text) > 500 {
			writeError(w, 400, "Directive text must be 500 characters or fewer")
			return
		}
		compiled := s.directiveCompiler().CompileRules(*body.Text)
		rules, unknown = compiled.Rules, compiled.Unknown
	}
	if body.Rules != nil {
		rules = *body.Rules
	}
	if err := validateRules(rules); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(body.Candidates) > 32 {
		writeError(w, 400, "at most 32 candidates are supported")
		return
	}
	for _, c := range body.Candidates {
		if err := validateRules(c.Rules); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	items, _, _, state := s.store.Snapshot()
	visible := items[:0]
	for _, item := range items {
		if !item.Dismissed {
			visible = append(visible, item)
		}
	}
	items = visible
	now := time.Now()
	score := func(item command.Item, draft []command.Rule) command.Item {
		directives := append([]command.Directive(nil), state.Directives...)
		directives = append(directives, command.Directive{Rules: draft})
		command.RescoreAt(&item, state.Model, directives, now)
		return item
	}
	result := struct {
		Rules      []command.Rule    `json:"rules"`
		Unknown    []string          `json:"unknown"`
		Items      []previewItem     `json:"items"`
		Counts     [4]int            `json:"counts"`
		Candidates []candidateResult `json:"candidates"`
	}{Rules: rules, Unknown: unknown, Items: make([]previewItem, 0, len(items)), Candidates: make([]candidateResult, 0, len(body.Candidates))}
	for _, item := range items {
		after := score(item, rules)
		entry := previewItem{ID: item.ID, Central: after.Central, Tier: after.Tier, Contrib: make([]contribution, 0, len(rules))}
		for index := range rules {
			without := append([]command.Rule(nil), rules[:index]...)
			without = append(without, rules[index+1:]...)
			other := score(item, without)
			entry.Contrib = append(entry.Contrib, contribution{RuleIndex: index, Delta: after.Central - other.Central})
		}
		result.Items = append(result.Items, entry)
		result.Counts[after.Tier-1]++
	}
	for _, candidate := range body.Candidates {
		entry := candidateResult{Key: candidate.Key}
		for index, item := range items {
			after := score(item, candidate.Rules)
			entry.Counts[after.Tier-1]++
			if after.Tier != result.Items[index].Tier {
				entry.Changed++
			}
		}
		result.Candidates = append(result.Candidates, entry)
	}
	writeJSON(w, 200, result)
}
