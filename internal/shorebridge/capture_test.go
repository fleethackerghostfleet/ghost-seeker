package shorebridge

import (
	"reflect"
	"slices"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestLiveCapture(t *testing.T) {
	for _, scene := range []bool{false, true} {
		c := NewConsole(New("", "", nil))
		b := &Card{item: &pb.Item{Geo: &pb.Geo{Lat: 0, Lon: -76, HeadingDeg: 0}, ContentHash: "real-hash"}, Source: "EO-P7", Forms: map[string]FormStatus{}, Tags: map[string]string{"heading": "0", "width_px": "640", "height_px": "480", "subtype": "tanker", "subtype_conf": "0.91", "unknownness": "0", "sighting_id": "track-real", "synthetic": "geo,width_px"}}
		o := &Object{Key: "capture", Best: b, Scene: scene, Frames: 8, FramesKnown: true}
		item := c.buildItem(o, objectScore{})
		got := item.Capture
		if got == nil || got.Lat != 0 || got.Lon != -76 || got.HeadingDeg != 0 || got.WidthPx != 640 || got.HeightPx != 480 || got.Subtype != "tanker" || got.SubtypeConf != .91 || got.Unknownness != 0 || got.TrackID != "track-real" || got.TrackFrames != 8 || got.ContentHash != "real-hash" {
			t.Fatalf("incorrect capture: %+v", got)
		}
		for _, key := range []string{"lat", "lon", "widthPx", "altM", "fovDeg"} {
			if !slices.Contains(got.Synthetic, key) {
				t.Fatalf("missing synthetic flag %s", key)
			}
		}
		for _, key := range []string{"headingDeg", "heightPx", "subtype", "subtypeConf", "unknownness", "trackId", "trackFrames", "contentHash"} {
			if slices.Contains(got.Synthetic, key) {
				t.Fatalf("real %s flagged synthetic", key)
			}
		}
	}
}

func TestLiveCaptureFallback(t *testing.T) {
	o := &Object{Best: &Card{Tags: map[string]string{"width_px": "bad", "unknownness": "NaN"}}}
	a, b := &command.Item{SourceID: "camera"}, &command.Item{SourceID: "camera"}
	populateCapture(a, o)
	populateCapture(b, o)
	if !reflect.DeepEqual(a.Capture, b.Capture) || len(a.Capture.Synthetic) != 13 {
		t.Fatalf("invalid fallback: %+v", a.Capture)
	}
}
