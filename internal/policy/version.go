package policy

import (
	"errors"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

// MaxVersion keeps versions JSON-number safe for the web UI.
const MaxVersion int64 = 1<<53 - 1

var (
	ErrVersionRange  = errors.New("policy version out of range")
	ErrHigherEchelon = errors.New("layers above ECHELON_SHIP can only be set by a policy signed with a trusted key")
)

func NextVersion(last int64, now time.Time) (int64, error) {
	if last < 0 {
		last = 0
	}
	if last >= MaxVersion {
		return 0, ErrVersionRange
	}
	version := now.UnixMilli()
	if next := last + 1; next > version {
		version = next
	}
	if version > MaxVersion {
		return 0, ErrVersionRange
	}
	return version, nil
}

func HigherLayers(layers []*pb.PolicyLayer) []*pb.PolicyLayer {
	var higher []*pb.PolicyLayer
	for _, layer := range layers {
		if layer != nil && layer.Echelon > pb.Echelon_ECHELON_SHIP {
			higher = append(higher, layer)
		}
	}
	return higher
}

func HigherLayersChanged(prev, next *pb.Policy, now time.Time, ttl time.Duration) bool {
	var was []*pb.PolicyLayer
	if prev != nil && Expiry(prev, ttl).After(now) {
		was = HigherLayers(prev.Layers)
	}
	var current []*pb.PolicyLayer
	if next != nil {
		current = HigherLayers(next.Layers)
	}
	if len(was) != len(current) {
		return true
	}
	for i := range current {
		if !proto.Equal(was[i], current[i]) {
			return true
		}
	}
	return false
}

func ClampToHeldDeadline(prev, next *pb.Policy, now time.Time, ttl time.Duration) {
	if prev == nil || next == nil || !Expiry(prev, ttl).After(now) || len(HigherLayers(prev.Layers)) == 0 {
		return
	}
	nextExpiry := Expiry(next, ttl)
	prevExpiry := Expiry(prev, ttl)
	if prevExpiry.Before(nextExpiry) {
		nextExpiry = prevExpiry
	}
	next.ExpiresAtUnixMs = nextExpiry.UnixMilli()
}
