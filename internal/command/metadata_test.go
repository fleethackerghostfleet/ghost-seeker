package command

import (
	"reflect"
	"testing"
)

func TestMetadataUsesCapture(t *testing.T) {
	c := &CaptureMeta{Lat: 0, Lon: -76.1, HeadingDeg: 0, AltM: 450, WidthPx: 640, HeightPx: 480, ContentHash: "real-hash", Subtype: "patrol", SubtypeConf: .91, Unknownness: .12, TrackID: "track-1", TrackFrames: 8, FOVDeg: 45, Synthetic: []string{"lat", "headingDeg", "altM"}}
	record := BuildMetadata(Item{SourceID: "UAV-3", Capture: c})
	capture := record["capture"].(map[string]any)
	for key, want := range map[string]any{"lat": c.Lat, "lon": c.Lon, "headingDeg": c.HeadingDeg, "altM": c.AltM, "widthPx": c.WidthPx, "heightPx": c.HeightPx, "contentHash": c.ContentHash, "fovDeg": c.FOVDeg, "synthetic": c.Synthetic} {
		if !reflect.DeepEqual(capture[key], want) {
			t.Fatalf("capture %s: got %v, want %v", key, capture[key], want)
		}
	}
	model := record["model"].(map[string]any)
	for key, want := range map[string]any{"subtype": c.Subtype, "subtypeConf": c.SubtypeConf, "unknownness": c.Unknownness, "trackId": c.TrackID, "trackFrames": c.TrackFrames} {
		if !reflect.DeepEqual(model[key], want) {
			t.Fatalf("model %s: got %v, want %v", key, model[key], want)
		}
	}
}

func TestMetadataOmitsAbsentCapture(t *testing.T) {
	record := BuildMetadata(Item{})
	capture := record["capture"].(map[string]any)
	if _, ok := capture["lat"]; ok {
		t.Fatal("invented capture position")
	}
	if _, ok := record["model"].(map[string]any)["subtype"]; ok {
		t.Fatal("invented subtype")
	}
}
