package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Gemini on Vertex AI. The same catalog-constrained schema as the
// OpenAI-compatible path is sent as responseSchema; validateDraft still
// rejects anything outside the catalog.

const vertexDefaultModel = "gemini-2.5-flash"

// NewVertexClient returns an LLM client for Gemini on Vertex AI, or nil when
// no project is configured. tokenCommand prints an OAuth access token (for
// example "gcloud auth print-access-token"); when empty the GCE/GKE metadata
// server is used, falling back to gcloud.
func NewVertexClient(project, location, model string, timeout time.Duration, tokenCommand string) *LLMClient {
	if strings.TrimSpace(project) == "" {
		return nil
	}
	if location == "" {
		location = "global"
	}
	if model == "" {
		model = vertexDefaultModel
	}
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	host := "aiplatform.googleapis.com"
	if location != "global" {
		host = location + "-aiplatform.googleapis.com"
	}
	return &LLMClient{
		Provider: "vertex", Model: model, Timeout: timeout,
		BaseURL: fmt.Sprintf("https://%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent", host, project, location, model),
		Token:   (&tokenSource{command: tokenCommand}).token,
	}
}

func (c *LLMClient) vertexRequest(ctx context.Context, text string) (llmDraft, error) {
	contents := make([]map[string]any, 0, len(promptExamples)*2+1)
	for _, message := range llmMessages(text)[1:] {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{"role": role, "parts": []map[string]any{{"text": message.Content}}})
	}
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]any{{"text": llmSystemPrompt() + vertexMappingRule}}},
		"contents":          contents,
		"generationConfig": map[string]any{
			"temperature": 0, "maxOutputTokens": 800,
			"responseMimeType": "application/json",
			"responseSchema":   vertexSchema(),
			"thinkingConfig":   map[string]any{"thinkingBudget": 0},
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return llmDraft{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(encoded))
	if err != nil {
		return llmDraft{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return llmDraft{}, fmt.Errorf("vertex credentials: %w", err)
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
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llmDraft{}, fmt.Errorf("vertex HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return llmDraft{}, err
	}
	if len(envelope.Candidates) == 0 || len(envelope.Candidates[0].Content.Parts) == 0 {
		return llmDraft{}, fmt.Errorf("missing candidates")
	}
	var draft llmDraft
	if err := json.Unmarshal([]byte(stripJSONFence(envelope.Candidates[0].Content.Parts[0].Text)), &draft); err != nil {
		return llmDraft{}, err
	}
	return draft, nil
}

const vertexMappingRule = "\nWhen a phrase has no exact class (missile boat, fast attack craft, Type 022, mine, ferry), choose the nearest classes and list each such phrase in mappings with the classes you chose. Leave mappings empty for exact matches."

// vertexSchema converts llmSchema to the OpenAPI subset Vertex accepts and
// adds the optional nearest-class mappings used for readback.
func vertexSchema() map[string]any {
	schema := vertexSubset(llmSchema()).(map[string]any)
	properties := schema["properties"].(map[string]any)
	properties["mappings"] = map[string]any{
		"type": "array", "maxItems": 8,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"phrase":  map[string]any{"type": "string"},
				"classes": properties["rules"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["classes"],
			},
			"required": []string{"phrase", "classes"},
		},
	}
	return schema
}

func vertexSubset(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if anyOf, ok := typed["anyOf"].([]any); ok {
			for _, option := range anyOf {
				if branch, ok := option.(map[string]any); ok && branch["type"] != "null" {
					out := vertexSubset(branch).(map[string]any)
					out["nullable"] = true
					return out
				}
			}
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "additionalProperties" || key == "minimum" || key == "maximum" {
				continue
			}
			out[key] = vertexSubset(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = vertexSubset(item)
		}
		return out
	}
	return value
}

// tokenSource caches an OAuth access token from the metadata server or a
// token command; the command output is never logged.
type tokenSource struct {
	command string
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (s *tokenSource) token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value != "" && time.Now().Before(s.expires) {
		return s.value, nil
	}
	value, ttl, err := "", 45*time.Minute, error(nil)
	if s.command == "" {
		value, ttl, err = metadataToken(ctx)
	}
	if s.command != "" || err != nil {
		command := s.command
		if command == "" {
			command = "gcloud auth print-access-token"
		}
		value, ttl, err = commandToken(ctx, command)
	}
	if err != nil {
		return "", err
	}
	s.value, s.expires = value, time.Now().Add(ttl)
	return value, nil
}

func metadataToken(ctx context.Context) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("metadata HTTP %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil || body.AccessToken == "" {
		return "", 0, fmt.Errorf("metadata token unavailable")
	}
	return body.AccessToken, max(time.Duration(body.ExpiresIn-60)*time.Second, time.Minute), nil
}

func commandToken(ctx context.Context, command string) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	fields := strings.Fields(command)
	output, err := exec.CommandContext(ctx, fields[0], fields[1:]...).Output()
	if err != nil {
		return "", 0, fmt.Errorf("token command failed")
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", 0, fmt.Errorf("token command printed nothing")
	}
	return token, 45 * time.Minute, nil
}
