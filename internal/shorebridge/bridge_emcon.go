package shorebridge

import (
	"encoding/json"
	"fmt"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"net/http"
	"strings"
	"time"
)

// cachedEmcon is the EMCON label of the last snapshot the poller built ("" if none).
func (b *Bridge) cachedEmcon() string {
	b.mu.Lock()
	raw := b.snap
	b.mu.Unlock()
	var snap struct {
		Emcon string `json:"emcon"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &snap) != nil {
		return ""
	}
	return snap.Emcon
}

func (b *Bridge) setEMCON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	mode, ok := parseEMCON(in.Mode)
	if !ok {
		http.Error(w, "mode must be NORMAL, BURST, or SILENT", http.StatusBadRequest)
		return
	}
	issuedBy := opauth.Actor(r.Context(), b.issuer())
	current, err := b.getPolicy(r.Context())
	if err != nil {
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if current != nil {
		current = policy.Flatten(current, b.currentEdgeID(), time.Now())
	}
	if current == nil {
		current = &pb.Policy{Interest: &pb.Interest{}}
	}
	if setting := current.EmconSetting; setting != nil && setting.Locked && setting.Echelon > b.echelon() {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]string{"error": "EMCON locked by " + setting.SetBy})
		return
	}
	pol := proto.Clone(current).(*pb.Policy)
	pol.IssuedBy = issuedBy
	if len(pol.Layers) > 0 {
		pol.Layers = cloneLayers(pol.Layers)
		operatorLayer := findOperatorLayer(pol.Layers, b.issuer(), b.echelon())
		if operatorLayer < 0 {
			pol.Layers = append(pol.Layers, &pb.PolicyLayer{Echelon: b.echelon(), IssuedBy: b.issuer()})
			operatorLayer = len(pol.Layers) - 1
		}
		pol.Layers[operatorLayer].Rules = withoutEmconOnlyRules(pol.Layers[operatorLayer].Rules)
		pol.Layers[operatorLayer].Emcon = &pb.EmconSetting{Mode: mode, SetBy: issuedBy, Echelon: b.echelon()}
	} else {
		rules := make([]*pb.InterestRule, 0, 2)
		if pol.Interest != nil && len(pol.Interest.BoostLabels) > 0 {
			var weight float64
			if pol.Interest.BoostFactor > 0 {
				weight = pol.Interest.BoostFactor
			}
			rules = append(rules, &pb.InterestRule{Id: fmt.Sprintf("ui-%d-boost", time.Now().UnixMilli()), Author: issuedBy,
				BoostLabels: append([]string(nil), pol.Interest.BoostLabels...), Weight: weight})
		}
		if pol.Interest != nil && len(pol.Interest.SuppressLabels) > 0 {
			rules = append(rules, &pb.InterestRule{Id: fmt.Sprintf("ui-%d-suppress", time.Now().UnixMilli()), Author: issuedBy,
				SuppressLabels: append([]string(nil), pol.Interest.SuppressLabels...)})
		}
		pol.Layers = []*pb.PolicyLayer{{
			Echelon: b.echelon(), IssuedBy: b.issuer(), Rules: rules,
			Emcon: &pb.EmconSetting{Mode: mode, SetBy: issuedBy, Echelon: b.echelon()},
		}}
	}
	pol.Id, pol.Version, pol.Signature, pol.Conflicts, pol.IssuedAtUnixMs = "", 0, nil, nil, 0
	var out pb.SetPolicyResponse
	if err := b.call(r.Context(), b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	raw, _ := protojson.Marshal(&out)
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func parseEMCON(value string) (pb.EmconMode, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
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

func emconName(mode pb.EmconMode) string {
	switch mode {
	case pb.EmconMode_EMCON_MODE_BURST:
		return "BURST"
	case pb.EmconMode_EMCON_MODE_SILENT:
		return "SILENT"
	default:
		return "NORMAL"
	}
}
