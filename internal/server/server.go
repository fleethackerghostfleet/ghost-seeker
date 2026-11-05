package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"github.com/fleethackerghostfleet/ghost-seeker/web"
)

type Server struct {
	store    *store.Store
	hub      *hub
	mux      *http.ServeMux
	compiler *command.Compiler
	after    func(time.Duration, func())
}

const maxStreamClients = 64

func New(data *store.Store) *Server {
	s := &Server{
		store: data, hub: newHub(), mux: http.NewServeMux(),
		compiler: &command.Compiler{}, after: func(delay time.Duration, fn func()) { time.AfterFunc(delay, fn) },
	}
	s.mux.HandleFunc("GET /api/config", s.config)
	s.mux.HandleFunc("GET /api/stream", s.stream)
	s.mux.HandleFunc("POST /api/directives", s.addDirective)
	s.mux.HandleFunc("POST /api/directives/compile", s.compileDirective)
	s.mux.HandleFunc("POST /api/directives/preview", s.previewDirectives)
	s.mux.HandleFunc("DELETE /api/directives", s.clearDirectives)
	s.mux.HandleFunc("DELETE /api/directives/{id}", s.deleteDirective)
	s.mux.HandleFunc("PUT /api/model", s.setModel)
	s.mux.HandleFunc("PATCH /api/items/{id}", s.patchItem)
	s.mux.HandleFunc("POST /api/items/{id}/decision", s.decide)
	s.mux.HandleFunc("POST /api/items/{id}/full-res", s.fullRes)
	s.mux.Handle("GET /cdr", http.RedirectHandler("/cdr.html", http.StatusFound))
	s.mux.Handle("GET /", web.Handler())
	return s
}

func (s *Server) SetCompiler(compiler *command.Compiler) {
	if compiler == nil {
		compiler = &command.Compiler{}
	}
	s.compiler = compiler
}

func (s *Server) directiveCompiler() *command.Compiler {
	if s.compiler == nil {
		return &command.Compiler{}
	}
	return s.compiler
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) PublishItem(item command.Item, stats command.Stats) {
	s.hub.publish("item", map[string]any{"item": item, "stats": stats})
}

// PublishUpdate broadcasts a review or commander decision on one item.
func (s *Server) PublishUpdate(item command.Item, cdr int) {
	s.hub.publish("update", map[string]any{"item": item, "cdr": cdr})
}

func (s *Server) PublishStats(stats command.Stats) {
	s.hub.publish("stats", map[string]any{"stats": stats})
}

func (s *Server) RescoreTimeScoped(now time.Time) {
	if s.store.RescoreTimeScoped(now) {
		s.publishSnapshot()
	}
}

func (s *Server) PublishEvict(id string) {
	if id != "" {
		s.hub.publish("evict", map[string]string{"id": id})
	}
}

