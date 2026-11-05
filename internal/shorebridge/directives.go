package shorebridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/proto"
)

var errDirectiveNotFound = errors.New("directive not found")

// layerPolicy folds a freshly built operator directive into the current shore
// policy. The result is always layered (#57): higher-echelon layers, locks and
// signatures survive, an EMCON phrase changes the caller-echelon EMCON unless a
// higher echelon locked it, and every directive is one InterestRule in the
// operator's layer carrying its own text and expiry, so a timed directive
// lapses on its own instead of re-timing the whole policy (which sent the edge
// back to its default policy and dropped every other directive with it).
//
// With replace the directive replaces the operator's standing rules (the
// /api/shore/policy strip). Otherwise it is added next to them (console
// POST /api/directives): a label the new directive boosts or suppresses is
// taken out of the operator's older rules (newest wins) and older rules left
// without labels are dropped. Returns warnings for the readback.
func (b *Bridge) layerPolicy(ctx context.Context, pol *pb.Policy, mode string, replace bool) (warnings []string, err error) {
	pol.IssuedBy = b.issuer()
	echelon := b.echelon()
	now := time.Now()
	ruleExpiryUnixMs := pol.GetExpiresAtUnixMs()
	pol.ExpiresAtUnixMs = 0
	if pol.Interest == nil {
		pol.Interest = &pb.Interest{}
	}
	current, err := b.getPolicy(ctx)
	if err != nil {
		// Shore policy unreadable: don't apply blind. Keep the last EMCON the
		// poller saw so an ordinary directive cannot reset BURST/SILENT to NORMAL.
		warnings = append(warnings, "Current shore policy could not be read; higher-echelon layers were not merged.")
		if mode == "" {
			if cached, ok := parseEMCON(b.cachedEmcon()); ok {
				pol.Emcon = cached
			}
		}
		current = nil
	} else {
		current = activePolicy(current, b.currentEdgeID(), now)
	}
	emconLocked := false
	if current != nil {
		emconLocked = current.EmconSetting != nil && current.EmconSetting.Locked && current.EmconSetting.Echelon > echelon
		if mode == "" || emconLocked {
			if mode != "" {
				warnings = append(warnings, "EMCON locked by "+current.EmconSetting.SetBy+"; not changed.")
			}
			pol.Emcon = current.Emcon
			if current.EmconSetting != nil {
				pol.EmconSetting = proto.Clone(current.EmconSetting).(*pb.EmconSetting)
			}
		}
		pol.Layers = cloneLayers(current.Layers)
		if len(pol.Layers) == 0 && !replace {
			pol.Layers = wrapFlatPolicy(current, pol.IssuedBy, echelon)
		}
	}
	operatorLayer := findOperatorLayer(pol.Layers, pol.IssuedBy, echelon)
	if operatorLayer < 0 {
		pol.Layers = append(pol.Layers, &pb.PolicyLayer{Echelon: echelon, IssuedBy: pol.IssuedBy})
		operatorLayer = len(pol.Layers) - 1
	}
	layer := pol.Layers[operatorLayer]
	// Labels another layer already boosts/suppresses (same polarity) are
	// inherited, not repeated in the caller rule; an operator suppressing a
	// label another layer boosts is a real override and stays.
	inheritedBoost, inheritedSuppress := inheritedLabels(pol.Layers, operatorLayer)
	rule := &pb.InterestRule{Id: newRuleID(layer.Rules, now), Author: pol.IssuedBy,
		BoostLabels:    ownLabels(pol.Interest.BoostLabels, inheritedBoost),
		SuppressLabels: ownLabels(pol.Interest.SuppressLabels, inheritedSuppress),
		Text:           strings.TrimSpace(pol.Text), ExpiresAtUnixMs: ruleExpiryUnixMs}
	if replace {
		layer.Rules = []*pb.InterestRule{rule}
	} else {
		layer.Rules = append(newestWins(layer.Rules, rule, now, mode != "" && !emconLocked), rule)
	}
	if mode != "" && !emconLocked {
		layer.Emcon = &pb.EmconSetting{Mode: pol.Emcon, SetBy: pol.IssuedBy, Echelon: echelon, ExpiresAtUnixMs: ruleExpiryUnixMs}
		pol.EmconSetting = proto.Clone(layer.Emcon).(*pb.EmconSetting)
	}
	pol.Text = layerText(layer, now)
	pol.Id, pol.Version, pol.Signature, pol.Conflicts, pol.IssuedAtUnixMs = "", 0, nil, nil, 0
	return warnings, nil
}

