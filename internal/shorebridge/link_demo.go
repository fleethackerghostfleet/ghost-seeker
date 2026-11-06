//go:build !prod

package shorebridge

import (
	"net/http"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
)

// WithLinkOverride lets a presenter pin the demo link rate (POST /api/shore/link).
func (b *Bridge) WithLinkOverride(on bool) *Bridge {
	b.linkOverride = on
	return b
}

func (b *Bridge) registerDemoRoutes(mux *http.ServeMux) {
	if b.linkOverride {
		mux.HandleFunc("POST /api/shore/link", b.setLink)
	}
}

// setLink pins the demo link rate ({"bps": N}) or restores the automatic
// distribution ({"auto": true}), so a presenter can make a backlog build.
func (b *Bridge) setLink(w http.ResponseWriter, r *http.Request) {
	s, ok := b.link.(LinkSetter)
	if !ok {
		http.Error(w, "link is not controlled by this process", http.StatusConflict)
		return
	}
	var in struct {
		Bps  *int64 `json:"bps"`
		Auto bool   `json:"auto"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		http.Error(w, "invalid request body", httpx.RequestBodyStatus(err))
		return
	}
	if !in.Auto && (in.Bps == nil || *in.Bps < 0) {
		http.Error(w, `body must be {"bps": N>=0} or {"auto": true}`, http.StatusBadRequest)
		return
	}
	if in.Auto {
		s.SetAuto()
	} else {
		s.SetFixed(*in.Bps)
	}
	writeJSON(w, b.linkStatus())
}
