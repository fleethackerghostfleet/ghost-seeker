package fact

import (
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestRealisticFactFitsOneKilobyte(t *testing.T) {
	it := &pb.Item{Id: "contact-123", Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: 1767225600000,
		Source: "camera-alpha", Features: &pb.Features{ModelId: "detector-v2", Unknownness: .12,
			Tags: map[string]string{"sighting_id": "s-123", "subtype": "cargo", "synthetic": "lat,lon"},
			Detections: []*pb.Detection{{Label: "cargo", Confidence: .91, Box: &pb.Box{X: .1, Y: .2, W: .3, H: .4}},
				{Label: "boat", Confidence: .72, Box: &pb.Box{X: .4, Y: .3, W: .2, H: .2}},
				{Label: "person", Confidence: .51, Box: &pb.Box{X: .7, Y: .1, W: .1, H: .2}}}}}
	if got := len(WithScore(it, .87, 2).Marshal()); got > 1024 {
		t.Fatalf("FACT is %d bytes, want <= 1024", got)
	}
}