// addDirective appends one directive to the operator's layer and returns the
// shore's response plus the id of the rule that now carries it.
func (b *Bridge) addDirective(ctx context.Context, pol *pb.Policy) (*pb.SetPolicyResponse, string, error) {
	mode := ""
	if pol.Emcon != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		mode = emconName(pol.Emcon)
	}
	if _, err := b.layerPolicy(ctx, pol, mode, false); err != nil {
		return nil, "", err
	}
	ruleID := ""
	if i := findOperatorLayer(pol.Layers, pol.IssuedBy, b.echelon()); i >= 0 {
		if rules := pol.Layers[i].Rules; len(rules) > 0 {
			ruleID = rules[len(rules)-1].Id
		}
	}
	var out pb.SetPolicyResponse
	if err := b.call(ctx, b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		return nil, "", err
	}
	b.Poke()
	return &out, ruleID, nil
}

// removeDirectives drops the operator's own rules (all of them when ids is
// empty) from the current shore policy. Removing the directive the current
// EMCON setting came from (emconOwner) takes the operator's EMCON back to
// NORMAL with it; other operators' layers, higher-echelon layers and locks
// are untouched, as is an EMCON chosen from the EMCON control or a newer
// EMCON directive.
func (b *Bridge) removeDirectives(ctx context.Context, ids []string) (*pb.SetPolicyResponse, error) {
	current, err := b.getPolicy(ctx)
	if err != nil {
		return nil, err
	}
	issuedBy, echelon, now := b.issuer(), b.echelon(), time.Now()
	if current == nil || len(current.Layers) == 0 {
		// Legacy flat policy: the console listed it as the single "policy" entry.
		for _, id := range ids {
			if id != flatDirectiveID {
				return nil, errDirectiveNotFound
			}
		}
		return b.applyPolicyAs(ctx, issuedBy, "", nil, nil)
	}
	pol := proto.Clone(current).(*pb.Policy)
	pol.IssuedBy = issuedBy
	pol.Layers = cloneLayers(pol.Layers)
	index := findOperatorLayer(pol.Layers, issuedBy, echelon)
	if index < 0 {
		if len(ids) > 0 {
			return nil, errDirectiveNotFound
		}
		pol.Layers = append(pol.Layers, &pb.PolicyLayer{Echelon: echelon, IssuedBy: issuedBy})
		index = len(pol.Layers) - 1
	}
	layer := pol.Layers[index]
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	kept := make([]*pb.InterestRule, 0, len(layer.Rules))
	removed, resetEmcon := 0, false
	owner := emconOwner(layer)
	for _, rule := range layer.Rules {
		if rule == nil {
			continue
		}
		if len(ids) == 0 || want[rule.Id] {
			removed++
			if rule == owner {
				resetEmcon = true
			}
			continue
		}
		kept = append(kept, rule)
	}
	if len(ids) > 0 && removed == 0 {
		return nil, errDirectiveNotFound
	}
	layer.Rules = kept
	if resetEmcon && layer.Emcon != nil {
		layer.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_NORMAL, SetBy: issuedBy, Echelon: echelon}
	}
	pol.Text = layerText(layer, now)
	flat := policy.Flatten(pol, b.currentEdgeID(), now)
	pol.Interest, pol.Emcon, pol.EmconSetting = flat.Interest, flat.Emcon, flat.EmconSetting
	pol.Id, pol.Version, pol.Signature, pol.Conflicts, pol.IssuedAtUnixMs = "", 0, nil, nil, 0
	var out pb.SetPolicyResponse
	if err := b.call(ctx, b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		return nil, err
	}
	b.Poke()
	return &out, nil
}

// flatDirectiveID is the id the console lists a layer-less (legacy) policy under.
const flatDirectiveID = "policy"

// activePolicy is the stored shore policy as it applies now: layers merged
// with expired rules and EMCON dropped, and a flat policy whose deadline
// passed treated as cleared (the edge is back on its default policy by then).
func activePolicy(p *pb.Policy, nodeID string, now time.Time) *pb.Policy {
	if p == nil {
		return nil
	}
	if len(p.Layers) == 0 && policyExpired(p, now) {
		return clearedPolicy()
	}
	return policy.Flatten(p, nodeID, now)
}

// viewPolicy is what the shore console shows for the stored policy: like
// activePolicy, but a layered policy whose whole-policy deadline passed is
// also shown as cleared because the edge reverts to its default then.
func viewPolicy(p *pb.Policy, nodeID string, now time.Time) *pb.Policy {
	if p != nil && policyExpired(p, now) {
		return clearedPolicy()
	}
	return activePolicy(p, nodeID, now)
}

