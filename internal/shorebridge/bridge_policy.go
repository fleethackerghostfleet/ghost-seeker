package shorebridge

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type policyBody struct {
	Text     string   `json:"text"`
	Suppress []string `json:"suppress"`
	Boost    []string `json:"boost"`
}

// setPolicy forwards an operator directive to shore. A free-text directive is
// compiled with the NL compiler when one is configured (readback, warnings and
// path "llm"|"rules" are returned alongside the policy), otherwise parsed with
// Interpret (e.g. "ignore cargo ships" -> suppress cargo ship).
func (b *Bridge) setPolicy(w http.ResponseWriter, r *http.Request) {
	var in policyBody
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: policy request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(in.Text) > 500 {
		http.Error(w, "text exceeds 500 characters", http.StatusBadRequest)
		return
	}
	issuedBy := opauth.Actor(r.Context(), b.issuer())
	mode, ruleText := command.SplitEmcon(in.Text)
	pol := &pb.Policy{IssuedBy: issuedBy, Text: in.Text, Interest: &pb.Interest{SuppressLabels: in.Suppress, BoostLabels: in.Boost}}
	durationMin := command.ExtractDurationMin(in.Text)
	if mode != "" {
		pol.Emcon = pb.EmconMode(pb.EmconMode_value["EMCON_MODE_"+mode])
	}
	var compiled *command.Compiled
	if len(in.Suppress) == 0 && len(in.Boost) == 0 && in.Text != "" {
		droppedScopedRules := false
		if b.Compiler != nil {
			c := b.Compiler.Compile(r.Context(), ruleText)
			if c.Blocked {
				http.Error(w, blockedMessage(c), http.StatusUnprocessableEntity)
				return
			}
			if durationMin == 0 {
				durationMin = c.DurationMin
			}
			if c.DurationMin == 0 && durationMin > 0 {
				c.DurationMin = durationMin
			}
			kept := make([]command.Rule, 0, len(c.Rules))
			for _, rule := range c.Rules {
				if len(rule.Sources) == 0 && len(rule.Attrs) == 0 && rule.WithinMin == 0 {
					kept = append(kept, rule)
				} else {
					droppedScopedRules = true
				}
			}
			if droppedScopedRules {
				priorWarnings := c.Warnings
				path := c.Path
				c = b.Compiler.Review(ruleText, kept, c.DurationMin)
				c.Path = path
				c.Warnings = mergeWarnings(priorWarnings, c.Warnings, []string{
					"Not applied in shore mode: source, attribute and time scopes are not supported by the edge scorer.",
				})
				if c.Blocked {
					http.Error(w, blockedMessage(c), http.StatusUnprocessableEntity)
					return
				}
			}
			pol.Interest.SuppressLabels, pol.Interest.BoostLabels = rulesToLabels(c.Rules)
			compiled = &c
		}
		if len(pol.Interest.SuppressLabels) == 0 && len(pol.Interest.BoostLabels) == 0 {
			if !droppedScopedRules && pol.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
				pol.Interest.SuppressLabels, pol.Interest.BoostLabels = Interpret(ruleText)
				compiled = nil
			}
		}
	}
	if durationMin > 0 {
		pol.ExpiresAtUnixMs = time.Now().Add(time.Duration(durationMin) * time.Minute).UnixMilli()
	}
	directiveSuppress, directiveBoost := pol.Interest.SuppressLabels, pol.Interest.BoostLabels
	warnings, err := b.layerPolicy(r.Context(), pol, mode, true)
	if err != nil {
		log.Printf("shorebridge: set policy: %v", err)
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	if compiled != nil {
		compiled.Warnings = append(compiled.Warnings, warnings...)
		compiled.Readback = emconReadback(compiled.Readback, mode, durationMin)
	}
	pol.Id, pol.Version, pol.Signature, pol.Conflicts, pol.IssuedAtUnixMs = "", 0, nil, nil, 0
	var out pb.SetPolicyResponse
	if err := b.call(r.Context(), b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		log.Printf("shorebridge: set policy upstream: %v", err)
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	raw, _ := protojson.Marshal(&out)
	var policy map[string]any
	if json.Unmarshal(raw, &policy) != nil {
		policy = map[string]any{}
	}
	readback := Readback(directiveSuppress, directiveBoost)
	if compiled != nil && compiled.Readback != "" {
		readback = compiled.Readback
	}
	if rb, ok := policy["readback"].(string); ok && rb != "" { // a shore that compiles directives itself wins
		readback = rb
	}
	readback = emconReadback(readback, mode, durationMin)
	resp := struct {
		Policy   json.RawMessage   `json:"policy"`
		Compiled *command.Compiled `json:"compiled,omitempty"`
		Readback string            `json:"readback"`
	}{Policy: raw, Compiled: compiled, Readback: readback}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func emconReadback(readback, mode string, durationMin int) string {
	if mode == "" {
		return readback
	}
	phrase := "EMCON " + mode
	if durationMin > 0 {
		phrase += fmt.Sprintf(" for %d min", durationMin)
	}
	phrase += "."
	if strings.Contains(readback, phrase) {
		return readback
	}
	readback = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(readback), "No rules recognized."))
	if readback == "" {
		return phrase
	}
	return readback + " " + phrase
}

