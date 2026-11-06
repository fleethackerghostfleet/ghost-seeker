package rnd

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
)

//go:embed grade.html
var page []byte

func (s *Session) Handler(allowHosts ...string) http.Handler {
	allowed := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true}
	for _, host := range allowHosts {
		if host = hostname(host); host != "" {
			allowed[host] = true
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { respond(w, s.State(), nil) })
	mux.HandleFunc("GET /images/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, ok := s.byID[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.dir, "images", item.Image.File))
	})
	mux.HandleFunc("POST /api/grade", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID   string `json:"id"`
			Tier int    `json:"tier"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if err := d.Decode(&req); err != nil {
			http.Error(w, err.Error(), httpx.RequestBodyStatus(err))
			return
		}
		if err := d.Decode(new(any)); err != io.EOF {
			http.Error(w, "single JSON object required", httpx.RequestBodyStatus(err))
			return
		}
		if _, ok := s.byID[req.ID]; !ok || req.Tier < 1 || req.Tier > 4 {
			http.Error(w, "unknown image or tier outside 1-4", 400)
			return
		}
		state, err := s.Grade(req.ID, req.Tier)
		respond(w, state, err)
	})
	mux.HandleFunc("POST /api/undo", func(w http.ResponseWriter, r *http.Request) { state, err := s.Undo(); respond(w, state, err) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		host := hostname(r.Host)
		if !allowed[host] {
			log.Printf("grader rejected Host=%q method=%s", r.Host, r.Method)
			http.Error(w, "host not allowed", http.StatusForbidden)
			return
		}
		log.Printf("grader access Host=%q method=%s", r.Host, r.Method)
		if r.Method == "POST" {
			// This unauthenticated tool is loopback-only by default. Reject browser
			// cross-origin mutation requests, including form posts.
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "cross-origin request", 403)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				http.Error(w, "cross-origin request", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func hostname(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(name)
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return ""
	}
	return strings.ToLower(host)
}
func respond(w http.ResponseWriter, state State, err error) {
	if err != nil {
		code := 500
		if errors.Is(err, ErrNoUndo) {
			code = 409
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}
