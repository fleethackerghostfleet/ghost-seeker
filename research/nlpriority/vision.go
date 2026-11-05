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
	"strings"
	"time"
)

type Vision struct {
	URL    string
	Client *http.Client
}

func (v *Vision) Classify(ctx context.Context, rules []Rule, items []Item) (map[string]Match, []string, error) {
	out := map[string]Match{}
	warnings := []string{}
	if len(rules) == 0 || len(items) == 0 {
		return out, warnings, nil
	}
	concepts := []string{}
	for _, r := range rules {
		if r.Concept != "unknown" {
			concepts = append(concepts, r.Concept)
		}
	}
	if len(concepts) == 0 {
		return out, warnings, nil
	}
	input := []map[string]any{}
	modelID := ""
	for _, it := range items {
		if len(it.Embedding) == 0 {
			continue
		}
		if it.ModelID == "" {
			return nil, nil, errors.New("image embedding lacks modelId")
		}
		if modelID == "" {
			modelID = it.ModelID
		}
		if it.ModelID != modelID {
			return nil, nil, errors.New("incompatible image encoder identities in queue")
		}
		input = append(input, map[string]any{"id": it.ID, "embedding": it.Embedding, "modelId": it.ModelID})
	}
	if len(input) == 0 {
		warnings = append(warnings, "No image embeddings: only exact existing labels can match; experimental classification is unavailable.")
		return out, warnings, nil
	}
	if v == nil || v.URL == "" {
		return nil, nil, errors.New("vision service is required to classify image embeddings")
	}
	if len(input) < len(items) {
		warnings = append(warnings, "Some images lack embeddings and can only match their existing labels.")
	}
	body, _ := json.Marshal(map[string]any{"concepts": concepts, "items": input})
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(v.URL, "/")+"/classify", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("vision unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, nil, fmt.Errorf("vision HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var result struct {
		ModelID   string  `json:"modelId"`
		Dimension int     `json:"dimension"`
		Matches   []Match `json:"matches"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return nil, nil, err
	}
	if result.ModelID != modelID {
		return nil, nil, errors.New("text/image encoder identity mismatch")
	}
	known := map[string]Item{}
	for _, it := range items {
		known[it.ID] = it
	}
	allowed := map[string]bool{}
	for _, s := range concepts {
		allowed[s] = true
	}
	for _, s := range BaseVocabulary {
		allowed[s] = true
	}
	for _, m := range result.Matches {
		it, ok := known[m.ID]
		if !ok || len(it.Embedding) == 0 {
			return nil, nil, errors.New("vision returned unknown item")
		}
		if _, exists := out[m.ID]; exists {
			return nil, nil, errors.New("vision returned duplicate item")
		}
		if result.Dimension != len(it.Embedding) {
			return nil, nil, errors.New("text/image embedding dimension mismatch")
		}
		if math.IsNaN(m.Similarity) || math.IsInf(m.Similarity, 0) || m.Similarity < -1.001 || m.Similarity > 1.001 || math.IsNaN(m.Margin) || math.IsInf(m.Margin, 0) {
			return nil, nil, errors.New("invalid vision score")
		}
		if m.Accepted && !allowed[m.Label] {
			return nil, nil, errors.New("vision returned label outside candidate vocabulary")
		}
		out[m.ID] = m
	}
	if len(out) != len(input) {
		return nil, nil, errors.New("vision omitted image classifications")
	}
	return out, warnings, nil
}
