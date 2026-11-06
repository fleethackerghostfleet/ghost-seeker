package shorebridge

import (
	"encoding/json"
	"net/http"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

// ---- HTTP ----

func (c *Console) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", c.config)
	mux.HandleFunc("GET /api/stream", c.stream)
	mux.HandleFunc("PATCH /api/items/{id}", c.patchItem)
	mux.HandleFunc("POST /api/items/{id}/review", c.reviewItem)
	mux.HandleFunc("POST /api/reviews/{id}/reason", c.correctReview)
	mux.HandleFunc("POST /api/reviews/{id}/undo", c.correctReview)
	mux.HandleFunc("GET /api/reviews/summary", c.reviewSummary)
	mux.HandleFunc("GET /api/reviews/export", c.reviewExport)
	mux.HandleFunc("POST /api/items/{id}/decision", c.decide)
	mux.HandleFunc("POST /api/items/{id}/full-res", c.fullRes)
	mux.HandleFunc("POST /api/items/{id}/pull", c.pull)
	mux.HandleFunc("GET /api/directives", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		d := c.directivesLocked()
		c.mu.Unlock()
		writeJSON(w, d)
	})
	mux.HandleFunc("POST /api/directives", c.addDirective)
	mux.HandleFunc("DELETE /api/directives", c.clearDirectives)
	mux.HandleFunc("DELETE /api/directives/{id}", c.clearDirectives)
	mux.HandleFunc("POST /api/directives/preview", c.preview)
	mux.HandleFunc("POST /api/directives/compile", c.compile)
	mux.HandleFunc("PUT /api/model", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusConflict, "The live feed is ranked by the ship’s scoring model. You cannot change that model here.")
	})
	mux.HandleFunc("GET /api/policy", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		p := c.policy
		c.mu.Unlock()
		writeJSON(w, map[string]any{"policy": p})
	})
	mux.HandleFunc("POST /api/policy/emcon", c.b.setEMCON)
	unsupported := func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusConflict, "Advanced policy editing must be done by a shore administrator, not from this console.")
	}
	mux.HandleFunc("POST /api/policy/validate", unsupported)
	mux.HandleFunc("PUT /api/policy", unsupported)
	mux.HandleFunc("GET /api/policy/audit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"entries": []any{}})
	})
	return mux
}

func (c *Console) config(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	writeJSON(w, map[string]any{
		"sources": c.sourceList(), "classes": c.allClasses(), "models": liveModels,
		"attrLabels": catalog.AttrLabels, "examples": liveExamples, "live": true,
	})
}

func (c *Console) allClasses() map[string]catalog.Class {
	out := make(map[string]catalog.Class, len(catalog.Classes)+len(c.classes))
	for k, v := range catalog.Classes {
		out[k] = v
	}
	for k, v := range c.classes {
		out[k] = v
	}
	return out
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
