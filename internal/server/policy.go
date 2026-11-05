package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Option func(*serverOptions)

type serverOptions struct {
	admin      bool
	adminToken string
	backend    policy.Backend
	nodeID     string
}

func WithAdmin(enabled bool) Option {
	return func(options *serverOptions) { options.admin = enabled }
}

// WithAdminToken sets the operator bearer token that super-admin policy
// edits must present explicitly (Authorization: Bearer) on each request, in
// addition to the browser session: a re-authentication step for the
// privileged raw-policy editor. Empty disables admin edits even with -admin.
func WithAdminToken(token string) Option {
	return func(options *serverOptions) { options.adminToken = token }
}

func WithPolicyBackend(backend policy.Backend) Option {
	return func(options *serverOptions) { options.backend = backend }
}

func WithNodeID(nodeID string) Option {
	return func(options *serverOptions) { options.nodeID = nodeID }
}

type policyRuntime struct {
	mu            sync.Mutex
	timerMu       sync.Mutex
	store         *store.Store
	guard         *policy.Store
	admin         bool
	adminToken    string
	nodeID        string
	higherLayers  []*pb.PolicyLayer
	operatorEMCON *pb.EmconSetting
	expiryTimer   *time.Timer
	onChange      func()
	err           error
}

func newPolicyRuntime(options serverOptions, data *store.Store) *policyRuntime {
	seedPolicy := options.admin || options.backend != nil || options.nodeID != ""
	if options.backend == nil {
		options.backend = policy.NewMemBackend()
	}
	if options.nodeID == "" {
		options.nodeID = "DDG-51"
	}
	rt := &policyRuntime{admin: options.admin, adminToken: options.adminToken, nodeID: options.nodeID, store: data}
	guard, err := policy.NewStore(context.Background(), options.backend, policy.Options{})
	if err != nil {
		rt.err = err
		return rt
	}
	rt.guard = guard
	if active, _ := guard.Active(time.Now()); active != nil {
		for _, layer := range active.Layers {
			if layer.Echelon == pb.Echelon_ECHELON_SHIP && layer.IssuedBy == "TAO" {
				rt.operatorEMCON = cloneEmcon(layer.Emcon)
				rt.restoreOperatorLayer(layer)
			} else {
				rt.higherLayers = append(rt.higherLayers, proto.Clone(layer).(*pb.PolicyLayer))
			}
		}
		layers := make([]*pb.PolicyLayer, 0, len(rt.higherLayers)+1)
		for _, layer := range rt.higherLayers {
			layers = append(layers, proto.Clone(layer).(*pb.PolicyLayer))
		}
		layers = append(layers, rt.operatorLayer())
		rt.syncStore(&pb.Policy{Layers: layers}, time.Now())
		rt.armExpiry(active)
		return rt
	}
	if !seedPolicy {
		return rt
	}
	rt.higherLayers = []*pb.PolicyLayer{{
		Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{
			{Id: "ctf72-submarine", Author: "CTF-72", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3, Text: "CTF-72: submarines are P1 Critical — locked"},
			{Id: "ctf72-tanker", Author: "CTF-72", BoostLabels: []string{"tanker"}, Text: "CTF-72: report tankers (delegated)"},
		},
	}}
	if _, err := rt.apply("ui", "CTF-72", "Seed command policy", time.Now()); err != nil {
		rt.err = err
	}
	return rt
}

