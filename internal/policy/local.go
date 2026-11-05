package policy

import (
	"strings"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

// LocalIssuer marks the layer written by the ship's own console (edge
// /ship/policy). The shore never sends it, so Store.Accept carries it over
// when a downlinked policy replaces the last one.
const LocalIssuer = "ship-console"

func isLocal(l *pb.PolicyLayer) bool { return l != nil && l.IssuedBy == LocalIssuer }

// LocalLayer returns the ship-console layer of p, or nil.
func LocalLayer(p *pb.Policy) *pb.PolicyLayer {
	for _, l := range p.GetLayers() {
		if isLocal(l) {
			return l
		}
	}
	return nil
}

// WithLocalLayer returns a copy of p whose ship-console layer is replaced by
// local (removed when local is nil or empty).
func WithLocalLayer(p *pb.Policy, local *pb.PolicyLayer) *pb.Policy {
	out := &pb.Policy{}
	if p != nil {
		out = proto.Clone(p).(*pb.Policy)
	}
	layers := make([]*pb.PolicyLayer, 0, len(out.Layers)+1)
	for _, l := range out.Layers {
		if !isLocal(l) {
			layers = append(layers, l)
		}
	}
	if local != nil && (len(local.Rules) > 0 || local.Emcon != nil) {
		local = proto.Clone(local).(*pb.PolicyLayer)
		local.IssuedBy = LocalIssuer
		layers = append(layers, local)
	}
	out.Layers = layers
	return out
}

func onlyLocalLayers(layers []*pb.PolicyLayer) bool {
	for _, l := range layers {
		if !isLocal(l) {
			return false
		}
	}
	return len(layers) > 0
}

func withoutLabels(labels, drop []string) []string {
	skip := map[string]bool{}
	for _, l := range drop {
		skip[normalizeLabel(l)] = true
	}
	var out []string
	for _, l := range labels {
		if !skip[normalizeLabel(l)] {
			out = append(out, l)
		}
	}
	return out
}

func unionLabels(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, label := range list {
			if key := normalizeLabel(label); key != "" && !seen[key] {
				seen[key] = true
				out = append(out, strings.TrimSpace(label))
			}
		}
	}
	return out
}
