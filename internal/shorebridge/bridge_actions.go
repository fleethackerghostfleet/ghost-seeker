package shorebridge

import (
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (b *Bridge) requestPull(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"itemId"`
		Form   string `json:"form"`
		// Original is the operator override: FULL at full size, no
		// re-encoding. It needs the operator's name in By.
		Original bool   `json:"original"`
		By       string `json:"by"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: pull request body: %v", err)
		http.Error(w, "invalid request body", httpx.RequestBodyStatus(err))
		return
	}
	if in.ItemID == "" {
		http.Error(w, "itemId required", http.StatusBadRequest)
		return
	}
	if !ids.Valid(in.ItemID) {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	by := opauth.Actor(r.Context(), "operator-ui")
	if in.Original {
		if by = opauth.Actor(r.Context(), strings.TrimSpace(in.By)); by == "" {
			http.Error(w, "operator name (by) required for a full-size override", http.StatusBadRequest)
			return
		}
	}
	kind := pb.FormKind_FORM_KIND_FULL
	for k, n := range formNames {
		if n == in.Form {
			kind = k
		}
	}
	var out pb.RequestPullResponse
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: in.ItemID, Form: kind, RequestedBy: by, Original: in.Original}}
	if err := b.call(r.Context(), b.shore, "RequestPull", req, &out); err != nil {
		log.Printf("shorebridge: request pull upstream: %v", err)
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	b.Poke()
	writeJSON(w, map[string]any{"ok": true, "itemId": in.ItemID, "form": formNames[kind], "original": in.Original})
}

func (b *Bridge) override(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"itemId"`
		Label  string `json:"label"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: override request body: %v", err)
		http.Error(w, "invalid request body", httpx.RequestBodyStatus(err))
		return
	}
	if in.ItemID == "" {
		http.Error(w, "itemId required", http.StatusBadRequest)
		return
	}
	if !ids.Valid(in.ItemID) {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(in.Label) > 500 {
		http.Error(w, "label exceeds 500 characters", http.StatusBadRequest)
		return
	}
	var out pb.OverrideLabelResponse
	req := &pb.OverrideLabelRequest{Override: &pb.LabelOverride{ItemId: in.ItemID, Label: in.Label, By: opauth.Actor(r.Context(), "operator-ui")}}
	if err := b.call(r.Context(), b.shore, "OverrideLabel", req, &out); err != nil {
		log.Printf("shorebridge: override upstream: %v", err)
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