func (rt *policyRuntime) restoreOperatorLayer(layer *pb.PolicyLayer) {
	byID := make(map[string]*command.Directive)
	var order []string
	for _, rule := range layer.Rules {
		id, _, scoped, ok := parsePolicyRuleID(rule.Id)
		if !ok || id == "" {
			continue
		}
		if scoped {
			log.Printf("policy: skipping scoped operator rule %q during restore", rule.Id)
			continue
		}
		directive := byID[id]
		if directive == nil {
			directive = &command.Directive{ID: id, Text: rule.Text}
			byID[id] = directive
			order = append(order, id)
		}
		cmdRule := command.Rule{DirectiveID: id, Weight: rule.Weight}
		if len(rule.BoostLabels) > 0 {
			cmdRule.Type = "boost"
			cmdRule.Classes, cmdRule.Attrs = splitLabels(rule.BoostLabels)
		} else {
			cmdRule.Type = "suppress"
			cmdRule.Classes, cmdRule.Attrs = splitLabels(rule.SuppressLabels)
		}
		directive.Rules = append(directive.Rules, cmdRule)
	}
	directives := make([]command.Directive, 0, len(order))
	for _, id := range order {
		if len(byID[id].Rules) > 0 {
			directives = append(directives, *byID[id])
		}
	}
	rt.store.RestoreDirectives(directives)
}

func parsePolicyRuleID(ruleID string) (string, int, bool, bool) {
	parts := strings.SplitN(ruleID, ":", 3)
	if len(parts) < 2 || parts[0] == "" {
		return "", 0, false, false
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, false, false
	}
	return parts[0], index, len(parts) == 3 && parts[2] == "scoped", true
}

func (rt *policyRuntime) apply(source, who, text string, now time.Time) (*pb.Policy, error) {
	if rt.guard == nil {
		return nil, rt.err
	}
	layers := make([]*pb.PolicyLayer, 0, len(rt.higherLayers)+1)
	for _, layer := range rt.higherLayers {
		layers = append(layers, proto.Clone(layer).(*pb.PolicyLayer))
	}
	layers = append(layers, rt.operatorLayer())
	p := &pb.Policy{
		Id: fmt.Sprintf("sim-%d", now.UnixMilli()), IssuedAtUnixMs: now.UnixMilli(),
		IssuedBy: who, Text: text, Version: rt.guard.LastVersion() + 1, Layers: layers,
	}
	p = policy.Flatten(p, rt.nodeID, now)
	accepted, err := rt.guard.Accept(context.Background(), p, source, now)
	if err != nil {
		return accepted, err
	}
	rt.syncStore(accepted, now)
	rt.armExpiry(accepted)
	return accepted, nil
}

func (rt *policyRuntime) armExpiry(p *pb.Policy) {
	rt.timerMu.Lock()
	defer rt.timerMu.Unlock()
	if rt.expiryTimer != nil {
		rt.expiryTimer.Stop()
		rt.expiryTimer = nil
	}
	if p == nil {
		return
	}
	expiry := policy.Expiry(p, policy.DefaultTTL)
	if expiry.IsZero() {
		return
	}
	delay := time.Until(expiry)
	if delay < 0 {
		delay = 0
	}
	rt.expiryTimer = time.AfterFunc(delay, func() {
		rt.expire(time.Now())
	})
}

func (rt *policyRuntime) expire(now time.Time) {
	rt.mu.Lock()
	if rt.guard == nil {
		rt.mu.Unlock()
		return
	}
	_, expired := rt.guard.Active(now)
	if !expired {
		rt.mu.Unlock()
		return
	}
	_ = rt.guard.MarkExpired(context.Background(), now)
	rt.higherLayers = nil
	rt.store.SetPolicyDirectives(nil, nil)
	rt.mu.Unlock()
	if rt.onChange != nil {
		rt.onChange()
	}
}

func (rt *policyRuntime) operatorLayer() *pb.PolicyLayer {
	layer := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Emcon: cloneEmcon(rt.operatorEMCON)}
	_, _, _, state := rt.store.Snapshot()
	for _, directive := range state.Directives {
		if directive.Source != "" {
			continue
		}
		for i, rule := range directive.Rules {
			labels := append(append([]string(nil), rule.Classes...), rule.Attrs...)
			id := fmt.Sprintf("%s:%d", directive.ID, i)
			if rule.Only || len(rule.Sources) > 0 || rule.WithinMin != 0 {
				id += ":scoped"
			}
			interestRule := &pb.InterestRule{
				Id: id, Author: "TAO", Text: directive.Text, Weight: rule.Weight,
			}
			if rule.Type == "suppress" {
				interestRule.SuppressLabels = append([]string(nil), labels...)
			} else {
				interestRule.BoostLabels = append([]string(nil), labels...)
			}
			layer.Rules = append(layer.Rules, interestRule)
		}
	}
	return layer
}

