package shorebridge

import (
	"encoding/json"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/buildprofile"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"net/http"
	"strings"
)

// ---- HTTP ----

func (c *Console) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", c.config)
	mux.HandleFunc("GET /api/items/{id}/metadata", c.itemMetadata)
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
		"features": map[string]any{
			"profile": buildprofile.Name, "simulation": false, "demoReset": false,
			"dataset": false, "demoLinkControl": c.b != nil && c.b.linkOverride,
		},
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

func (c *Console) itemMetadata(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	base := c.base[r.PathValue("id")]
	if base != nil {
		copy := c.withOverlay(base)
		base = &copy
	}
	c.mu.Unlock()
	if base == nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	record := command.BuildMetadata(*base)
	if emcon := c.b.cachedEmcon(); emcon != "" {
		record["capture"].(map[string]any)["sensor"].(map[string]any)["emcon"] = emcon
	}
	if base.Live != nil {
		tr := record["transfer"].(map[string]any)
		meta := base.Live.Forms["fact"]
		tr["metadata"] = map[string]any{"bytes": meta.Bytes, "received": meta.Received, "state": meta.State, "done": meta.State == "received"}
		image := base.Live.Forms[base.Live.ImageForm]
		if base.Live.ImageForm == "" {
			image = base.Live.Forms["full"]
		}
		tr["image"] = map[string]any{"bytes": image.Bytes, "received": image.Received, "state": image.State, "done": image.State == "received", "form": base.Live.ImageForm}
		if f := c.b.fetchFact(r.Context(), base.Live.ItemID); f != nil {
			if len(f.Tags) > 0 {
				record["capture"].(map[string]any)["tags"] = f.Tags
			}
			if synthetic := strings.TrimSpace(f.Tags["synthetic"]); synthetic != "" {
				var list []string
				if strings.HasPrefix(synthetic, "[") {
					_ = json.Unmarshal([]byte(synthetic), &list)
				}
				if len(list) == 0 {
					for _, v := range strings.Split(synthetic, ",") {
						if v = strings.TrimSpace(v); v != "" {
							list = append(list, v)
						}
					}
				}
				record["synthetic"] = list
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}
