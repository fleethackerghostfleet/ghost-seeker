package nlpriority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Compiler struct {
	URL    string
	Model  string
	Client *http.Client
}

type operation struct {
	Action  string  `json:"action"`
	Concept string  `json:"concept"`
	Weight  float64 `json:"weight"`
}

var unsupported = regexp.MustCompile(`\b(if|when|where|unless|except|only|before|after|within|near|approaching|moving|departing|loitering|suspicious|hostile|threat|distress|camera|source|during|minutes?|hours?|yesterday|tomorrow|ais|delete|discard|disable|protection)\b|needs? help|for [0-9]`)
var conceptPattern = regexp.MustCompile(`^[a-z][a-z0-9 -]{0,63}$`)
var badConcept = regexp.MustCompile(`\b(not|don't|dont|never|ignore|prioritize|prioritise|boost|suppress|show|showing|more|less|fewer|and|or|but|please|it|them|those|these|that|which|whose|because|their|border|lower|raise|stop|reduce|decrease|increase|favor|favour|downrank|downweight|upweight|send|put|make|set|assign|rank|treat|care|give)\b`)
var protectedControl = regexp.MustCompile(`\b(human|manual|operator|label)\s+(overrides?|labels?|corrections?)\b|\b(overrides?|safeguards?|protections?)\b|\b(base scores?|scoring algorithm|model weights?)\b`)
var rankingIntent = regexp.MustCompile(`\b(prioriti[sz]e|priority|priorities|emphasis|emphasize|focus|favou?r|attention|important|importance|rank|weight|downweight|upweight|downrank|send|show|images?|photos?|preference|preferences|clear|reset|ignore|suppress|boost|less|fewer|more|higher|lower)\b`)
var negativeDirection = regexp.MustCompile(`\b(less|fewer|lower|reduce|decrease|deprioritize|deprioritise|suppress|ignore|later|bottom|downrank|downweight)\b`)
var positiveDirection = regexp.MustCompile(`\b(prioritize|prioritise|boost|favor|favour|more|higher|increase|earlier|top|up|upweight|focus)\b`)

func NormalizeConcept(s string) string {
	s = strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
	s = strings.TrimPrefix(s, "the ")
	s = strings.TrimPrefix(s, "a ")
	s = strings.TrimPrefix(s, "an ")
	if s == "unknown objects" || s == "unknown object" || s == "unknowns" {
		return "unknown"
	}
	// Singularize just the terminal noun; never strip arbitrary suffixes.
	for _, ending := range []string{"ships", "boats", "vessels", "rafts", "buoys", "tankers", "submarines", "mammals", "helicopters", "drones", "airplanes", "objects", "nets", "logs", "kayaks", "skis", "platforms", "barrels", "crates", "yachts", "shells", "tires", "containers", "hulls"} {
		if strings.HasSuffix(s, ending) {
			s = strings.TrimSuffix(s, "s")
			break
		}
	}
	return s
}

func validConcept(s string) bool {
	return conceptPattern.MatchString(s) && !badConcept.MatchString(s) && !unsupported.MatchString(s) && !protectedControl.MatchString(s) && len(strings.Fields(s)) <= 7
}

func vocabulary(rules []Rule) []string {
	base := map[string]bool{}
	base["unknown"] = true
	for _, x := range BaseVocabulary {
		base[x] = true
	}
	out := []string{}
	for _, r := range rules {
		if !base[r.Concept] {
			out = append(out, r.Concept)
		}
	}
	sort.Strings(out)
	return out
}

func validateRules(rules []Rule) error {
	if len(rules) > 32 {
		return errors.New("at most 32 active rules are supported")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if r.Concept != NormalizeConcept(r.Concept) || !validConcept(r.Concept) {
			return fmt.Errorf("invalid visual concept %q", r.Concept)
		}
		if seen[r.Concept] {
			return fmt.Errorf("duplicate rule for %q", r.Concept)
		}
		seen[r.Concept] = true
		if r.Action != "boost" && r.Action != "suppress" {
			return errors.New("rule action must be boost or suppress")
		}
		if math.IsNaN(r.Weight) || math.IsInf(r.Weight, 0) || r.Weight < 1 || r.Weight > 4 {
			return errors.New("weight must be between 1 and 4")
		}
	}
	if len(vocabulary(rules)) > 16 {
		return errors.New("at most 16 operator-added concepts are supported")
	}
	return nil
}