func clearedPolicy() *pb.Policy {
	return &pb.Policy{Id: "default", Emcon: pb.EmconMode_EMCON_MODE_NORMAL, Interest: &pb.Interest{}}
}

func policyExpired(p *pb.Policy, now time.Time) bool {
	return p.GetExpiresAtUnixMs() > 0 && now.UnixMilli() >= p.GetExpiresAtUnixMs()
}

// nextPolicyExpiry is the earliest future deadline in p (whole policy, any
// rule, any layer EMCON), or zero when nothing is timed.
func nextPolicyExpiry(p *pb.Policy, now time.Time) time.Time {
	var next time.Time
	consider := func(unixMs int64) {
		if unixMs <= 0 {
			return
		}
		at := time.UnixMilli(unixMs)
		if at.After(now) && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	if p == nil {
		return next
	}
	consider(p.ExpiresAtUnixMs)
	for _, layer := range p.Layers {
		if layer == nil {
			continue
		}
		consider(layer.GetEmcon().GetExpiresAtUnixMs())
		for _, rule := range layer.Rules {
			consider(rule.GetExpiresAtUnixMs())
		}
	}
	return next
}

// wrapFlatPolicy turns a layer-less policy's standing interest and EMCON into
// the operator's layer so a new directive can sit next to them (the flat
// deadline, if any, becomes theirs).
func wrapFlatPolicy(current *pb.Policy, issuedBy string, echelon pb.Echelon) []*pb.PolicyLayer {
	layer := &pb.PolicyLayer{Echelon: echelon, IssuedBy: issuedBy}
	interest := current.GetInterest()
	if len(interest.GetBoostLabels())+len(interest.GetSuppressLabels()) > 0 {
		layer.Rules = []*pb.InterestRule{{Id: "ui-standing", Author: issuedBy, Text: current.GetText(),
			BoostLabels:    append([]string(nil), interest.GetBoostLabels()...),
			SuppressLabels: append([]string(nil), interest.GetSuppressLabels()...),
			Weight:         interest.GetBoostFactor(), ExpiresAtUnixMs: current.GetExpiresAtUnixMs()}}
	}
	if setting := current.GetEmconSetting(); setting != nil && setting.Mode != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		layer.Emcon = proto.Clone(setting).(*pb.EmconSetting)
	} else if current.GetEmcon() != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		layer.Emcon = &pb.EmconSetting{Mode: current.GetEmcon(), SetBy: current.GetIssuedBy()}
	}
	if layer.Emcon != nil {
		if layer.Emcon.ExpiresAtUnixMs == 0 {
			layer.Emcon.ExpiresAtUnixMs = current.GetExpiresAtUnixMs()
		}
		if layer.Emcon.Echelon == pb.Echelon_ECHELON_UNSPECIFIED {
			layer.Emcon.Echelon = echelon
		}
		if layer.Emcon.SetBy == "" {
			layer.Emcon.SetBy = issuedBy
		}
	}
	return []*pb.PolicyLayer{layer}
}

func inheritedLabels(layers []*pb.PolicyLayer, skip int) (boost, suppress map[string]bool) {
	boost, suppress = map[string]bool{}, map[string]bool{}
	for i, layer := range layers {
		if i == skip || layer == nil {
			continue
		}
		for _, r := range layer.GetRules() {
			for _, l := range r.GetBoostLabels() {
				boost[l] = true
			}
			for _, l := range r.GetSuppressLabels() {
				suppress[l] = true
			}
		}
	}
	return boost, suppress
}

func ownLabels(labels []string, inherited map[string]bool) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if !inherited[l] {
			out = append(out, l)
		}
	}
	return out
}

