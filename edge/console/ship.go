package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ShipRule is one ship-operator directive from the ship console (ui/index.html).
type ShipRule struct {
	Boost     []string `json:"boost,omitempty"`
	Suppress  []string `json:"suppress,omitempty"`
	Weight    float64  `json:"weight,omitempty"`
	Text      string   `json:"text,omitempty"`
	ExpiresAt int64    `json:"expiresAtUnixMs,omitempty"`
}

// ShipPolicy is the whole ship-console layer: POST replaces it.
type ShipPolicy struct {
	By    string     `json:"by,omitempty"`
	Rules []ShipRule `json:"rules"`
	Emcon string     `json:"emcon,omitempty"` // "", NORMAL, BURST, SILENT
}

const maxShipRules = 32

// ShipPolicyHandler serves GET/POST /ship/policy: the ship operator's own
// boost/suppress rules and EMCON, kept as a SHIP-echelon layer on top of
// whatever the shore sent (higher-echelon locks still win in the merge).
func (s *Service) ShipPolicyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
		case http.MethodPost:
			var req ShipPolicy
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
				http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
				return
			}
			layer, err := s.shipLayer(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			raw := s.Sch.RawPolicy()
			base := raw
			if base == nil {
				base = s.Sch.Policy()
			}
			p := policy.WithLocalLayer(base, layer)
			if raw != nil && raw.ExpiresAtUnixMs == 0 && !strings.HasPrefix(raw.Id, "ship-") {
				// AcceptLocal re-stamps IssuedAt; keep the shore policy's own deadline.
				p.ExpiresAtUnixMs = policy.Expiry(raw, policy.DefaultTTL).UnixMilli()
			}
			p.Id = fmt.Sprintf("ship-%d", s.Clk.Now().UnixMilli())
			p.Text = "ship console"
			p.Signature = nil
			if _, _, err := s.Sch.AcceptLocalPolicy(r.Context(), p, policy.LocalIssuer); err != nil {
				code := http.StatusBadRequest
				if errors.Is(err, policy.ErrHigherEchelon) || errors.Is(err, policy.ErrUnsigned) {
					code = http.StatusForbidden
				}
				http.Error(w, err.Error(), code)
				return
			}
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.writeShipPolicy(w)
	})
}

func (s *Service) shipLayer(req ShipPolicy) (*pb.PolicyLayer, error) {
	if len(req.Rules) > maxShipRules {
		return nil, fmt.Errorf("at most %d rules", maxShipRules)
	}
	by := strings.TrimSpace(req.By)
	if by == "" {
		by = "ship operator"
	}
	if len(by) > 80 {
		return nil, errors.New("by: at most 80 characters")
	}
	now := s.Clk.Now().UnixMilli()
	layer := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: policy.LocalIssuer}
	for i, rule := range req.Rules {
		if len(rule.Boost)+len(rule.Suppress) == 0 {
			return nil, fmt.Errorf("rule %d: no labels", i+1)
		}
		if err := ValidateInterest(&pb.Interest{BoostLabels: rule.Boost, SuppressLabels: rule.Suppress}); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		if rule.Weight < 0 || rule.Weight > 100 || len(rule.Text) > 280 {
			return nil, fmt.Errorf("rule %d: weight 0–100, text ≤280 characters", i+1)
		}
		if rule.ExpiresAt != 0 && rule.ExpiresAt <= now {
			continue
		}
		layer.Rules = append(layer.Rules, &pb.InterestRule{Id: fmt.Sprintf("ship-%d", i+1), Author: by,
			BoostLabels: rule.Boost, SuppressLabels: rule.Suppress, Weight: rule.Weight,
			ExpiresAtUnixMs: rule.ExpiresAt, Text: rule.Text})
	}
	switch strings.ToUpper(strings.TrimSpace(req.Emcon)) {
	case "":
	case "NORMAL":
		layer.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_NORMAL, SetBy: by, Echelon: pb.Echelon_ECHELON_SHIP}
	case "BURST":
		layer.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_BURST, SetBy: by, Echelon: pb.Echelon_ECHELON_SHIP}
	case "SILENT":
		layer.Emcon = &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: by, Echelon: pb.Echelon_ECHELON_SHIP}
	default:
		return nil, fmt.Errorf("emcon: want NORMAL, BURST or SILENT")
	}
	return layer, nil
}

func (s *Service) writeShipPolicy(w http.ResponseWriter) {
	m := protojson.MarshalOptions{EmitUnpopulated: false}
	active := s.Sch.Policy()
	if active == nil {
		active = &pb.Policy{}
	}
	activeJSON, err := m.Marshal(active)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	localJSON := []byte("null")
	if local := policy.LocalLayer(s.Sch.RawPolicy()); local != nil {
		if localJSON, err = m.Marshal(proto.Clone(local)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"local": json.RawMessage(localJSON), "active": json.RawMessage(activeJSON),
		"serverTimeUnixMs": s.Clk.Now().UnixMilli(), "ttl": time.Duration(policy.DefaultTTL).String()})
}