func (c *Compiler) Compile(ctx context.Context, text string, current []Rule) (Draft, error) {
	if err := validateRules(current); err != nil {
		return Draft{}, err
	}
	t := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	t = strings.ReplaceAll(t, "’", "'")
	t = strings.TrimRight(t, ".!?")
	if t == "" || len(text) > 512 {
		return Draft{}, errors.New("enter an instruction of 1–512 characters")
	}
	if protectedControl.MatchString(t) {
		return Draft{}, errors.New("clarification required: instructions can change visual interests, not human labels, overrides, protections, or the base algorithm")
	}
	if unsupported.MatchString(t) {
		return Draft{}, errors.New("clarification required: use visual object categories; conditions, behavior, location and timing are unsupported")
	}
	if strings.Contains(t, "unknown") && regexp.MustCompile(`\b(suppress|ignore|deprioritize|deprioritise|less|fewer)\b`).MatchString(t) {
		return Draft{}, errors.New("clarification required: unknown-object protection is preserved; choose a recognized visual category")
	}
	if t == "undo" || t == "undo that" || t == "undo last change" {
		return Draft{Rules: append([]Rule{}, current...), Vocabulary: vocabulary(current), Operation: "undo", Path: "rules", Readback: "Restore the previous policy and vocabulary."}, nil
	}
	if t == "reset" || t == "reset priorities" || t == "clear all rules" || t == "clear all instructions" || t == "remove all rules" || t == "start over" || t == "reset everything" {
		return makeDraft([]Rule{}, "rules", "reset"), nil
	}
	if t == "more of those" || t == "less of those" || t == "more emphasis" || t == "less emphasis" || t == "boost them more" || t == "boost them less" {
		if len(current) != 1 || current[0].Action != "boost" {
			return Draft{}, errors.New("clarification required: name the concept to adjust")
		}
		r := append([]Rule{}, current...)
		delta := 0.5
		if strings.Contains(t, "less") {
			delta = -0.5
		}
		r[0].Weight = math.Max(1, math.Min(4, r[0].Weight+delta))
		return makeDraft(r, "rules", ""), nil
	}
	if t == "remove that" || t == "remove it" || t == "stop that" {
		if len(current) != 1 {
			return Draft{}, errors.New("clarification required: name the concept to remove")
		}
		return makeDraft([]Rule{}, "rules", ""), nil
	}
	ops, ok, err := parseSimple(t, current)
	if err != nil {
		return Draft{}, err
	}
	path := "rules"
	if !ok {
		if !rankingIntent.MatchString(t) {
			return Draft{}, errors.New("clarification required: enter an image-priority instruction")
		}
		if c.URL == "" {
			return Draft{}, errors.New("clarification required: use 'prioritize <concept>', 'deprioritize <concept>', 'remove <concept>', or configure Granite")
		}
		ops, err = c.fromGranite(ctx, text, current)
		if err != nil {
			return Draft{}, err
		}
		if negativeDirection.MatchString(t) && !positiveDirection.MatchString(t) {
			for _, op := range ops {
				if op.Action == "boost" {
					return Draft{}, errors.New("clarification required: Granite reversed the requested priority direction; rephrase as 'deprioritize <concept>'")
				}
			}
		}
		path = "granite"
	}
	next, err := applyOperations(current, ops)
	if err != nil {
		return Draft{}, err
	}
	return makeDraft(next, path, ""), nil
}