func newRuleID(existing []*pb.InterestRule, now time.Time) string {
	base := fmt.Sprintf("ui-%d", now.UnixMilli())
	id := base
	for n := 1; ; n++ {
		taken := false
		for _, rule := range existing {
			if rule != nil && rule.Id == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// newestWins returns the operator's older rules with the labels the newest
// directive names removed (whichever polarity they had) and with expired
// rules pruned; a rule left without labels by the trim is dropped, a rule
// that never had labels (an EMCON-only directive) is kept unless the newest
// directive sets EMCON itself (setsEmcon), which supersedes it.
func newestWins(rules []*pb.InterestRule, newest *pb.InterestRule, now time.Time, setsEmcon bool) []*pb.InterestRule {
	trim := map[string]bool{}
	for _, l := range append(append([]string(nil), newest.BoostLabels...), newest.SuppressLabels...) {
		trim[normLabel(l)] = true
	}
	out := make([]*pb.InterestRule, 0, len(rules))
	for _, old := range rules {
		if old == nil || (old.ExpiresAtUnixMs > 0 && now.UnixMilli() >= old.ExpiresAtUnixMs) {
			continue
		}
		if setsEmcon && emconOnlyRule(old) {
			continue
		}
		had := len(old.BoostLabels) + len(old.SuppressLabels)
		old.BoostLabels = withoutLabels(old.BoostLabels, trim)
		old.SuppressLabels = withoutLabels(old.SuppressLabels, trim)
		if had > 0 && len(old.BoostLabels)+len(old.SuppressLabels) == 0 {
			continue
		}
		out = append(out, old)
	}
	return out
}

// emconOnlyRule is a directive that only set the transmit posture ("go quiet
// for 1 minute"): no labels, an EMCON phrase in its words.
func emconOnlyRule(rule *pb.InterestRule) bool {
	if rule == nil || len(rule.BoostLabels)+len(rule.SuppressLabels) > 0 {
		return false
	}
	mode, _ := command.SplitEmcon(rule.Text)
	return mode != ""
}

// emconOwner is the layer rule whose EMCON phrase the layer's current EMCON
// setting came from: the newest rule naming that mode with the same deadline.
// A later EMCON directive or a choice on the EMCON control (no deadline, or
// another mode) leaves older EMCON directives without ownership.
func emconOwner(layer *pb.PolicyLayer) *pb.InterestRule {
	setting := layer.GetEmcon()
	if setting == nil || setting.Mode == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		return nil
	}
	for i := len(layer.Rules) - 1; i >= 0; i-- {
		rule := layer.Rules[i]
		if rule == nil {
			continue
		}
		if mode, _ := command.SplitEmcon(rule.Text); mode == "" {
			continue
		} else if mode == emconName(setting.Mode) && rule.ExpiresAtUnixMs == setting.ExpiresAtUnixMs {
			return rule
		}
		return nil
	}
	return nil
}

// withoutEmconOnlyRules drops the EMCON-only directives from a layer's rules,
// for when the EMCON control replaces the posture they asked for.
func withoutEmconOnlyRules(rules []*pb.InterestRule) []*pb.InterestRule {
	out := make([]*pb.InterestRule, 0, len(rules))
	for _, rule := range rules {
		if rule != nil && !emconOnlyRule(rule) {
			out = append(out, rule)
		}
	}
	return out
}

func withoutLabels(labels []string, drop map[string]bool) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if !drop[normLabel(l)] {
			out = append(out, l)
		}
	}
	return out
}

func normLabel(l string) string { return strings.ToLower(strings.TrimSpace(l)) }