func (rt *policyRuntime) syncStore(p *pb.Policy, now time.Time) {
	accepted, _, conflicts := policy.MergeRules(p.Layers, rt.nodeID, now)
	source := make([]command.Directive, 0)
	for _, item := range accepted {
		if item.Layer.Echelon == pb.Echelon_ECHELON_SHIP && item.Layer.IssuedBy == "TAO" {
			continue
		}
		for _, rule := range commandRules(item.Rule) {
			if len(rule.Classes)+len(rule.Attrs) == 0 {
				continue
			}
			issuer := item.Layer.IssuedBy
			text := item.Rule.Text
			if text == "" {
				text = issuer
			}
			id := "policy:" + item.Rule.Id
			source = append(source, command.Directive{ID: id, Text: text, Rules: []command.Rule{rule}, Source: issuer, Locked: item.Rule.Locked, LockedBy: issuer})
		}
	}
	blocked := make(map[string]map[int]string)
	for _, conflict := range conflicts {
		if conflict == nil || conflict.RuleId == "emcon" {
			continue
		}
		id, index, _, ok := parsePolicyRuleID(conflict.RuleId)
		if ok {
			if blocked[id] == nil {
				blocked[id] = make(map[int]string)
			}
			blocked[id][index] = conflict.Reason
		}
	}
	rt.store.SetPolicyDirectives(source, blocked)
}

func commandRules(rule *pb.InterestRule) []command.Rule {
	var result []command.Rule
	if len(rule.BoostLabels) > 0 {
		classes, attrs := splitLabels(rule.BoostLabels)
		result = append(result, command.Rule{Type: "boost", Classes: classes, Attrs: attrs, Weight: effectivePolicyWeight(rule.Weight)})
	}
	if len(rule.SuppressLabels) > 0 {
		classes, attrs := splitLabels(rule.SuppressLabels)
		result = append(result, command.Rule{Type: "suppress", Classes: classes, Attrs: attrs, Weight: effectivePolicyWeight(rule.Weight)})
	}
	return result
}

func effectivePolicyWeight(weight float64) float64 {
	if weight > 0 {
		return weight
	}
	return policy.DefaultRuleWeight
}

func splitLabels(labels []string) ([]string, []string) {
	var classes, attrs []string
	for _, label := range labels {
		if _, ok := catalog.Classes[label]; ok {
			classes = append(classes, label)
		} else if _, ok := catalog.AttrLabels[label]; ok {
			attrs = append(attrs, label)
		}
	}
	return classes, attrs
}

type policySummary struct {
	Version         int64             `json:"version"`
	Admin           bool              `json:"admin"`
	NodeID          string            `json:"nodeId"`
	EMCON           emconSummary      `json:"emcon"`
	Locks           []lockSummary     `json:"locks"`
	Conflicts       []conflictSummary `json:"conflicts"`
	ExpiresAtUnixMs int64             `json:"expiresAtUnixMs"`
}

type conflictSummary struct {
	RuleID    string `json:"ruleId"`
	Reason    string `json:"reason"`
	BlockedBy string `json:"blockedBy"`
}

type emconSummary struct {
	Mode     string `json:"mode"`
	SetBy    string `json:"setBy"`
	Locked   bool   `json:"locked"`
	LockedBy string `json:"lockedBy"`
}

type lockSummary struct {
	RuleID   string   `json:"ruleId"`
	Labels   []string `json:"labels"`
	LockedBy string   `json:"lockedBy"`
}

func (s *Server) currentPolicy() (*pb.Policy, bool) {
	if s.policy == nil || s.policy.guard == nil {
		return nil, false
	}
	return s.policy.guard.Active(time.Now())
}

