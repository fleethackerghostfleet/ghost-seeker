package openvocab

import (
	"slices"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestLabelsBoundedAndNormalized(t *testing.T) {
	if len(Labels(nil)) != 0 {
		t.Fatal("nil policy")
	}
	p := &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"buoy", "sailboat", "new", "  LIFE RAFT  ", "kayak", "look for boats"}, SuppressLabels: []string{"life raft", "oil slick"}, TierLabels: map[string]int32{"orange life jacket": 1}}}
	want := []string{Prefix + "kayak", Prefix + "life raft", Prefix + "oil slick", Prefix + "orange life jacket"}
	if got := Labels(p); !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	p.Interest.BoostLabels = append(p.Interest.BoostLabels, "canoe", "red barrel", "blue barrel")
	if len(Labels(p)) != MaxPrompts {
		t.Fatal("not bounded")
	}
}