// layerText is the operator's standing directive text: the words of each
// unexpired rule in the layer, oldest first.
func layerText(layer *pb.PolicyLayer, now time.Time) string {
	var parts []string
	for _, rule := range layer.GetRules() {
		if rule == nil || (rule.ExpiresAtUnixMs > 0 && now.UnixMilli() >= rule.ExpiresAtUnixMs) {
			continue
		}
		if text := strings.TrimSpace(rule.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "; ")
}

// directives lists the policy as the console shows it: one entry per rule the
// merge accepts (labels as trimmed by higher echelons), oldest first within a
// layer, plus the operator's own rules a higher-echelon lock blocked. Entries
// from other layers carry Source (and Locked) so the UI shows them as
// delegated and offers no remove button.
func (b *Bridge) directives(pol *pb.Policy, now time.Time) []command.Directive {
	out := []command.Directive{}
	if pol == nil {
		return out
	}
	if len(pol.Layers) == 0 {
		rules := policyRules(flatDirectiveID, pol.GetInterest().GetSuppressLabels(), pol.GetInterest().GetBoostLabels())
		if len(rules) == 0 {
			return out
		}
		text := strings.TrimSpace(pol.GetText())
		if text == "" {
			text = Readback(pol.GetInterest().GetSuppressLabels(), pol.GetInterest().GetBoostLabels())
		}
		return append(out, command.Directive{ID: flatDirectiveID, Text: text, Rules: rules, ExpiresAtUnixMs: pol.GetExpiresAtUnixMs()})
	}
	issuedBy, echelon := b.issuer(), b.echelon()
	accepted, _, conflicts := policy.MergeRules(pol.Layers, b.currentEdgeID(), now)
	blocked := map[string]string{}
	for _, conflict := range conflicts {
		if conflict != nil && conflict.RuleId != "emcon" {
			blocked[conflict.RuleId] = conflict.Reason
		}
	}
	entry := func(layer *pb.PolicyLayer, rule *pb.InterestRule) (command.Directive, bool) {
		own := layer.Echelon == echelon && layer.IssuedBy == issuedBy
		rules := policyRules(rule.Id, rule.SuppressLabels, rule.BoostLabels)
		if len(rules) == 0 && !(own && emconOwner(layer) != nil && emconOwner(layer).Id == rule.Id) {
			return command.Directive{}, false
		}
		text := strings.TrimSpace(rule.Text)
		if text == "" {
			text = strings.TrimSuffix(Readback(rule.SuppressLabels, rule.BoostLabels), " Until cleared.")
		}
		d := command.Directive{ID: rule.Id, Text: text, Rules: rules, ExpiresAtUnixMs: rule.ExpiresAtUnixMs}
		if !own {
			d.Source = layer.IssuedBy
			if d.Source == "" {
				d.Source = strings.TrimPrefix(layer.Echelon.String(), "ECHELON_")
			}
			d.Locked = rule.Locked
			if rule.Locked {
				d.LockedBy = d.Source
			}
		}
		return d, true
	}
	// Keep the stored order (higher echelons first, then each layer oldest first).
	for _, layer := range sortedLayersHighFirst(pol.Layers) {
		for _, rule := range layer.Rules {
			if rule == nil {
				continue
			}
			if reason, isBlocked := blocked[rule.Id]; isBlocked {
				if d, ok := entry(layer, rule); ok {
					d.Blocked = reason
					out = append(out, d)
				}
				continue
			}
			for _, a := range accepted {
				if a.Rule.Id == rule.Id && a.Layer.Echelon == layer.Echelon && a.Layer.IssuedBy == layer.IssuedBy {
					if d, ok := entry(layer, a.Rule); ok {
						out = append(out, d)
					}
					break
				}
			}
		}
	}
	return out
}

func sortedLayersHighFirst(layers []*pb.PolicyLayer) []*pb.PolicyLayer {
	out := make([]*pb.PolicyLayer, 0, len(layers))
	for _, layer := range layers {
		if layer != nil {
			out = append(out, layer)
		}
	}
	for i := 1; i < len(out); i++ { // stable insertion sort by echelon, high first
		for j := i; j > 0 && out[j].Echelon > out[j-1].Echelon; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func policyRules(directiveID string, suppress, boost []string) []command.Rule {
	rules := []command.Rule{}
	for _, l := range dedupe(boost) {
		rules = append(rules, command.Rule{Type: "boost", Classes: []string{l}, Attrs: []string{}, DirectiveID: directiveID})
	}
	for _, l := range dedupe(suppress) {
		rules = append(rules, command.Rule{Type: "suppress", Classes: []string{l}, Attrs: []string{}, DirectiveID: directiveID})
	}
	return rules
}

// directivesText joins the active directives' words, oldest first.
func directivesText(ds []command.Directive) string {
	var parts []string
	for _, d := range ds {
		if d.Blocked == "" && d.Text != "" {
			parts = append(parts, d.Text)
		}
	}
	return strings.Join(parts, "; ")
}

// directivesReadback reads the active directives back grouped by deadline, so
// each rule says when it lapses: "Deprioritize (not delete): cargo ship. Until
// 05:47Z. Boost: submarine. Until cleared."
func directivesReadback(ds []command.Directive) string {
	type group struct{ suppress, boost []string }
	var order []int64
	groups := map[int64]*group{}
	for _, d := range ds {
		if d.Blocked != "" {
			continue
		}
		for _, r := range d.Rules {
			g := groups[d.ExpiresAtUnixMs]
			if g == nil {
				g = &group{}
				groups[d.ExpiresAtUnixMs] = g
				order = append(order, d.ExpiresAtUnixMs)
			}
			switch r.Type {
			case "suppress":
				g.suppress = append(g.suppress, r.Classes...)
			case "boost":
				g.boost = append(g.boost, r.Classes...)
			}
		}
	}
	if len(order) == 0 {
		return Readback(nil, nil)
	}
	parts := make([]string, 0, len(order))
	for _, at := range order {
		g := groups[at]
		rb := Readback(g.suppress, g.boost)
		if at > 0 {
			rb = strings.Replace(rb, "Until cleared.", "Until "+time.UnixMilli(at).UTC().Format("15:04Z")+".", 1)
		}
		parts = append(parts, rb)
	}
	return strings.Join(parts, " ")
}
