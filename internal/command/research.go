package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ResearchClient is an opt-in boundary to the separately packaged research
// interpreter. The demo continues to own classification, scoring and policy.
type ResearchClient struct {
	BaseURL string
	Timeout time.Duration
	HTTP    *http.Client
}

type ResearchResolution struct {
	Input     string `json:"input"`
	Canonical string `json:"canonical"`
	Reason    string `json:"reason,omitempty"`
}

type researchRule struct {
	Concept string  `json:"concept"`
	Action  string  `json:"action"`
	Weight  float64 `json:"weight"`
}

type researchDraft struct {
	Rules       *[]researchRule      `json:"rules"`
	Vocabulary  []string             `json:"vocabulary"`
	Path        string               `json:"path"`
	Readback    string               `json:"readback"`
	Operation   string               `json:"operation"`
	Resolutions []ResearchResolution `json:"resolutions"`
}

func NewResearchClient(baseURL string) (*ResearchClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("research compiler URL must be an HTTP(S) origin without credentials, query, or path")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if !((ip != nil && ip.IsLoopback()) || host == "localhost" || host == "host.docker.internal" || (ip == nil && !strings.Contains(host, "."))) {
		return nil, fmt.Errorf("research compiler URL must use loopback or a local container service name")
	}
	return &ResearchClient{BaseURL: strings.TrimRight(baseURL, "/"), Timeout: 6 * time.Second}, nil
}

func (c *ResearchClient) draft(ctx context.Context, text string) (researchDraft, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// An explicit empty list makes this request independent of the research
	// service's separate active queue, vocabulary and policy revisions.
	body, _ := json.Marshal(struct {
		Text  string         `json:"text"`
		Rules []researchRule `json:"rules"`
	}{Text: text, Rules: []researchRule{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/api/compile", bytes.NewReader(body))
	if err != nil {
		return researchDraft{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return researchDraft{}, fmt.Errorf("research language service unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var detail struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&detail)
		if detail.Error != "" {
			return researchDraft{}, fmt.Errorf("research language service: %s", shortReason(detail.Error, 200))
		}
		return researchDraft{}, fmt.Errorf("research language service returned HTTP %d", resp.StatusCode)
	}
	var draft researchDraft
	dec := json.NewDecoder(io.LimitReader(resp.Body, 128<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&draft); err != nil {
		return researchDraft{}, fmt.Errorf("invalid research compiler response: %w", err)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return researchDraft{}, fmt.Errorf("invalid trailing research compiler response")
	}
	return draft, nil
}

func (c *Compiler) compileResearch(ctx context.Context, text string) Compiled {
	blocked := func(reason string) Compiled {
		result := c.finish(text, []Rule{}, []string{}, 0, "research", []string{"Blocked: " + reason})
		result.Blocked = true
		result.Readback = "No directive will be applied."
		return result
	}
	if strings.TrimSpace(text) == "" || len(text) > 512 {
		return blocked("Enter a visual interest of 1–512 characters.")
	}
	draft, err := c.Research.draft(ctx, text)
	if err != nil {
		return blocked(err.Error())
	}
	if draft.Operation != "" || draft.Rules == nil || len(*draft.Rules) == 0 {
		return blocked("This request does not add a supported visual preference. Use the demo's remove or clear controls to change existing directives.")
	}
	if len(*draft.Rules) > 32 {
		return blocked("Research compiler returned too many rules.")
	}
	rules := make([]Rule, 0, len(*draft.Rules))
	seen := map[string]bool{}
	resolutions := append([]ResearchResolution{}, draft.Resolutions...)
	for _, rule := range *draft.Rules {
		if rule.Action != "boost" && rule.Action != "suppress" {
			return blocked("Research compiler returned an unsupported action.")
		}
		if math.IsNaN(rule.Weight) || math.IsInf(rule.Weight, 0) || rule.Weight < 1 || rule.Weight > 4 {
			return blocked("Research compiler returned an invalid emphasis weight.")
		}
		concept := strings.ToLower(strings.TrimSpace(rule.Concept))
		class, ok := canonicalClass(concept)
		if !ok {
			return blocked(fmt.Sprintf("The demo cannot match the visual concept %q yet. Choose an existing demo class; new or qualified concepts remain research-only.", shortReason(concept, 80)))
		}
		if seen[class] {
			return blocked("Research compiler returned conflicting or duplicate rules for " + class + ".")
		}
		seen[class] = true
		rules = append(rules, Rule{Type: rule.Action, Classes: []string{class}, Weight: rule.Weight})
		if concept != class {
			resolutions = append(resolutions, ResearchResolution{Input: concept, Canonical: class, Reason: "Matched the existing demo class."})
		}
	}
	path := "research"
	if draft.Path != "" {
		path += "/" + shortReason(draft.Path, 100)
	}
	result := c.finish(text, rules, []string{}, 0, path, []string{})
	result.Resolutions = resolutions
	return result
}
