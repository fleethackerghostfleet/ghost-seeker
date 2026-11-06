//go:build !prod

package server

import (
	"net/http"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
)

func WithDemoReset() Option {
	return func(options *serverOptions) { options.demoReset = true }
}

func (s *Server) registerDemoRoutes() {
	if s.demoReset {
		s.mux.HandleFunc("POST /api/demo/reset", s.resetDemo)
	}
}

// handleDemoRoute answers /api/demo/reset requests the mux would not: 404
// when reset is off and 405 for non-POST methods.
func (s *Server) handleDemoRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/demo/reset" {
		return false
	}
	if !s.demoReset {
		http.NotFound(w, r)
		return true
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	return false
}

func (s *Server) resetDemo(w http.ResponseWriter, r *http.Request) {
	if principal, ok := opauth.PrincipalFrom(r.Context()); ok && principal.Role != "operator" {
		writeError(w, http.StatusForbidden, "operator role required")
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &body); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if body.Confirm != "reset" {
		writeError(w, http.StatusBadRequest, "confirm must be reset")
		return
	}
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	epoch := s.store.Reset()
	snapshot := s.snapshotEvent()
	snapshot["reset"] = true
	s.hub.publish("snapshot", snapshot)
	audit(r, "demo.reset", "epoch", epoch)
	writeJSON(w, http.StatusOK, map[string]any{"epoch": epoch})
}
