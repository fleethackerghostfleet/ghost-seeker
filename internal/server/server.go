// Package server exposes the ingest API for edge sensors and the operator API
// + live event stream used by the web UI.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

const maxUpload = 8 << 20

var allowedTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/svg+xml": true}

type Server struct {
	store       *store.Store
	hub         *hub
	web         fs.FS
	ingestToken string
}

const maxDetections = 32

func New(st *store.Store, web fs.FS, ingestToken string) *Server {
	return &Server{store: st, hub: newHub(), web: web, ingestToken: ingestToken}
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.Handle("GET /", http.FileServerFS(s.web))
	m.HandleFunc("POST /api/ingest", s.ingest)
	m.HandleFunc("GET /api/config", s.config)
	m.HandleFunc("GET /api/stats", s.stats)
	m.HandleFunc("GET /api/images", s.list)
	m.HandleFunc("GET /api/images/{id}", s.get)
	m.HandleFunc("GET /api/images/{id}/image", s.blob)
	m.HandleFunc("POST /api/images/{id}/review", s.review)
	m.HandleFunc("POST /api/directives", s.addDirective)
	m.HandleFunc("DELETE /api/directives/{id}", s.removeDirective)
	m.HandleFunc("DELETE /api/directives", s.clearDirectives)
	m.HandleFunc("PUT /api/model", s.setModel)
	m.HandleFunc("GET /api/events", s.events)
	return m
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	if s.ingestToken != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.ingestToken)) != 1 {
			httpError(w, http.StatusUnauthorized, "invalid ingest token")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		httpError(w, http.StatusBadRequest, "expected multipart form with meta and image: "+err.Error())
		return
	}
	var rep catalog.Report
	if err := json.Unmarshal([]byte(r.FormValue("meta")), &rep); err != nil {
		httpError(w, http.StatusBadRequest, "bad meta: "+err.Error())
		return
	}
	if _, ok := catalog.SourceByID(rep.Source); !ok {
		httpError(w, http.StatusBadRequest, "unknown source "+strconv.Quote(rep.Source))
		return
	}
	unit := func(f float64) bool { return f >= 0 && f <= 1 }
	if len(rep.Detections) > maxDetections || !unit(rep.EdgeScore) {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("edge_score must be within [0,1] and at most %d detections", maxDetections))
		return
	}
	for i := range rep.Detections {
		d := &rep.Detections[i]
		if _, ok := catalog.Classes[d.Class]; !ok {
			httpError(w, http.StatusBadRequest, "unknown class "+strconv.Quote(d.Class))
			return
		}
		if !unit(d.Conf) || !unit(d.Box.X) || !unit(d.Box.Y) || !unit(d.Box.W) || !unit(d.Box.H) {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("detection %d: conf and box must be within [0,1]", i))
			return
		}
		attrs := map[string]bool{}
		for k, v := range d.Attrs {
			if !catalog.Attrs[k] {
				httpError(w, http.StatusBadRequest, "unknown attribute "+strconv.Quote(k))
				return
			}
			if v {
				attrs[k] = true
			}
		}
		d.Attrs = attrs
	}
	if rep.CapturedAt == 0 {
		rep.CapturedAt = time.Now().UnixMilli()
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing image part")
		return
	}
	defer file.Close()
	ct := strings.TrimSpace(strings.Split(hdr.Header.Get("Content-Type"), ";")[0])
	if !allowedTypes[ct] {
		httpError(w, http.StatusUnsupportedMediaType, "image must be jpeg, png, webp or svg")
		return
	}
	img, err := io.ReadAll(file)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	it, st, err := s.store.Ingest(rep, ct, img)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.publish("image", map[string]any{"item": it, "stats": st})
	writeJSON(w, http.StatusCreated, it)
}

type modelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

func configPayload(c scoring.Config) map[string]any {
	return map[string]any{"model": c.Model, "directives": c.Directives, "weights": scoring.Weights(c)}
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	models := make([]modelInfo, 0, len(catalog.ModelOrder))
	for _, k := range catalog.ModelOrder {
		models = append(models, modelInfo{k, catalog.Models[k].Name, catalog.Models[k].Desc})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": catalog.Sources, "classes": catalog.Classes, "models": models,
		"keepThreshold": catalog.EdgeKeepThreshold, "config": configPayload(s.store.Config()), "stats": s.store.Stats(),
	})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Stats())
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > store.MaxItems {
		limit = 300
	}
	writeJSON(w, http.StatusOK, s.store.List(r.URL.Query().Get("src"), limit))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusNotFound, "no such image")
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) blob(w http.ResponseWriter, r *http.Request) {
	b, ct, err := s.store.Blob(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	// uploaded SVGs must never run script if opened directly
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "private, max-age=86400, immutable")
	w.Write(b)
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	var rv store.Review
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&rv); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	it, st, err := s.store.SetReview(r.PathValue("id"), rv)
	if err != nil {
		httpError(w, http.StatusNotFound, "no such image")
		return
	}
	s.hub.publish("update", map[string]any{"item": it, "stats": st})
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) rescored(w http.ResponseWriter, code int, res store.Rescore, extra map[string]any) {
	payload := map[string]any{"config": configPayload(res.Config), "scores": res.Scores, "promoted": res.Promoted, "demoted": res.Demoted}
	s.hub.publish("rescore", payload)
	out := map[string]any{"config": payload["config"], "promoted": res.Promoted, "demoted": res.Demoted, "rescored": len(res.Scores)}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, code, out)
}

func (s *Server) addDirective(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		httpError(w, http.StatusBadRequest, "text required")
		return
	}
	rules := scoring.ParseDirective(in.Text)
	if len(rules) == 0 {
		httpError(w, http.StatusUnprocessableEntity, "Could not map that to known objects or behaviors — try ships, aircraft, drones, people, AIS, approaching…")
		return
	}
	d := scoring.Directive{ID: fmt.Sprintf("d%d", time.Now().UnixNano()), Text: strings.TrimSpace(in.Text), Rules: rules}
	res := s.store.UpdateConfig(func(c *scoring.Config) { c.Directives = append(c.Directives, d) })
	s.rescored(w, http.StatusCreated, res, map[string]any{"directive": d})
}

func (s *Server) removeDirective(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	res := s.store.UpdateConfig(func(c *scoring.Config) {
		out := c.Directives[:0:0]
		for _, d := range c.Directives {
			if d.ID == id {
				found = true
				continue
			}
			out = append(out, d)
		}
		c.Directives = out
	})
	if !found {
		httpError(w, http.StatusNotFound, "no such directive")
		return
	}
	s.rescored(w, http.StatusOK, res, nil)
}

func (s *Server) clearDirectives(w http.ResponseWriter, r *http.Request) {
	res := s.store.UpdateConfig(func(c *scoring.Config) { c.Directives = []scoring.Directive{} })
	s.rescored(w, http.StatusOK, res, nil)
}

func (s *Server) setModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&in); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := catalog.Models[in.Model]; !ok {
		httpError(w, http.StatusBadRequest, "unknown model")
		return
	}
	res := s.store.UpdateConfig(func(c *scoring.Config) { c.Model = in.Model })
	s.rescored(w, http.StatusOK, res, nil)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	c := s.hub.subscribe()
	defer s.hub.unsubscribe(c)
	fmt.Fprintf(w, "retry: 2000\nevent: hello\ndata: {\"clients\":%d}\n\n", s.hub.count())
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c:
			w.Write(msg)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// LogRequests is a minimal access log for non-streaming API calls.
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		next.ServeHTTP(w, r)
		if r.Method != http.MethodGet {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(t).Round(time.Microsecond))
		}
	})
}
