package fact

import (
	"encoding/json"
	"reflect"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

func TestFactRoundTrip(t *testing.T) {
	it := &pb.Item{Id: "capture-1", Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: 1234, Source: "mast",
		Geo: &pb.Geo{Lat: 12, Lon: -34, HeadingDeg: 90},
		Features: &pb.Features{Unknownness: .7, ModelId: "model", LabelOverride: "tug", OverrideBy: "operator", Tags: map[string]string{"scene": "harbor"},
			Detections: []*pb.Detection{{Label: "boat", Confidence: .8, Box: &pb.Box{X: .1, Y: .2, W: .3, H: .4}}, {Label: "person", Confidence: .6}}}}
	f := FromItem(it)
	if f.ID != it.Id || f.Type != "image" || f.CapturedAt != 1234 || f.Source != "mast" || f.Lat != 12 || f.Lon != -34 || f.Heading != 90 {
		t.Fatalf("metadata = %+v", f)
	}
	var decoded Fact
	if err := json.Unmarshal(f.Marshal(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f, &decoded) || !proto.Equal(decoded.ToFeatures(), it.Features) {
		t.Fatalf("round trip lost fields: %+v", decoded)
	}
	if decoded.TopLabel() != "tug" {
		t.Fatal("override did not win")
	}
}

func TestFactTypesAndEmptyFeatures(t *testing.T) {
	for _, tc := range []struct {
		kind      pb.ItemType
		name      string
		generated bool
	}{
		{pb.ItemType_ITEM_TYPE_IMAGE, "image", true},
		{pb.ItemType_ITEM_TYPE_RECORD, "record", false},
		{pb.ItemType_ITEM_TYPE_TELEMETRY, "telemetry", true},
		{pb.ItemType_ITEM_TYPE_DIGEST, "digest", false},
		{pb.ItemType(999), "unknown", true},
	} {
		f := FromItem(&pb.Item{Type: tc.kind})
		if f.Type != tc.name || Generated(tc.kind) != tc.generated || len(f.Labels) != 0 || f.TopLabel() != "" || !proto.Equal(f.ToFeatures(), &pb.Features{}) {
			t.Fatalf("kind %v: %+v", tc.kind, f)
		}
	}
}

func TestTopLabelAndBoxBounds(t *testing.T) {
	f := &Fact{Labels: []Label{{Label: "first", Conf: .9}, {Label: "tie", Conf: .9}, {Label: "lower", Conf: .2}}}
	if f.TopLabel() != "first" {
		t.Fatal("highest-confidence tie should retain first label")
	}
	for _, n := range []int{0, 3, 4, 5} {
		f := &Fact{Labels: []Label{{Label: "boat", Box: make([]float32, n)}}}
		d := f.ToFeatures().Detections[0]
		if (d.Box != nil) != (n == 4) {
			t.Fatalf("box length %d produced %v", n, d.Box)
		}
	}
}
