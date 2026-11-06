package shorebridge

import (
	"errors"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func prioritizationMode(snap *Snapshot) Prioritization {
	mode := strings.ToLower(strings.TrimSpace(snap.Edge.Prioritizer))
	switch mode {
	case "tomer":
		return Prioritization{Mode: mode, Label: "Image scoring model", Source: "edge"}
	case "both":
		return Prioritization{Mode: mode, Label: "Combined ship and image scoring models", Source: "edge"}
	case "edgeml":
		return Prioritization{Mode: mode, Label: "Object scoring model", Source: "edge"}
	case "":
		return Prioritization{Mode: "edge", Label: "Ship score", Source: "default"}
	}
	return Prioritization{Mode: mode, Label: "Ship scoring model: " + mode, Source: "edge"}
}

func (c *Console) policyFrom(snap *Snapshot, pol *pb.Policy) LivePolicy {
	p := LivePolicy{
		ID: snap.PolicyID, Text: snap.Policy, Suppress: dedupe(snap.Suppress), Boost: dedupe(snap.Boost),
		TierLabels: snap.TierLabels,
		NodeID:     "shore", EMCON: EmconSummary{Mode: "NORMAL"}, Locks: []PolicyLock{}, Conflicts: []PolicyConflict{},
	}
	p.Readback = tierReadback(p.Suppress, p.Boost, p.TierLabels)
	c.pol, c.ruleFor = nil, nil
	if pol == nil {
		return p
	}
	return c.policyView(pol, time.Now())
}

// policyView renders the stored policy as it applies at now: expired rules and
// EMCON are dropped (policy.Flatten), each directive is listed with its own
// deadline and the readback says when each rule lapses. It also caches the
// policy and the label -> directive index the item notes use.
func (c *Console) policyView(pol *pb.Policy, now time.Time) LivePolicy {
	pol = viewPolicy(pol, c.b.currentEdgeID(), now)
	info := policyInfo(pol, c.b.echelon()) // same merge/lock view as the F2 strip (#57)
	p := LivePolicy{
		ID: pol.GetId(), Text: strings.TrimSpace(pol.GetText()), NodeID: "shore", Version: info.Version,
		Suppress: dedupe(pol.GetInterest().GetSuppressLabels()), Boost: dedupe(pol.GetInterest().GetBoostLabels()),
		TierLabels: pol.GetInterest().GetTierLabels(),
		EMCON:      EmconSummary{Mode: info.Emcon, SetBy: info.EmconSetBy, Locked: info.EmconLocked, LockedBy: info.EmconLockedBy},
		Locks:      info.Locks, Conflicts: info.Conflicts,
	}
	directives := c.b.directives(pol, now)
	c.pol, c.ruleFor = pol, map[string]string{}
	for _, d := range directives {
		for _, r := range d.Rules {
			for _, class := range r.Classes {
				if _, seen := c.ruleFor[class]; !seen {
					c.ruleFor[class] = d.ID
				}
			}
		}
	}
	if next := nextPolicyExpiry(pol, now); !next.IsZero() {
		p.ExpiresAtUnixMs = next.UnixMilli()
	}
	if len(pol.Layers) > 0 {
		p.Text = directivesText(directives)
		p.Readback = directivesReadback(directives)
		return p
	}
	p.Readback = tierReadback(p.Suppress, p.Boost, p.TierLabels)
	if p.ExpiresAtUnixMs > 0 {
		until := time.UnixMilli(p.ExpiresAtUnixMs).UTC().Format("15:04Z")
		p.Readback = strings.Replace(p.Readback, "Until cleared.", "Until "+until+".", 1)
	}
	return p
}

// adoptPolicy replaces the cached view with the policy the shore just applied,
// so the next directive and GET /api/policy see it before the poller does.
func (c *Console) adoptPolicy(pol *pb.Policy) {
	if pol == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policy = c.policyView(pol, time.Now())
	c.live.Policy = c.policy
	c.publish("snapshot", c.snapshotLocked())
}

func (c *Console) directiveFor(label string) string {
	if id, ok := c.ruleFor[strings.ToLower(strings.TrimSpace(label))]; ok {
		return id
	}
	return flatDirectiveID
}

func emconLabel(mode pb.EmconMode) string {
	switch mode {
	case pb.EmconMode_EMCON_MODE_BURST:
		return "BURST"
	case pb.EmconMode_EMCON_MODE_SILENT:
		return "SILENT"
	default:
		return "NORMAL"
	}
}

func dedupe(labels []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range labels {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func (c *Console) directivesLocked() []command.Directive {
	if c.pol == nil {
		rules := policyRules(flatDirectiveID, c.policy.Suppress, c.policy.Boost, c.policy.TierLabels)
		if len(rules) == 0 {
			return []command.Directive{}
		}
		text := c.policy.Text
		if strings.TrimSpace(text) == "" {
			text = c.policy.Readback
		}
		return []command.Directive{{ID: flatDirectiveID, Text: text, Rules: rules}}
	}
	return c.b.directives(c.pol, time.Now())
}

func (c *Console) addDirective(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text        string          `json:"text"`
		Rules       *[]command.Rule `json:"rules"`
		DurationMin int             `json:"durationMin"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Rules != nil {
		for _, rule := range *body.Rules {
			if rule.Tier < 0 || rule.Tier > 4 {
				writeError(w, http.StatusBadRequest, "target tier must be between 1 and 4")
				return
			}
		}
	}
	if utf8.RuneCountInString(body.Text) > 500 {
		writeError(w, http.StatusBadRequest, "text exceeds 500 characters")
		return
	}
	if body.DurationMin < 0 || body.DurationMin > 10080 {
		writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
		return
	}
	cd := c.compileDirective(r.Context(), body.Text, body.Rules, body.DurationMin)
	if cd.Blocked {
		msg := "directive is blocked"
		if cd.Compiled != nil {
			msg = blockedMessage(*cd.Compiled)
		}
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	suppress, boost, rules := cd.Suppress, cd.Boost, cd.Rules
	if len(rules) == 0 && cd.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		writeError(w, http.StatusUnprocessableEntity, "Could not map that to known objects — try \"ignore cargo ships\" or \"prioritize submarines\".")
		return
	}
	pol := &pb.Policy{IssuedBy: opauth.Actor(r.Context(), c.b.issuer()), Text: body.Text, Interest: &pb.Interest{SuppressLabels: suppress, BoostLabels: boost, TierLabels: ruleTiers(rules)}, Emcon: cd.Emcon}
	if cd.DurationMin > 0 {
		pol.ExpiresAtUnixMs = time.Now().Add(time.Duration(cd.DurationMin) * time.Minute).UnixMilli()
	}
	// The directive is layered onto the policy the shore holds right now (read
	// inside addDirective), never onto this console's cached view, so two
	// directives posted seconds apart both survive.
	out, ruleID, err := c.b.addDirective(r.Context(), pol)
	if err != nil {
		log.Printf("console: set policy upstream: %v", err)
		if writeShoreAuthError(w, err) {
			return
		}
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	c.adoptPolicy(out.GetPolicy())
	if ruleID == "" {
		ruleID = flatDirectiveID
	}
	for i := range rules {
		rules[i].DirectiveID = ruleID
	}
	readback := withDuration(tierReadback(suppress, boost, ruleTiers(rules)), cd.DurationMin)
	if cd.Compiled != nil || cd.Emcon != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		readback = cd.Readback
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"id": ruleID, "text": body.Text, "rules": rules, "readback": readback, "warnings": cd.Warnings, "path": cd.Path, "compiled": cd.Compiled,
		"expiresAtUnixMs": pol.GetExpiresAtUnixMs(),
		"suppressedCount": out.GetSuppressedCount(), "boostedCount": out.GetBoostedCount(), "conflicts": conflictList(out.GetConflicts()),
	})
}

func conflictList(conflicts []*pb.MergeConflict) []PolicyConflict {
	out := []PolicyConflict{}
	for _, c := range conflicts {
		out = append(out, PolicyConflict{RuleID: c.GetRuleId(), Reason: c.GetReason(), BlockedBy: c.GetBlockedBy()})
	}
	return out
}

// clearDirectives removes the operator's own directives: all of them on
// DELETE /api/directives, one on DELETE /api/directives/{id}. Other operators'
// and higher-echelon layers are never touched.
func (c *Console) clearDirectives(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if id := strings.TrimSpace(r.PathValue("id")); id != "" {
		ids = []string{id}
	}
	out, err := c.b.removeDirectives(r.Context(), ids)
	if err != nil {
		if errors.Is(err, errDirectiveNotFound) {
			writeError(w, http.StatusNotFound, "That directive is no longer active, or it was issued by another echelon and cannot be removed here.")
			return
		}
		log.Printf("console: clear policy upstream: %v", err)
		if writeShoreAuthError(w, err) {
			return
		}
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	c.adoptPolicy(out.GetPolicy())
	w.WriteHeader(http.StatusNoContent)
}
