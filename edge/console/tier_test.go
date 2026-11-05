package console

import (
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestValidatePinnedTiers(t *testing.T) {
	for _, in := range []*pb.Interest{nil, {}, {TierLabels: map[string]int32{"tanker": 2}}} {
		if err := ValidateInterest(in); err != nil {
			t.Fatal(err)
		}
	}
	for _, pins := range []map[string]int32{{"tanker": 0}, {"tanker": 5}, {" ": 2}, {strings.Repeat("x", MaxLabelChars+1): 2}} {
		if err := ValidateInterest(&pb.Interest{TierLabels: pins}); err == nil {
			t.Fatalf("invalid pins accepted: %v", pins)
		}
	}
}