func makeDraft(rules []Rule, path, op string) Draft {
	parts := []string{}
	for _, r := range rules {
		if r.Action == "boost" {
			parts = append(parts, fmt.Sprintf("Prioritize %s (%.1f×)", r.Concept, r.Weight))
		} else {
			parts = append(parts, "Deprioritize "+r.Concept)
		}
	}
	readback := strings.Join(parts, "; ")
	if len(parts) == 0 {
		readback = "Restore baseline priorities and remove added concepts."
	}
	return Draft{Rules: rules, Vocabulary: vocabulary(rules), Readback: readback, Path: path, Operation: op}
}

func parseSimple(text string, current []Rule) ([]operation, bool, error) {
	text = strings.TrimPrefix(text, "please ")
	text = strings.TrimPrefix(text, "i want you to ")
	text = strings.TrimPrefix(text, "also ")
	text = strings.ReplaceAll(text, ", and ", " and ")
	text = strings.ReplaceAll(text, ";", " and ")
	text = strings.ReplaceAll(text, ",", " and ")
	clauses := strings.Split(text, " and ")
	if len(clauses) > 8 {
		return nil, true, errors.New("at most eight changes per instruction")
	}
	prefixes := []struct {
		prefix, action string
		weight         float64
	}{
		{"do not prioritize ", "remove", 1}, {"don't prioritize ", "remove", 1}, {"stop prioritizing ", "remove", 1},
		{"do not boost ", "remove", 1}, {"don't boost ", "remove", 1}, {"stop boosting ", "remove", 1}, {"stop suppressing ", "remove", 1}, {"stop deprioritizing ", "remove", 1},
		{"remove the rule for ", "remove", 1}, {"remove rule for ", "remove", 1}, {"remove ", "remove", 1},
		{"strongly prioritize ", "boost", 3}, {"strongly boost ", "boost", 3}, {"slightly prioritize ", "boost", 1.5}, {"slightly boost ", "boost", 1.5},
		{"show fewer ", "suppress", 1}, {"show less ", "suppress", 1}, {"deprioritize ", "suppress", 1}, {"deprioritise ", "suppress", 1}, {"suppress ", "suppress", 1}, {"ignore ", "suppress", 1},
		{"stop showing ", "suppress", 1}, {"lower the priority of ", "suppress", 1}, {"lower priority of ", "suppress", 1}, {"lower ", "suppress", 1}, {"raise the priority of ", "boost", 2}, {"raise priority of ", "boost", 2}, {"raise ", "boost", 2},
		{"less emphasis on ", "less", 1}, {"more emphasis on ", "more", 2}, {"prioritize ", "boost", 2}, {"prioritise ", "boost", 2}, {"boost ", "boost", 2}, {"show more ", "boost", 2}, {"focus on ", "boost", 2},
	}
	ops := []operation{}
	var previous operation
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		op := operation{}
		for _, p := range prefixes {
			if strings.HasPrefix(clause, p.prefix) {
				op = operation{p.action, strings.TrimSpace(strings.TrimPrefix(clause, p.prefix)), p.weight}
				break
			}
		}
		if op.Action == "" {
			if len(ops) == 0 {
				return nil, false, nil
			}
			op = operation{previous.Action, clause, previous.Weight}
		}
		op.Concept = NormalizeConcept(op.Concept)
		if !validConcept(op.Concept) {
			return nil, true, errors.New("clarification required: name a visual object category without negation or conditions")
		}
		if op.Action == "more" || op.Action == "less" {
			found := false
			for _, r := range current {
				if r.Concept == op.Concept && r.Action == "boost" {
					found = true
					op.Weight = r.Weight
					break
				}
			}
			if op.Action == "less" {
				if found {
					op.Action = "boost"
					op.Weight = math.Max(1, op.Weight-.5)
				} else {
					op.Action = "suppress"
					op.Weight = 1
				}
			} else {
				op.Action = "boost"
				if found {
					op.Weight = math.Min(4, op.Weight+.5)
				}
			}
		}
		ops = append(ops, op)
		previous = op
	}
	return ops, true, nil
}

