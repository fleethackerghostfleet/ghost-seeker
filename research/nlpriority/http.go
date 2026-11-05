package nlpriority

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Service) Handler(webDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "mode": "research", "model": "IBM Granite 4.0 Nano 350M"})
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { v, e := s.State(r.Context()); respond(w, v, e) })
	mux.HandleFunc("POST /api/compile", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text  string `json:"text"`
			Rules []Rule `json:"rules"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Compile(r.Context(), in.Text, in.Rules)
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text          string `json:"text"`
			Engine        string `json:"engine"`
			BasePreviewID string `json:"basePreviewId,omitempty"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.PreviewFrom(r.Context(), in.Text, in.Engine, in.BasePreviewID)
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/apply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PreviewID string `json:"previewId"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Apply(r.Context(), in.PreviewID)
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/undo", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Undo(r.Context())
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/items", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Items []Item `json:"items"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Ingest(r.Context(), in.Items)
		respond(w, v, e)
	})
	if webDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(webDir)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host {
					writeJSON(w, 403, map[string]string{"error": "cross-origin API requests are disabled"})
					return
				}
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, 415, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 50<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request JSON: " + err.Error()})
		return false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "request must contain one JSON value"})
		return false
	}
	return true
}
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		status := 422
		if errors.Is(err, ErrConflict) {
			status = 409
		}
		if errors.Is(err, ErrNotFound) {
			status = 404
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = 504
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
