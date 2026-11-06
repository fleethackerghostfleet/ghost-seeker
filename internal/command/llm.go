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
	// Provider is "" (OpenAI-compatible chat completions) or "vertex".
	Provider string
	BaseURL  string
	Model    string
	Timeout  time.Duration
	HTTP     *http.Client
	Cooldown time.Duration
	// Token returns a bearer token for each request when set.
	Token     func(context.Context) (string, error)
	CacheTTL  time.Duration
	mu        sync.Mutex
	openUntil time.Time
	cache     map[string]cachedDraft
	inflight  map[string]*draftCall
}

type llmDraft struct {
	Rules       []Rule       `json:"rules"`
	DurationMin *int         `json:"durationMin"`
	Mappings    []llmMapping `json:"mappings,omitempty"`
}

type llmMapping struct {
	Phrase  string   `json:"phrase"`
	Classes []string `json:"classes"`
}

type cachedDraft struct {
	draft   llmDraft
	expires time.Time
}

type draftCall struct {
	done  chan struct{}
	draft llmDraft
	err   error
}

const llmCacheSize = 512

func cacheKey(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// Cached returns a previously compiled draft without calling the model.
func (c *LLMClient) Cached(text string) (llmDraft, bool) {
	if c == nil {
		return llmDraft{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[cacheKey(text)]
	if !ok || time.Now().After(entry.expires) {
		return llmDraft{}, false
	}
	return entry.draft, true
}

// Prefetch compiles text in the background so a later Draft or Cached call
// for the same text returns immediately. Previews use it to never wait.
func (c *LLMClient) Prefetch(text string) {
	if c == nil || strings.TrimSpace(text) == "" || c.isOpen() {
		return
	}
	key := cacheKey(text)
	c.mu.Lock()
	_, cached := c.cache[key]
	_, running := c.inflight[key]
	c.mu.Unlock()
	if cached || running {
		return
	}
	go func() { _, _ = c.Draft(context.Background(), text) }()
}

func (c *LLMClient) store(key string, draft llmDraft) {
	ttl := c.CacheTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	if c.cache == nil {
		c.cache = make(map[string]cachedDraft)
	}
	if len(c.cache) >= llmCacheSize {
		now := time.Now()
		for existing, entry := range c.cache {
			if now.After(entry.expires) || len(c.cache) >= llmCacheSize {
				delete(c.cache, existing)
			}
		}
	}
	c.cache[key] = cachedDraft{draft: draft, expires: time.Now().Add(ttl)}
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
	if draft, ok := c.Cached(text); ok {
		return draft, nil
	}
	if c.isOpen() {
		return llmDraft{}, fmt.Errorf("circuit open")
	}
	key := cacheKey(text)
	c.mu.Lock()
	call, running := c.inflight[key]
	if !running {
		call = &draftCall{done: make(chan struct{})}
		if c.inflight == nil {
			c.inflight = make(map[string]*draftCall)
		}
		c.inflight[key] = call
	}
	c.mu.Unlock()
	if !running {
		go c.run(key, text, call)
	}
	select {
	case <-call.done:
		return call.draft, call.err
	case <-ctx.Done():
		return llmDraft{}, ctx.Err()
	}
}

// run performs one model request detached from the caller, so a cancelled
// HTTP request still fills the cache for the next preview or apply.
func (c *LLMClient) run(key, text string, call *draftCall) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var draft llmDraft
	var err error
	if c.Provider == "vertex" {
		draft, err = c.vertexRequest(ctx, text)
	} else {
		draft, err = c.request(ctx, text)
	}
	if err != nil {
		c.recordFailure()
	}
	valid := err == nil
	if valid {
		_, validateErr := validateDraft(draft)
		valid = validateErr == nil
	}
	c.mu.Lock()
	delete(c.inflight, key)
	if valid {
		c.store(key, draft)
	}
	c.mu.Unlock()
	call.draft, call.err = draft, err
	close(call.done)
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
		if c.Token != nil {
			token, err := c.Token(ctx)
			if err != nil {
				return llmDraft{}, err
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
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

func (c *LLMClient) label() string {
	if c != nil && c.Provider == "vertex" {
		return "Gemini (Vertex AI)"
	}
	return "Local LLM"
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