func (s *Server) policySummary() policySummary {
	summary := policySummary{Admin: s.policy.admin, NodeID: s.policy.nodeID, EMCON: emconSummary{Mode: "NORMAL"}}
	p, _ := s.currentPolicy()
	if p == nil {
		return summary
	}
	summary.Version, summary.Conflicts = p.Version, conflictSummaries(p.Conflicts)
	summary.ExpiresAtUnixMs = p.ExpiresAtUnixMs
	if summary.ExpiresAtUnixMs == 0 {
		summary.ExpiresAtUnixMs = policy.Expiry(p, policy.DefaultTTL).UnixMilli()
	}
	_, emcon, _ := policy.Merge(p.Layers, s.policy.nodeID, time.Now())
	if emcon != nil {
		summary.EMCON.Mode = emconModeName(emcon.Mode)
		summary.EMCON.SetBy = emcon.SetBy
		summary.EMCON.Locked = emcon.Locked && emcon.Echelon > pb.Echelon_ECHELON_SHIP
		if summary.EMCON.Locked {
			summary.EMCON.LockedBy = emcon.SetBy
		}
	}
	for _, lock := range policy.Locks(p.Layers) {
		if lock.Emcon {
			continue
		}
		summary.Locks = append(summary.Locks, lockSummary{RuleID: lock.RuleID, Labels: append([]string(nil), lock.BoostLabels...), LockedBy: lock.LockedBy})
	}
	return summary
}

func emconModeName(mode pb.EmconMode) string {
	switch mode {
	case pb.EmconMode_EMCON_MODE_BURST:
		return "BURST"
	case pb.EmconMode_EMCON_MODE_SILENT:
		return "SILENT"
	default:
		return "NORMAL"
	}
}

func parseEMCON(mode string) (pb.EmconMode, bool) {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "NORMAL":
		return pb.EmconMode_EMCON_MODE_NORMAL, true
	case "BURST":
		return pb.EmconMode_EMCON_MODE_BURST, true
	case "SILENT":
		return pb.EmconMode_EMCON_MODE_SILENT, true
	default:
		return pb.EmconMode_EMCON_MODE_UNSPECIFIED, false
	}
}

func cloneEmcon(setting *pb.EmconSetting) *pb.EmconSetting {
	if setting == nil {
		return nil
	}
	return proto.Clone(setting).(*pb.EmconSetting)
}

