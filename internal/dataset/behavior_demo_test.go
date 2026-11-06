//go:build !prod

package dataset

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestManifestAndImageValidationErrors(t *testing.T) {
	for _, data := range []string{"[]", "null", "{broken}", `[{"eval_split":"test"}]`} {
		path := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(path); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := LoadManifest(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("accepted missing manifest")
	}
	for name, mutate := range map[string]func(*Image){
		"missing ID":          func(i *Image) { i.ImageID = "" },
		"invalid dimensions":  func(i *Image) { i.Height = 0 },
		"short digest":        func(i *Image) { i.SHA256 = "abc" },
		"nonhex digest":       func(i *Image) { i.SHA256 = strings.Repeat("z", 64) },
		"unknown class":       func(i *Image) { i.Detections[0].Class = "not-a-class" },
		"negative confidence": func(i *Image) { i.Detections[0].Conf = -.1 },
		"large confidence":    func(i *Image) { i.Detections[0].Conf = 1.1 },
		"NaN confidence":      func(i *Image) { i.Detections[0].Conf = math.NaN() },
		"infinite confidence": func(i *Image) { i.Detections[0].Conf = math.Inf(1) },
		"nonfinite box":       func(i *Image) { i.Detections[0].Box.X = math.NaN() },
		"infinite box":        func(i *Image) { i.Detections[0].Box.Y = math.Inf(1) },
		"outside box":         func(i *Image) { i.Detections[0].Box = command.Box{X: 479, W: 2, H: 1} },
	} {
		t.Run(name, func(t *testing.T) {
			image := testImage("one", "video_one")
			mutate(&image)
			if err := validateImage(image); err == nil {
				t.Fatal("accepted invalid image")
			}
		})
	}
}

func TestSplitPolicyErrorsAndLongestAlias(t *testing.T) {
	if _, err := ValidateSplits(nil, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing splits accepted")
	}
	for _, data := range []string{"[", "test:\n  clips: [42]\n", "test:\n  clips: true\n"} {
		path := filepath.Join(t.TempDir(), "splits.yaml")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateSplits(nil, path); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
	path := filepath.Join(t.TempDir(), "splits.yaml")
	if err := os.WriteFile(path, []byte("source_video_aliases:\n  cam: safe\n  camseg1: locked\ntest:\n  clips: [locked]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"CAMSEG1.mp4", "camseg1_seg002.mp4"} {
		image := testImage("one", "collection")
		image.SourceVideo = source
		if n, err := ValidateSplits([]Image{image}, path); n != 1 || err == nil {
			t.Fatalf("alias %s bypassed lock: %d %v", source, n, err)
		}
	}
	if n, err := ValidateSplits(nil, path); n != 0 || err != nil {
		t.Fatalf("empty images=%d %v", n, err)
	}
}

func TestReplayRejectsUnavailableAndEscapingFiles(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "images")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReplay(nil, root, Seed); err == nil {
		t.Fatal("empty replay accepted")
	}
	if _, err := NewReplay([]Image{testImage("one", "video_one")}, filepath.Join(root, "missing"), Seed); err == nil {
		t.Fatal("missing root accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "one.jpg"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.jpg")
	if err := os.WriteFile(outside, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "..", "outside.jpg")); err != nil || string(data) != "image" {
		t.Fatalf("traversal target control: data=%q err=%v", data, err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.jpg"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"missing.jpg", "escape.jpg", "directory.jpg", "../outside.jpg"} {
		image := testImage("one", "video_one")
		image.File = file
		if _, err := NewReplay([]Image{image}, root, Seed); err == nil {
			t.Fatalf("accepted %s", file)
		}
	}
	image := testImage("one", "video_one")
	image.Width = 0
	if _, err := NewReplay([]Image{image}, root, Seed); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	image = testImage("one", "video_one")
	image.SHA256 = strings.ToUpper(image.SHA256)
	if _, err := NewReplay([]Image{image}, root, Seed); err != nil {
		t.Fatalf("uppercase digest rejected: %v", err)
	}
}

func TestDatasetImageHTTPBoundaries(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "images")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frame.jpg"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	const secret = "private-dataset-secret"
	outside := filepath.Join(parent, "secret.jpg")
	if err := os.WriteFile(outside, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "..", "secret.jpg")); err != nil || string(data) != secret {
		t.Fatalf("traversal target control: data=%q err=%v", data, err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.jpg")); err != nil {
		t.Fatal(err)
	}
	h := ImageHandler(root)
	for _, tc := range []struct {
		method, path, byteRange string
		status                  int
		body                    string
	}{
		{"GET", "frame.jpg", "", 200, "image"}, {"HEAD", "frame.jpg", "", 200, ""},
		{"GET", "frame.jpg", "bytes=1-3", 206, "mag"}, {"GET", "frame.jpg", "bytes=99-", 416, ""},
		{"POST", "frame.jpg", "", 405, ""}, {"GET", "missing.jpg", "", 404, ""},
		{"GET", "directory", "", 404, ""}, {"GET", "escape.jpg", "", 404, ""},
		{"GET", "../secret.jpg", "", 404, ""},
		{"GET", "%2e%2e/secret.jpg", "", 404, ""}, {"GET", "..%5csecret.jpg", "", 404, ""},
	} {
		r := httptest.NewRequest(tc.method, "/dataset-images/"+tc.path, nil)
		r.Header.Set("Range", tc.byteRange)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: status=%d body=%s", tc, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("%+v: response leaked parent file contents", tc)
		}
		if tc.status == 200 || tc.status == 206 {
			if w.Body.String() != tc.body || w.Header().Get("Content-Type") != "image/jpeg" {
				t.Fatalf("response=%+v %q", w.Header(), w.Body)
			}
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal("missing method guidance")
		}
	}
	w := httptest.NewRecorder()
	ImageHandler(filepath.Join(root, "missing")).ServeHTTP(w, httptest.NewRequest("GET", "/dataset-images/frame.jpg", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), root) {
		t.Fatalf("unavailable root response=%d %s", w.Code, w.Body)
	}
}

func TestDatasetSourceNamesAndEmptySummary(t *testing.T) {
	images := []Image{testImage("one", "seaships7000"), testImage("two", "smd_vis_onboard_sample"), testImage("three", "other"), testImage("four", "video_clip")}
	sources := Sources(images)
	if len(sources) != 4 {
		t.Fatalf("sources=%+v", sources)
	}
	want := map[string]string{"real-seaships7000": "SeaShips 7000 · clean v2 dev", "real-smd-vis-onboard-sample": "SMD onboard · clean v2 dev", "real-other": "other · clean v2 dev", "real-video-clip": "Ghost Fleet · clip-1"}
	for _, source := range sources {
		if source.Name != want[source.ID] {
			t.Fatalf("source=%+v", source)
		}
	}
	data, err := json.Marshal(Summarize(nil))
	if err != nil || string(data) != `{"detections_by_class":{},"images_with_person_detection":0,"person_driven_p1":0}` {
		t.Fatalf("empty summary=%s %v", data, err)
	}
}
