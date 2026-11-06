package command

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestCaptureDeterministic(t *testing.T) {
	for _, image := range []string{"", "missing.jpg", "eo-p7-01.jpg"} {
		a := Item{Img: image, SourceID: "EO-P7", Seed: 42, Frame: 12}
		b := a
		EnsureCapture(&a)
		EnsureCapture(&b)
		if !reflect.DeepEqual(a.Capture, b.Capture) {
			t.Fatal("not deterministic")
		}
		if a.Capture.Unknownness < 0 || a.Capture.Unknownness > 1 {
			t.Fatal("invalid unknownness")
		}
	}
}

func TestCapturePreservesReal(t *testing.T) {
	c := &CaptureMeta{Lat: 0, Lon: 12, HeadingDeg: 0, Unknownness: 0}
	a := Item{Capture: c}
	EnsureCapture(&a)
	if a.Capture != c || a.Capture.Lon != 12 {
		t.Fatal("real capture replaced")
	}
	a = Item{Dets: []Detection{{Class: "tanker", Conf: .93}}}
	EnsureCapture(&a)
	if a.Capture.Subtype != "tanker" || slices.Contains(a.Capture.Synthetic, "subtype") {
		t.Fatal("real class lost")
	}
	if a.Capture.SubtypeConf != .93 || slices.Contains(a.Capture.Synthetic, "subtypeConf") {
		t.Fatal("real detection confidence lost")
	}
	a = Item{Dets: []Detection{{Class: "vessel", Conf: .93, Ship: &catalog.ShipClass{Type: "tanker", Conf: .87}}}}
	EnsureCapture(&a)
	if a.Capture.Subtype != "tanker" || a.Capture.SubtypeConf != .87 || slices.Contains(a.Capture.Synthetic, "subtype") || slices.Contains(a.Capture.Synthetic, "subtypeConf") {
		t.Fatal("ship classification must take precedence")
	}
}

func TestCaptureRealImage(t *testing.T) {
	a := Item{Img: "eo-p7-01.jpg"}
	EnsureCapture(&a)
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../web/static/imagery", a.Img))
	if err != nil {
		t.Fatal(err)
	}
	if a.Capture.ContentHash != fmt.Sprintf("%x", sha256.Sum256(b)) || a.Capture.WidthPx <= 0 {
		t.Fatal("incorrect image metadata")
	}
	for _, key := range []string{"contentHash", "widthPx", "heightPx"} {
		if slices.Contains(a.Capture.Synthetic, key) {
			t.Fatalf("real %s flagged synthetic", key)
		}
	}
}