func (s *Server) getPolicy(w http.ResponseWriter, _ *http.Request) {
	if s.policy.err != nil {
		writeError(w, http.StatusInternalServerError, s.policy.err.Error())
		return
	}
	p, _ := s.currentPolicy()
	raw, err := protojson.Marshal(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var current any
	if err := json.Unmarshal(raw, &current); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": s.policySummary(), "policy": current})
}

func (s *Server) policyAudit(w http.ResponseWriter, r *http.Request) {
	if s.policy.guard == nil {
		writeError(w, http.StatusInternalServerError, s.policy.err.Error())
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	if limit > 1000 {
		limit = 1000
	}
	entries, err := s.policy.guard.Audit(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) setPolicyEMCON(w http.ResponseWriter, r *http.Request) {
	if s.policy.guard == nil {
		writeError(w, http.StatusInternalServerError, s.policy.err.Error())
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	mode, ok := parseEMCON(body.Mode)
	if !ok {
		writeError(w, http.StatusBadRequest, "mode must be NORMAL, BURST, or SILENT")
		return
	}
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	now := time.Now()
	p, expired := s.policy.guard.Active(now)
	if p == nil {
		if expired {
			_ = s.policy.guard.MarkExpired(r.Context(), now)
		}
		var err error
		p, err = s.policy.apply("system", "CTF-72", "Refresh expired policy", now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	_, merged, _ := policy.Merge(p.Layers, s.policy.nodeID, now)
	if merged != nil && merged.Locked && merged.Echelon > pb.Echelon_ECHELON_SHIP {
		reason := "EMCON locked by " + merged.SetBy
		s.policy.guard.Reject(r.Context(), p, "ui", reason, now)
		writeJSON(w, http.StatusConflict, map[string]any{"error": reason, "conflicts": conflictSummaries([]*pb.MergeConflict{{RuleId: "emcon", Reason: reason, BlockedBy: "emcon"}})})
		return
	}
	s.policy.operatorEMCON = &pb.EmconSetting{Mode: mode, SetBy: opauth.Actor(r.Context(), "TAO"), Echelon: pb.Echelon_ECHELON_SHIP}
	accepted, err := s.policy.apply("ui", opauth.Actor(r.Context(), "TAO"), "EMCON "+emconModeName(mode), time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.publishSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{"summary": s.policySummary(), "conflicts": conflictSummaries(accepted.Conflicts)})
}

func (s *Server) validatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	p, err := decodeProtoPolicy(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	interest, emcon, conflicts := policy.Merge(p.Layers, s.policy.nodeID, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "conflicts": conflictSummaries(conflicts), "interest": map[string]any{"boost": interest.BoostLabels, "suppress": interest.SuppressLabels, "boostFactor": interest.BoostFactor, "boostWeights": interest.BoostWeights}, "emcon": emconModeName(func() pb.EmconMode {
		if emcon == nil {
			return pb.EmconMode_EMCON_MODE_NORMAL
		}
		return emcon.Mode
	}())})
}

func (s *Server) applyAdminPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.policy.guard == nil {
		writeError(w, http.StatusInternalServerError, s.policy.err.Error())
		return
	}
	p, err := decodeProtoPolicy(r.Body)
	if err != nil {
		s.policy.guard.Reject(r.Context(), nil, "admin", err.Error(), time.Now())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.IssuedBy = opauth.Actor(r.Context(), p.IssuedBy)
	now := time.Now()
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	prev := s.policy.higherLayers
	s.policy.higherLayers = nil
	for _, layer := range p.Layers {
		if layer.Echelon == pb.Echelon_ECHELON_SHIP && layer.IssuedBy == "TAO" {
			continue
		}
		s.policy.higherLayers = append(s.policy.higherLayers, proto.Clone(layer).(*pb.PolicyLayer))
	}
	accepted, err := s.policy.apply("admin", opauth.Actor(r.Context(), "super-admin"), fallback(p.Text, "raw policy edit"), now)
	if err != nil {
		s.policy.higherLayers = prev
		s.policy.guard.Reject(r.Context(), p, "admin", err.Error(), now)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.publishSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{"summary": s.policySummary(), "conflicts": conflictSummaries(accepted.Conflicts)})
}

func decodeProtoPolicy(reader io.Reader) (*pb.Policy, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 2<<20))
	if err != nil {
		return nil, err
	}
	p := &pb.Policy{}
	if err := protojson.Unmarshal(raw, p); err != nil {
		return nil, err
	}
	return p, nil
}

func fallback(value, alternate string) string {
	if strings.TrimSpace(value) == "" {
		return alternate
	}
	return value
}

func conflictSummaries(conflicts []*pb.MergeConflict) []conflictSummary {
	result := make([]conflictSummary, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict != nil {
			result = append(result, conflictSummary{RuleID: conflict.RuleId, Reason: conflict.Reason, BlockedBy: conflict.BlockedBy})
		}
	}
	return result
}

// requireAdmin gates the raw policy editor: -admin must be on and the request
// must re-present the operator bearer token (the session cookie alone is not
// enough for a privileged, policy-replacing edit).
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.policy.admin {
		writeError(w, http.StatusForbidden, "super admin only (start with -admin or GS_ADMIN=1)")
		return false
	}
	if s.policy.adminToken == "" || !opauth.Equal(s.policy.adminToken, opauth.BearerToken(r)) {
		opauth.Unauthorized(w)
		return false
	}
	return true
}
