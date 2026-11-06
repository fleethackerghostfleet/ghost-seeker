package ingest

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
	"google.golang.org/protobuf/proto"
)

func TestCaptureEnrichment(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("invalid"), buf.Bytes()} {
		a := &pb.Item{Id: "frame-42", Source: "camera", Type: pb.ItemType_ITEM_TYPE_IMAGE}
		b := proto.Clone(a).(*pb.Item)
		enrichCapture(a, data)
		enrichCapture(b, data)
		if !proto.Equal(a, b) {
			t.Fatal("not deterministic")
		}
		enrichCapture(b, data)
		if !proto.Equal(a, b) {
			t.Fatal("not idempotent")
		}
		if len(data) == buf.Len() && (a.Features.Tags["width_px"] != "32" || strings.Contains(a.Features.Tags["synthetic"], "width_px")) {
			t.Fatal("real dimensions lost")
		}
		a.Features.Detections = []*pb.Detection{{Label: "vessel", Confidence: .9, Box: &pb.Box{X: .1, Y: .2, W: .3, H: .4}}}
		encoded := fact.FromItem(a).Marshal()
		if len(encoded) > 1024 {
			t.Fatalf("FACT is %d bytes: %s", len(encoded), encoded)
		}
	}
}

func TestCaptureJPEGDimensions(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 48)), nil); err != nil {
		t.Fatal(err)
	}
	item := &pb.Item{Id: "jpeg-frame", Source: "camera"}
	enrichCapture(item, buf.Bytes())
	tags := item.Features.Tags
	if tags["width_px"] != "64" || tags["height_px"] != "48" {
		t.Fatalf("incorrect JPEG dimensions: %v", tags)
	}
	for _, key := range []string{"width_px", "height_px"} {
		if strings.Contains(tags["synthetic"], key) {
			t.Fatalf("real %s flagged synthetic: %v", key, tags)
		}
	}
}

func TestCapturePreservesSidecar(t *testing.T) {
	a := &pb.Item{Geo: &pb.Geo{Lat: 0, Lon: 12}, Features: &pb.Features{Unknownness: 0, Tags: map[string]string{"heading": "0", "subtype": "tanker", "subtype_conf": "0.99", "unknownness": "0", "width_px": "800", "height_px": "600"}}}
	b := proto.Clone(a).(*pb.Item)
	enrichCapture(a, nil)
	if !proto.Equal(a, b) {
		t.Fatalf("real values changed: %v", a)
	}
}
