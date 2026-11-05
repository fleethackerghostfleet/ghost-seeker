package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type LLMClient struct {
	BaseURL   string
	Model     string
	Timeout   time.Duration
	HTTP      *http.Client
	Cooldown  time.Duration
	mu        sync.Mutex
	openUntil time.Time
}

type llmDraft struct {
	Rules       []Rule `json:"rules"`
	DurationMin *int   `json:"durationMin"`
}

func NewLLMClient(baseURL, model string, timeout time.Duration) *LLMClient {
	if baseURL == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &LLMClient{BaseURL: baseURL, Model: model, Timeout: timeout}
}

func (c *LLMClient) Draft(ctx context.Context, text string) (llmDraft, error) {
	if c == nil {
		return llmDraft{}, fmt.Errorf("LLM client unavailable")
	}
	if c.isOpen() {
		return llmDraft{}, fmt.Errorf("circuit open")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	draft, err := c.request(ctx, text)
	if err != nil {
		c.recordFailure()
		return llmDraft{}, err
	}
	return draft, nil
}

func (c *LLMClient) request(ctx context.Context, text string) (llmDraft, error) {
	url := strings.TrimSuffix(strings.TrimRight(c.BaseURL, "/"), "/v1") + "/v1/chat/completions"
	responseFormat := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name": "ghost_seeker_directive", "schema": llmSchema(), "strict": true,
		},
	}
	for attempt := 0; attempt < 2; attempt++ {
		body := map[string]any{
			"messages": llmMessages(text), "temperature": 0, "max_tokens": 600,
			"response_format": responseFormat,
		}
		if c.Model != "" {
			body["model"] = c.Model
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return llmDraft{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
		if err != nil {
			return llmDraft{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		client := c.HTTP
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return llmDraft{}, err
		}
		if attempt == 0 && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			responseFormat = map[string]any{"type": "json_object"}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return llmDraft{}, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		var envelope struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
		resp.Body.Close()
		if err != nil {
			return llmDraft{}, err
		}
		if len(envelope.Choices) == 0 {
			return llmDraft{}, fmt.Errorf("missing choices")
		}
		content := stripJSONFence(envelope.Choices[0].Message.Content)
		if !strings.HasPrefix(strings.TrimSpace(content), "{") {
			return llmDraft{}, fmt.Errorf("response content is not a JSON object")
		}
		var draft llmDraft
		if err := json.Unmarshal([]byte(content), &draft); err != nil {
			return llmDraft{}, err
		}
		return draft, nil
	}
	return llmDraft{}, fmt.Errorf("request failed")
}

func stripJSONFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```")
	if strings.HasPrefix(strings.ToLower(content), "json") {
		content = content[4:]
	}
	content = strings.TrimSpace(content)
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}

func (c *LLMClient) isOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Before(c.openUntil)
}

func (c *LLMClient) recordFailure() {
	cooldown := c.Cooldown
	if cooldown == 0 {
		cooldown = 30 * time.Second
	}
	if cooldown < 0 {
		return
	}
	c.mu.Lock()
	c.openUntil = time.Now().Add(cooldown)
	c.mu.Unlock()
}