func (s *Server) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": catalog.Sources, "classes": catalog.Classes, "models": catalog.Models,
		"attrLabels": catalog.AttrLabels, "examples": catalog.Examples,
	})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	client, ok := s.hub.add()
	if !ok {
		http.Error(w, "too many stream clients", http.StatusServiceUnavailable)
		return
	}
	defer s.hub.remove(client)
	items, stats, cdr, state := s.store.Snapshot()
	if !writeEvent(w, "snapshot", map[string]any{"state": state, "stats": stats, "cdr": cdr, "items": items, "weights": command.EffectiveWeights(state.Model, state.Directives)}) {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-client.events:
			if !open {
				return
			}
			if !writeEvent(w, event.name, event.data) {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) addDirective(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text        string          `json:"text"`
		Rules       *[]command.Rule `json:"rules"`
		DurationMin int             `json:"durationMin"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &body); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if utf8.RuneCountInString(body.Text) > 500 {
		writeError(w, http.StatusBadRequest, "Directive text must be 500 characters or fewer")
		return
	}
	if body.DurationMin < 0 || body.DurationMin > 10080 {
		writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
		return
	}
	compiler := s.directiveCompiler()
	var compiled command.Compiled
	var rules []command.Rule
	durationMin := body.DurationMin
	if body.Rules != nil {
		rules = *body.Rules
		compiled = compiler.Review(body.Text, rules, durationMin)
		durationMin = compiled.DurationMin
	} else {
		compiled = compiler.Compile(r.Context(), body.Text)
		rules = compiled.Rules
		if durationMin == 0 {
			durationMin = compiled.DurationMin
		}
	}
	if compiled.Blocked {
		message := ""
		for _, warning := range compiled.Warnings {
			if strings.HasPrefix(warning, "Blocked") {
				message = warning
				break
			}
		}
		if message == "" && len(compiled.Warnings) > 0 {
			message = compiled.Warnings[0]
		}
		if message == "" {
			message = "Directive is blocked"
		}
		writeError(w, http.StatusUnprocessableEntity, message)
		return
	}
	if err := validateRules(rules); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(rules) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "Could not map that to known objects or behaviors — try ships, aircraft, drones, people, AIS, approaching…")
		return
	}
	if durationMin < 0 || durationMin > 10080 {
		writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
		return
	}
	directive, err := s.store.AddDirective(strings.TrimSpace(body.Text), rules)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.publishSnapshot()
	audit(r, "directive.add", "id", directive.ID, "text", directive.Text)
	if durationMin > 0 {
		id := directive.ID
		s.after(time.Duration(durationMin)*time.Minute, func() {
			if s.store.DeleteDirective(id) {
				s.publishSnapshot()
			}
		})
	}
	writeJSON(w, http.StatusCreated, directive)
}

func (s *Server) compileDirective(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text        string          `json:"text"`
		Rules       *[]command.Rule `json:"rules"`
		DurationMin int             `json:"durationMin"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if utf8.RuneCountInString(body.Text) > 500 {
		writeError(w, http.StatusBadRequest, "Directive text must be 500 characters or fewer")
		return
	}
	compiler := s.directiveCompiler()
	var compiled command.Compiled
	if body.Rules != nil {
		if err := validateRules(*body.Rules); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.DurationMin < 0 || body.DurationMin > 10080 {
			writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
			return
		}
		compiled = compiler.Review(body.Text, *body.Rules, body.DurationMin)
	} else {
		compiled = compiler.Compile(r.Context(), body.Text)
	}
	writeJSON(w, http.StatusOK, compiled)
}

func (s *Server) deleteDirective(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteDirective(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	s.publishSnapshot()
	audit(r, "directive.delete", "id", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearDirectives(w http.ResponseWriter, r *http.Request) {
	s.store.ClearDirectives()
	s.publishSnapshot()
	audit(r, "directive.clear")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &body); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if _, ok := catalog.Models[body.ID]; !ok {
		writeError(w, http.StatusBadRequest, "unknown model id")
		return
	}
	s.store.SetModel(body.ID)
	s.publishSnapshot()
	audit(r, "model.set", "id", body.ID)
	writeJSON(w, http.StatusOK, map[string]string{"id": body.ID})
}

func (s *Server) patchItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Manual    *int  `json:"manual"`
		Reviewed  *bool `json:"reviewed"`
		Dismissed *bool `json:"dismissed"`
		Sent      *bool `json:"sent"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &body); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if body.Manual != nil && (*body.Manual < 0 || *body.Manual > 4) {
		writeError(w, http.StatusBadRequest, "manual must be between 0 and 4")
		return
	}
	item, cdr, ok := s.store.PatchItem(r.PathValue("id"), body.Manual, body.Reviewed, body.Dismissed, body.Sent)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.PublishUpdate(item, cdr)
	args := []any{"id", item.ID}
	if body.Manual != nil {
		args = append(args, "manual", *body.Manual)
	}
	if body.Reviewed != nil {
		args = append(args, "reviewed", *body.Reviewed)
	}
	if body.Dismissed != nil {
		args = append(args, "dismissed", *body.Dismissed)
	}
	if body.Sent != nil {
		args = append(args, "sent", *body.Sent)
	}
	audit(r, "item.patch", args...)
	writeJSON(w, http.StatusOK, map[string]any{"item": item, "cdr": cdr})
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &body); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	item, cdr, err := s.store.Decide(r.PathValue("id"), body.Decision, body.Note)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrUnknownDecision):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.PublishUpdate(item, cdr)
	audit(r, "item.decide", "id", item.ID, "decision", body.Decision, "note", body.Note)
	writeJSON(w, http.StatusOK, map[string]any{"item": item, "cdr": cdr})
}

// fullRes is the operator override to send an image's original bytes at
// full size. It requires the operator's name.
func (s *Server) fullRes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		By string `json:"by"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, cdr, err := s.store.RequestFullRes(r.PathValue("id"), body.By)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrOperatorRequired):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.PublishUpdate(item, cdr)
	writeJSON(w, http.StatusOK, map[string]any{"item": item, "cdr": cdr})
}

func (s *Server) publishSnapshot() {
	items, stats, cdr, state := s.store.Snapshot()
	s.hub.publish("snapshot", map[string]any{"state": state, "stats": stats, "cdr": cdr, "items": items, "weights": command.EffectiveWeights(state.Model, state.Directives)})
}

func decodeJSON(reader io.Reader, dst any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func writeRequestBodyError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}

// There is no authenticated identity yet, so the log records the source address only.
func audit(r *http.Request, action string, args ...any) {
	fields := make([]string, 0, (len(args)+1)/2)
	for i := 0; i < len(args); i += 2 {
		value := ""
		if i+1 < len(args) {
			value = fmt.Sprint(args[i+1])
		}
		fields = append(fields, fmt.Sprintf("%v=%q", args[i], value))
	}
	log.Printf("audit action=%q source=%q request=%s %q args=%s", action, r.RemoteAddr, r.Method, r.URL.Path, strings.Join(fields, " "))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeEvent(w io.Writer, name string, value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err == nil
}

type event struct {
	name string
	data any
}

type client struct {
	events chan event
}

type hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

func newHub() *hub {
	return &hub{clients: make(map[*client]struct{})}
}

func (h *hub) add() (*client, bool) {
	c := &client{events: make(chan event, 32)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= maxStreamClients {
		return nil, false
	}
	h.clients[c] = struct{}{}
	return c, true
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	if _, exists := h.clients[c]; exists {
		delete(h.clients, c)
		close(c.events)
	}
	h.mu.Unlock()
}

func (h *hub) publish(name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.events <- event{name: name, data: data}:
		default:
			delete(h.clients, c)
			close(c.events)
		}
	}
}