// rulesToLabels flattens compiled rules into the flat boost/suppress label
// lists the edge scorer matches against (classes only; attributes have no
// edge-side equivalent yet).
func rulesToLabels(rules []command.Rule) (suppress, boost []string) {
	seen := map[string]bool{}
	for _, rule := range rules {
		for _, class := range transportLabels(rule.Classes) {
			key := rule.Type + "/" + class
			if class == "" || seen[key] {
				continue
			}
			seen[key] = true
			switch rule.Type {
			case "suppress":
				suppress = append(suppress, class)
			case "boost":
				boost = append(boost, class)
			}
		}
	}
	return suppress, boost
}

func mergeWarnings(groups ...[]string) []string {
	merged := make([]string, 0)
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, warning := range group {
			if !seen[warning] {
				seen[warning] = true
				merged = append(merged, warning)
			}
		}
	}
	return merged
}

func blockedMessage(c command.Compiled) string {
	for _, warning := range c.Warnings {
		if strings.HasPrefix(warning, "Blocked") {
			return warning
		}
	}
	if len(c.Warnings) > 0 {
		return c.Warnings[0]
	}
	return "directive is blocked"
}

// applyPolicy replaces the standing shore policy with the given interest and
// asks the poller to refresh so the console sees the change at once.
func (b *Bridge) applyPolicy(ctx context.Context, text string, suppress, boost []string) (*pb.SetPolicyResponse, error) {
	return b.applyPolicyAs(ctx, opauth.Actor(ctx, b.issuer()), text, suppress, boost)
}

func (b *Bridge) applyPolicyAs(ctx context.Context, issuedBy, text string, suppress, boost []string) (*pb.SetPolicyResponse, error) {
	if issuedBy == "" {
		issuedBy = opauth.Actor(ctx, b.issuer())
	}
	if suppress == nil {
		suppress = []string{}
	}
	if boost == nil {
		boost = []string{}
	}
	return b.applyPolicyProto(ctx, &pb.Policy{IssuedBy: issuedBy, Text: text, Interest: &pb.Interest{SuppressLabels: suppress, BoostLabels: boost}})
}

// applyPolicyProto sends a fully built policy (interest, EMCON, expiry) to the
// shore and pokes the poller.
func (b *Bridge) applyPolicyProto(ctx context.Context, pol *pb.Policy) (*pb.SetPolicyResponse, error) {
	mode := ""
	if pol.Emcon != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		mode = emconName(pol.Emcon)
	}
	if _, err := b.layerPolicy(ctx, pol, mode, true); err != nil {
		return nil, err
	}
	var out pb.SetPolicyResponse
	if err := b.call(ctx, b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		return nil, err
	}
	b.Poke()
	return &out, nil
}

func (b *Bridge) currentEdgeID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.edgeNodeID
}

func (b *Bridge) getPolicy(ctx context.Context) (*pb.Policy, error) {
	var response pb.GetPolicyResponse
	if err := b.call(ctx, b.shore, "GetPolicy", &pb.GetPolicyRequest{}, &response); err != nil {
		return nil, err
	}
	return response.Policy, nil
}

func findOperatorLayer(layers []*pb.PolicyLayer, issuedBy string, echelon pb.Echelon) int {
	for i, layer := range layers {
		if layer != nil && layer.Echelon == echelon && layer.IssuedBy == issuedBy {
			return i
		}
	}
	return -1
}

func cloneLayers(layers []*pb.PolicyLayer) []*pb.PolicyLayer {
	out := make([]*pb.PolicyLayer, 0, len(layers))
	for _, layer := range layers {
		if layer == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, proto.Clone(layer).(*pb.PolicyLayer))
	}
	return out
}

func policyInfo(p *pb.Policy, operatorEchelon ...pb.Echelon) *PolicyInfo {
	echelon := pb.Echelon_ECHELON_SHIP
	if len(operatorEchelon) > 0 {
		echelon = operatorEchelon[0]
	}
	info := &PolicyInfo{Version: p.Version, Emcon: emconName(p.Emcon), Locks: []PolicyLock{}, Conflicts: []PolicyConflict{}}
	if p.EmconSetting != nil {
		info.EmconSetBy = p.EmconSetting.SetBy
		if p.EmconSetting.Mode != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
			info.Emcon = emconName(p.EmconSetting.Mode)
		}
		if p.EmconSetting.Locked && p.EmconSetting.Echelon > echelon {
			info.EmconLocked = true
			info.EmconLockedBy = p.EmconSetting.SetBy
		}
	}
	for _, lock := range policy.Locks(p.Layers) {
		if !lock.Emcon {
			info.Locks = append(info.Locks, PolicyLock{RuleID: lock.RuleID, Labels: append([]string(nil), lock.BoostLabels...), LockedBy: lock.LockedBy})
		}
	}
	for _, conflict := range p.Conflicts {
		if conflict != nil {
			info.Conflicts = append(info.Conflicts, PolicyConflict{RuleID: conflict.RuleId, Reason: conflict.Reason, BlockedBy: conflict.BlockedBy})
		}
	}
	return info
}
