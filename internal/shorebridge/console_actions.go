package shorebridge

import (
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"log"
	"net/http"
	"strings"
	"time"
)

func (c *Console) patchItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Manual    *int  `json:"manual"`
		Reviewed  *bool `json:"reviewed"`
		Dismissed *bool `json:"dismissed"`
		Sent      *bool `json:"sent"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Manual != nil && (*body.Manual < 0 || *body.Manual > 4) {
		writeError(w, http.StatusBadRequest, "manual must be 0..4")
		return
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	if base == nil {
		c.mu.Unlock()
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	if isScene(base) {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, "camera overviews cannot be reviewed or escalated")
		return
	}
	o := c.overlayFor(id)
	if body.Manual != nil {
		o.Manual = *body.Manual
	}
	if body.Reviewed != nil {
		o.Reviewed = *body.Reviewed
	}
	if body.Dismissed != nil {
		o.Dismissed = *body.Dismissed
	}
	if body.Sent != nil && o.Sent != *body.Sent {
		if *body.Sent {
			now := time.Now()
			o.SentAt, o.Decision, o.DecisionNote, o.DecidedAt = &now, "", "", nil
		} else {
			o.SentAt = nil
		}
		o.Sent = *body.Sent
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	c.mu.Unlock()
	writeJSON(w, map[string]any{"item": item, "cdr": cdr})
}

func (c *Console) overlayFor(id string) *overlay {
	o := c.overlay[id]
	if o == nil {
		o = &overlay{}
		c.overlay[id] = o
	}
	return o
}

func (c *Console) decide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Decision != "" && !store.Decisions[body.Decision] {
		writeError(w, http.StatusBadRequest, store.ErrUnknownDecision.Error())
		return
	}
	note := []rune(strings.TrimSpace(body.Note))
	if len(note) > 280 {
		note = note[:280]
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	if base == nil {
		c.mu.Unlock()
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	if isScene(base) {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, "camera overviews cannot be escalated")
		return
	}
	o := c.overlayFor(id)
	if !o.Sent && o.Decision != store.DecisionReturn {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, store.ErrNotEscalated.Error())
		return
	}
	now := time.Now()
	switch {
	case body.Decision == store.DecisionReturn && o.Sent:
		o.Sent, o.SentAt, o.Reviewed, o.Dismissed = false, nil, false, false
	case body.Decision != store.DecisionReturn && !o.Sent:
		o.Sent, o.SentAt, o.Reviewed = true, &now, true
	}
	o.Decision, o.DecisionNote, o.DecidedAt = body.Decision, string(note), &now
	if body.Decision == "" {
		o.DecisionNote, o.DecidedAt = "", nil
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	c.mu.Unlock()
	writeJSON(w, map[string]any{"item": item, "cdr": cdr})
}

// fullRes is the operator override to send the original image at full size,
// no re-encoding: RequestPull{original:true} to the shore for the card's item.
func (c *Console) fullRes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		By string `json:"by"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	by := opauth.Actor(r.Context(), strings.TrimSpace(body.By))
	if by == "" {
		writeError(w, http.StatusBadRequest, store.ErrOperatorRequired.Error())
		return
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	c.mu.Unlock()
	if base == nil || base.Live == nil {
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	if isScene(base) {
		writeError(w, http.StatusConflict, "pull camera overviews from Cameras")
		return
	}
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: base.Live.ItemID, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: by, Original: true}}
	var out pb.RequestPullResponse
	if err := c.b.call(r.Context(), c.b.shore, "RequestPull", req, &out); err != nil {
		log.Printf("console: full-res pull upstream: %v", err)
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	c.b.Poke()
	c.mu.Lock()
	o := c.overlayFor(id)
	if !o.FullRes {
		now := time.Now()
		o.FullRes, o.FullResBy, o.FullResAt = true, by, &now
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	c.mu.Unlock()
	writeJSON(w, map[string]any{"item": item, "cdr": cdr})
}

// pull asks the edge for one more form of the card's shore item (FULL by
// default): the fidelity ladder's "give me the rest" action.
func (c *Console) pull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Form string `json:"form"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	c.mu.Unlock()
	if base == nil || base.Live == nil {
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	kind := pb.FormKind_FORM_KIND_FULL
	for k, n := range formNames {
		if n == body.Form {
			kind = k
		}
	}
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: base.Live.ItemID, Form: kind, RequestedBy: opauth.Actor(r.Context(), "operator-ui")}}
	var out pb.RequestPullResponse
	if err := c.b.call(r.Context(), c.b.shore, "RequestPull", req, &out); err != nil {
		log.Printf("console: pull upstream: %v", err)
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	c.b.Poke()
	writeJSON(w, map[string]any{"ok": true, "itemId": base.Live.ItemID, "form": formNames[kind]})
}
