package nlpriority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Classifying the request separately prevents the small operation extractor
// from inventing an image preference in response to unrelated conversation.
// This is a model decision, rather than a command-word admission whitelist.
func (c *Compiler) checkVisualIntent(ctx context.Context, text string) error {
	system := `Classify the user's sentence for an image discovery application. Return visual_interest when the user expresses an interest in seeing, finding, watching for, emphasizing, avoiding or changing preferences about visible objects in images. Questions can request a visual search: "can you find" and "how about" are visual interests when they name visible objects. Negative preferences and requests to stop or remove an existing preference are also visual_interest. Return other for greetings, personal feelings, general questions, requests for explanations, and unrelated conversation. Classify the request; do not answer it and do not invent an image search.
Input: Look for blue boats.
Output: {"intent":"visual_interest"}
Input: I am interested in aircraft.
Output: {"intent":"visual_interest"}
Input: Could you find orange dinghies?
Output: {"intent":"visual_interest"}
Input: Help me spot buoys.
Output: {"intent":"visual_interest"}
Input: Avoid red boats.
Output: {"intent":"visual_interest"}
Input: Stop watching for sailboats.
Output: {"intent":"visual_interest"}
Input: How about aircraft?
Output: {"intent":"visual_interest"}
Input: Hello.
Output: {"intent":"other"}
Input: What is the weather?
Output: {"intent":"other"}
Input: I am hungry.
Output: {"intent":"other"}`
	schema := map[string]any{"type": "object", "properties": map[string]any{"intent": map[string]any{"type": "string", "enum": []string{"visual_interest", "other"}}}, "required": []string{"intent"}, "additionalProperties": false}
	model := c.Model
	if model == "" {
		model = "granite-4.0-350m"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "temperature": 0, "max_tokens": 24, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": "Input: " + text + "\nOutput:"}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "intent_kind", "strict": true, "schema": schema}}})
	url := strings.TrimRight(c.URL, "/")
	if !strings.HasSuffix(url, "/chat/completions") {
		if !strings.HasSuffix(url, "/v1") {
			url += "/v1"
		}
		url += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("clarification required: Granite request interpretation unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clarification required: Granite request interpretation returned HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&envelope); err != nil || len(envelope.Choices) != 1 {
		return errors.New("invalid Granite request interpretation response")
	}
	var result struct {
		Intent string `json:"intent"`
	}
	dec := json.NewDecoder(strings.NewReader(envelope.Choices[0].Message.Content))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&result); err != nil {
		return errors.New("invalid structured request interpretation")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("trailing request interpretation output")
	}
	switch result.Intent {
	case "visual_interest":
		return nil
	case "other":
		return errors.New("clarification required: describe the visual objects you want to see more or less of")
	default:
		return errors.New("invalid Granite request interpretation category")
	}
}
