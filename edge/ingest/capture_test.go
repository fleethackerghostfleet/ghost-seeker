package ingest

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestCaptureAddsMissingSubtypeConfidence(t *testing.T) {
	for _, label := range []string{"tanker", "vessel", ""} {
		a := &pb.Item{Features: &pb.Features{Tags: map[string]string{"subtype": "tanker"}, Detections: []*pb.Detection{{Label: label, Confidence: .92}}}}
		enrichCapture(a, nil)
		wantConf, wantSynthetic := "0.70", true
		if label == "tanker" {
			wantConf, wantSynthetic = "0.920", false
		}
		if a.Features.Tags["subtype"] != "tanker" || a.Features.Tags["subtype_conf"] != wantConf || strings.Contains(a.Features.Tags["synthetic"], "subtype_conf") != wantSynthetic {
			t.Fatalf("incorrect confidence for detection %q: %v", label, a.Features.Tags)
		}
	}
}

func TestCaptureClassificationProvenance(t *testing.T) {
	for _, real := range []bool{false, true} {
		a := &pb.Item{Features: &pb.Features{}}
		if real {
			a.Features.Detections = []*pb.Detection{{Label: "tanker", Confidence: .91}}
		}
		enrichCapture(a, nil)
		for _, key := range []string{"subtype", "subtype_conf"} {
			if strings.Contains(a.Features.Tags["synthetic"], key) == real {
				t.Fatalf("incorrect provenance for %s: %v", key, a.Features.Tags)
			}
		}
		if real && (a.Features.Tags["subtype"] != "tanker" || a.Features.Tags["subtype_conf"] != "0.910") {
			t.Fatalf("real classification lost: %v", a.Features.Tags)
		}
	}
}

func TestRetagConfirmedSubtypeConfidence(t *testing.T) {
	for _, label := range []string{"boat", "tanker"} {
		ctx := context.Background()
		g, st, clk := newIngester(t, DedupSightings, nil)
		opened := sighting("s1", pb.Sighting_EVENT_OPEN, true, "tentative")
		row, err := g.newItem(ctx, capture(clk, 1), "capture-hash", &pb.AnalyzeResponse{}, opened)
		if err != nil {
			t.Fatal(err)
		}
		if row.Item.Features.Tags["subtype_conf"] != "0.70" {
			t.Fatal("missing synthetic placeholder")
		}
		row.Item.Features.Tags["synthetic"] += ",subtype"
		if err := st.UpdateItem(ctx, row.Item); err != nil {
			t.Fatal(err)
		}
		updated, err := g.retag(ctx, row.Item.Id, &pb.Sighting{Subtype: label, Event: pb.Sighting_EVENT_UPDATE})
		if err != nil {
			t.Fatal(err)
		}
		tags := updated.Item.Features.Tags
		if label == "boat" {
			if tags["subtype_conf"] != "0.900" || strings.Contains(tags["synthetic"], "subtype") {
				t.Fatalf("real confirmation confidence not applied: %v", tags)
			}
		} else if tags["subtype_conf"] != "0.70" || !strings.Contains(tags["synthetic"], "subtype_conf") {
			t.Fatalf("unmatched confidence changed: %v", tags)
		}
		data, err := st.ReadFormAll(row.Item.Id, pb.FormKind_FORM_KIND_FACT)
		if err != nil {
			t.Fatal(err)
		}
		var f fact.Fact
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		if f.Tags["subtype_conf"] != tags["subtype_conf"] || f.Tags["synthetic"] != tags["synthetic"] {
			t.Fatal("FACT provenance not updated")
		}
	}
}
