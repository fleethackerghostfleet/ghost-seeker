package nlpriority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Core labels are detector categories, not a synonym dictionary.
var coreDetectorLabels = []string{"boat", "ship", "person", "bird", "airplane", "helicopter", "drone", "debris", "unknown"}

type ConceptResolution struct {
	Input     string `json:"input"`
	Canonical string `json:"canonical"`
	Reason    string `json:"reason,omitempty"`
}

func fixedConcepts() map[string]bool {
	out := map[string]bool{}
	for _, x := range BaseVocabulary {
		out[x] = true
	}
	for _, x := range coreDetectorLabels {
		out[x] = true
	}
	return out
}

// Category equivalence is decided by Granite at command time. It may select
// only a supplied category or NONE. Current rules change only in the draft.
func (c *Compiler) resolveConcepts(ctx context.Context, current []Rule, ops []operation) ([]Rule, []operation, []ConceptResolution, error) {
	fixed := fixedConcepts()
	known := append(append([]string{}, BaseVocabulary...), coreDetectorLabels...)
	seenKnown := fixedConcepts()
	unknown := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		s = NormalizeConcept(s)
		if s != "" && !fixed[s] && !seen[s] {
			unknown = append(unknown, s)
			seen[s] = true
		}
	}
	for _, r := range current {
		add(r.Concept)
		if !seenKnown[r.Concept] {
			known = append(known, r.Concept)
			seenKnown[r.Concept] = true
		}
	}
	for _, op := range ops {
		if op.Action != "reset" {
			add(op.Concept)
		}
	}
	if len(unknown) == 0 {
		return current, ops, nil, nil
	}
	if c.URL == "" {
		return nil, nil, nil, errors.New("clarification required: Granite must be available to interpret unfamiliar category names")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resolved := map[string]string{}
	changes := []ConceptResolution{}
	// Short individual enum responses are more reliable for this 350M model than
	// a long batched mapping. All calls share one command deadline; none run per image.
	for _, input := range unknown {
		canonical, err := c.resolveOne(ctx, input, known)
		if err != nil {
			return nil, nil, nil, err
		}
		reason := ""
		if canonical == "NONE" {
			canonical = input
			reason = "Preserved as a new visual concept."
		} else if !seenKnown[canonical] {
			return nil, nil, nil, errors.New("Granite selected a category outside the known vocabulary")
		}
		resolved[input] = canonical
		changes = append(changes, ConceptResolution{Input: input, Canonical: canonical, Reason: reason})
	}
	canonical := func(s string) string {
		s = NormalizeConcept(s)
		if x, ok := resolved[s]; ok {
			return x
		}
		return s
	}
	nextCurrent := []Rule{}
	positions := map[string]int{}
	for _, r := range current {
		r.Concept = canonical(r.Concept)
		if i, ok := positions[r.Concept]; ok {
			old := nextCurrent[i]
			if old.Action != "suppress" && (r.Action == "suppress" || r.Weight > old.Weight) {
				nextCurrent[i] = r
			}
		} else {
			positions[r.Concept] = len(nextCurrent)
			nextCurrent = append(nextCurrent, r)
		}
	}
	nextOps := append([]operation{}, ops...)
	for i := range nextOps {
		nextOps[i].Concept = canonical(nextOps[i].Concept)
	}
	return nextCurrent, nextOps, changes, nil
}

func (c *Compiler) resolveOne(ctx context.Context, input string, known []string) (string, error) {
	// NONE already preserves the input. Omitting its identical custom label
	// lets Granite resolve older literal policies against the fixed vocabulary.
	options := []string{}
	for _, label := range known {
		if label != input {
			options = append(options, label)
		}
	}
	known = options
	knownJSON, _ := json.Marshal(known)
	system := `Read the visual object phrase. First list any extra visual qualifiers (size, color, material) that are not part of a known category. Then choose the equivalent known category, or NONE. Synonyms for a known category do not create extra qualifiers. The words boat, ship, vessel and craft can refer to the same functional category. Equipment is different from the vehicle using it.
Known categories: ` + string(knownJSON) + `
Input: cargo vessel
Output: {"qualifiers":[],"label":"cargo ship"}
Input: large cargo vessel
Output: {"qualifiers":["large"],"label":"cargo ship"}
Input: red cargo ship
Output: {"qualifiers":["red"],"label":"cargo ship"}
Input: rescue boat
Output: {"qualifiers":[],"label":"NONE"}
Input: cargo net
Output: {"qualifiers":[],"label":"NONE"}`

	// Keep property order: this small model extracts qualifiers before selecting
	// a category. A map would serialize label first.
	properties := struct {
		Qualifiers any `json:"qualifiers"`
		Label      any `json:"label"`
	}{
		Qualifiers: map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 4},
		Label:      map[string]any{"type": "string", "enum": append(append([]string{}, known...), "NONE")},
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": []string{"qualifiers", "label"}, "additionalProperties": false}
	model := c.Model
	if model == "" {
		model = "granite-4.0-350m"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "temperature": 0, "max_tokens": 60, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": "Input: " + input + "\nOutput:"}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "concept_resolution", "strict": true, "schema": schema}}})
	url := strings.TrimRight(c.URL, "/")
	if !strings.HasSuffix(url, "/chat/completions") {
		if !strings.HasSuffix(url, "/v1") {
			url += "/v1"
		}
		url += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("clarification required: Granite category interpretation unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("clarification required: Granite category interpretation returned HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&envelope); err != nil || len(envelope.Choices) != 1 {
		return "", errors.New("invalid Granite category interpretation response")
	}
	var result struct {
		Qualifiers *[]string `json:"qualifiers"`
		Label      string    `json:"label"`
	}
	dec := json.NewDecoder(strings.NewReader(envelope.Choices[0].Message.Content))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&result); err != nil {
		return "", errors.New("invalid structured category interpretation")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return "", errors.New("trailing category interpretation output")
	}
	// Check the raw label first; preservation must not hide an invalid answer.
	allowed := result.Label == "NONE"
	for _, label := range known {
		if result.Label == label {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("Granite selected a category outside the known vocabulary")
	}
	if result.Qualifiers == nil || len(*result.Qualifiers) > 4 {
		return "", errors.New("category interpretation requires a qualifiers array with at most four entries")
	}
	preserve := result.Label == "NONE"
	for _, qualifier := range *result.Qualifiers {
		if strings.TrimSpace(qualifier) == "" || !strings.Contains(input, qualifier) {
			return "", errors.New("Granite reported a qualifier absent from the original phrase")
		}
		// Attributes come from the model, not a static word/alias list. Require
		// phrase boundaries: red is not considered present in redwing.
		phrase := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(qualifier) + `($|[^a-z0-9])`)
		if !phrase.MatchString(result.Label) {
			preserve = true
		}
	}
	if preserve {
		return "NONE", nil
	}
	return result.Label, nil
}