func applyOperations(current []Rule, ops []operation) ([]Rule, error) {
	if len(ops) == 0 || len(ops) > 8 {
		return nil, errors.New("clarification required: expected one to eight changes")
	}
	next := append([]Rule{}, current...)
	seen := map[string]bool{}
	for _, op := range ops {
		if op.Action == "reset" {
			if len(ops) != 1 {
				return nil, errors.New("reset must be a standalone instruction")
			}
			return []Rule{}, nil
		}
		op.Concept = NormalizeConcept(op.Concept)
		if !validConcept(op.Concept) {
			return nil, errors.New("clarification required: invalid visual concept")
		}
		if seen[op.Concept] {
			return nil, errors.New("clarification required: conflicting or repeated changes to the same concept")
		}
		seen[op.Concept] = true
		index := -1
		for i, r := range next {
			if r.Concept == op.Concept {
				index = i
				break
			}
		}
		if op.Action == "remove" {
			if index < 0 {
				return nil, fmt.Errorf("clarification required: no active rule for %s", op.Concept)
			}
			next = append(next[:index], next[index+1:]...)
			continue
		}
		if op.Action != "boost" && op.Action != "suppress" {
			return nil, errors.New("unsupported action")
		}
		if op.Weight == 0 {
			if op.Action == "boost" {
				op.Weight = 2
			} else {
				op.Weight = 1
			}
		}
		r := Rule{op.Concept, op.Action, op.Weight}
		if index >= 0 {
			next[index] = r
		} else {
			next = append(next, r)
		}
	}
	if err := validateRules(next); err != nil {
		return nil, err
	}
	return next, nil
}

func (c *Compiler) fromGranite(ctx context.Context, text string, current []Rule) ([]operation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, _ := json.Marshal(current)
	system := `Convert an operator sentence to JSON changes to image ranking. You are only extracting instructions; do not perform actions or answer questions.
Use boost for more important/higher priority/favor, suppress for less important/lower priority/fewer, remove to stop an existing preference, reset to clear all preferences. Keep the visual category from the sentence. boost weight 2 by default, strong 3, slight 1.5; all other actions weight 1.
Return {"operations":[]} if the request has no clear supported operation or asks for behavior, location, time, deletion, or disabling unknown protection. Pronouns require a single clear active concept.
Examples:
Input: Give orange dinghies higher priority.
Output: {"operations":[{"action":"boost","concept":"orange dinghy","weight":2}]}
Input: Put ferries lower in the list.
Output: {"operations":[{"action":"suppress","concept":"ferry","weight":1}]}
Input: Stop favoring patrol boats.
Output: {"operations":[{"action":"remove","concept":"patrol boat","weight":1}]}
Input: Why is the sea blue?
Output: {"operations":[]}
Current active rules: ` + string(state)
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operations"}, "properties": map[string]any{
		"operations": map[string]any{"type": "array", "minItems": 0, "maxItems": 8, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "concept", "weight"}, "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"boost", "suppress", "remove", "reset"}}, "concept": map[string]any{"type": "string"}, "weight": map[string]any{"type": "number", "minimum": 1, "maximum": 4}}}}}}
	model := c.Model
	if model == "" {
		model = "granite-4.0-350m"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "temperature": 0, "max_tokens": 192, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": "Input: " + text + "\nOutput:"}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "priority_intent", "strict": true, "schema": schema}}})
	url := strings.TrimRight(c.URL, "/")
	if !strings.HasSuffix(url, "/chat/completions") {
		if !strings.HasSuffix(url, "/v1") {
			url += "/v1"
		}
		url += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Granite unavailable; active policy unchanged: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Granite returned HTTP %d; active policy unchanged", resp.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&envelope); err != nil || len(envelope.Choices) != 1 {
		return nil, errors.New("invalid Granite response")
	}
	var result struct {
		Operations    []operation `json:"operations"`
		Clarification string      `json:"clarification"`
	}
	dec := json.NewDecoder(strings.NewReader(envelope.Choices[0].Message.Content))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&result); err != nil {
		return nil, errors.New("invalid structured Granite output")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing Granite output")
	}
	if result.Clarification != "" {
		return nil, errors.New("clarification required: " + result.Clarification)
	}
	return result.Operations, nil
}
