package shorebridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

type edgeCapabilities struct {
	b     *Bridge
	mu    sync.Mutex
	until time.Time
	caps  command.Capabilities
}

func (b *Bridge) InferenceCapabilities() command.CapabilityProvider {
	return &edgeCapabilities{b: b}
}

func (p *edgeCapabilities) Capabilities() command.Capabilities {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.until) {
		return p.caps
	}
	p.caps = command.Capabilities{}
	p.until = time.Now().Add(15 * time.Second)
	if p.b.edge == "" {
		return p.caps
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.b.edge+"/ship/inference", nil)
	if err != nil {
		return p.caps
	}
	p.b.authorize(req)
	resp, err := p.b.client.Do(req)
	if err != nil {
		return p.caps
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var caps command.Capabilities
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&caps) == nil {
			p.caps = caps
		}
	}
	return p.caps
}
