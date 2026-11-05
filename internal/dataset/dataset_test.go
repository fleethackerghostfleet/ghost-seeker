package dataset

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func testImage(id, datasetID string) Image {
	return Image{
		ImageID: id, DatasetID: datasetID, EvalSplit: "dev", SourceVideo: "clip-1",
		SHA256: strings.Repeat("a", 64), File: id + ".jpg", Width: 1920, Height: 1080,
		Detections: []Detection{{Class: "person", Box: command.Box{X: 10, Y: 20, W: 30, H: 40}, Conf: .8}},
	}
}

func TestLoadManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	image := testImage("one", "video_one")
	data, err := json.Marshal([]Image{image})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(path)
	if err != nil || len(loaded) != 1 || loaded[0].ImageID != "one" {
		t.Fatalf("LoadManifest: images=%+v err=%v", loaded, err)
	}
}

func TestLoadManifestRequiresEvalSplit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	data, err := json.Marshal([]map[string]any{{
		"image_id": "one", "dataset_id": "video_one", "source_video": "clip-1",
		"sha256": strings.Repeat("a", 64), "file": "one.jpg", "width": 1920, "height": 1080,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("LoadManifest error = %v, want rebuild guidance for missing eval_split", err)
	}
}

func TestValidateImageRejectsNonDevAndSingapore(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Image)
	}{
		{name: "test split", mutate: func(image *Image) { image.EvalSplit = "test" }},
		{name: "quarantine split", mutate: func(image *Image) { image.EvalSplit = "quarantine" }},
		{name: "singapore maritime", mutate: func(image *Image) { image.DatasetID = "singapore_maritime" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := testImage("one", "video_one")
			test.mutate(&image)
			if err := validateImage(image); err == nil {
				t.Fatal("validateImage accepted a locked review row")
			}
		})
	}
}

func TestValidateSplitsRejectsLockedRows(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		image Image
	}{
		{
			name: "list match",
			yaml: "test:\n  clips: [Spotted_A_Submarine]\n",
			image: func() Image {
				image := testImage("one", "video_spotted_a_submarine")
				image.SourceVideo = "Spotted_A_Submarine"
				return image
			}(),
		},
		{
			name: "alias match",
			yaml: "source_video_aliases:\n  Old_Clip_Name: Canonical_Clip\ntest:\n  clips: [Canonical_Clip]\n",
			image: func() Image {
				image := testImage("one", "video_old_clip")
				image.SourceVideo = "Old_Clip_Name"
				return image
			}(),
		},
		{
			name: "segment prefix",
			yaml: "test:\n  clips: [Sailboat_on_the_horizon]\n",
			image: func() Image {
				image := testImage("one", "video_sailboat")
				image.SourceVideo = "Sailboat_on_the_horizon_seg0"
				return image
			}(),
		},
		{
			name: "seaships upstream test path",
			yaml: "test:\n  seaships7000: upstream test split\n",
			image: func() Image {
				image := testImage("seaships7000/test/one", "seaships7000")
				image.SourceVideo = "000001"
				return image
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "splits.yaml")
			if err := os.WriteFile(path, []byte(test.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			rejected, err := ValidateSplits([]Image{test.image}, path)
			if rejected != 1 || err == nil || !strings.Contains(err.Error(), test.image.ImageID) {
				t.Fatalf("ValidateSplits = (%d, %v), want one rejection naming %q", rejected, err, test.image.ImageID)
			}
		})
	}
}

func TestRealReviewManifestHasNoLockedRows(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "data", "review_set", "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Skip("local review-set manifest is not present in this worktree")
	} else if err != nil {
		t.Fatal(err)
	}
	images, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := ValidateSplits(images, filepath.Join("..", "..", "edgeml", "configs", "splits.yaml"))
	if err != nil || rejected != 0 {
		t.Fatalf("real manifest locked-row count = %d, err=%v; want 0", rejected, err)
	}
	t.Logf("validated %d real manifest rows; rejected %d", len(images), rejected)
}

func TestAuthoritativeReviewSelectionHasNoLockedRows(t *testing.T) {
	path := filepath.Join("..", "..", "datasets", "review", "clean_v2_dev_review.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	images := []Image{}
	for scanner.Scan() {
		var row struct {
			ImageID     string `json:"image_id"`
			DatasetID   string `json:"dataset_id"`
			SourceVideo string `json:"source_video"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		images = append(images, Image{ImageID: row.ImageID, DatasetID: row.DatasetID, SourceVideo: row.SourceVideo})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	rejected, err := ValidateSplits(images, filepath.Join("..", "..", "edgeml", "configs", "splits.yaml"))
	if err != nil || rejected != 0 {
		t.Fatalf("authoritative selection locked-row count = %d, err=%v; want 0", rejected, err)
	}
	t.Logf("validated %d authoritative selection rows; rejected %d", len(images), rejected)
}

func TestReplayOrderAndEachOncePerPass(t *testing.T) {
	root := t.TempDir()
	images := make([]Image, 8)
	for i := range images {
		images[i] = testImage(fmt.Sprintf("image-%d", i), "video_one")
		if err := os.WriteFile(filepath.Join(root, images[i].File), []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := NewReplay(images, root, 101)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewReplay(images, root, 101)
	if err != nil {
		t.Fatal(err)
	}
	firstA, firstB := make([]string, len(images)), make([]string, len(images))
	seen := map[string]int{}
	for i := range images {
		firstA[i] = a.Next(time.Unix(int64(i), 0)).ImageID
		firstB[i] = b.Next(time.Unix(int64(i), 0)).ImageID
		seen[firstA[i]]++
	}
	if strings.Join(firstA, ",") != strings.Join(firstB, ",") {
		t.Fatalf("replay orders differ: %v != %v", firstA, firstB)
	}
	for i := range images {
		if seen[images[i].ImageID] != 1 {
			t.Fatalf("image %q replayed %d times", images[i].ImageID, seen[images[i].ImageID])
		}
	}
	secondPass := map[string]int{}
	for range images {
		secondPass[a.Next(time.Now()).ImageID]++
	}
	for id, count := range secondPass {
		if count != 1 {
			t.Fatalf("second pass has %d copies of %q", count, id)
		}
	}
}

func TestManifestRejectsUnsafeFilesAndBoxes(t *testing.T) {
	for _, file := range []string{"../outside.jpg", "/tmp/outside.jpg", `..\outside.jpg`} {
		image := testImage("one", "video_one")
		image.File = file
		if err := validateImage(image); err == nil {
			t.Fatalf("accepted unsafe file %q", file)
		}
	}
	image := testImage("one", "video_one")
	image.Detections[0].Box.X = 470
	if err := validateImage(image); err == nil {
		t.Fatal("accepted out-of-frame detection")
	}
}

func TestImageHandlerServesImagesAndBlocksTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.jpg"), []byte("jpeg"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := ImageHandler(root)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dataset-images/image.jpg", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("image response: %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	for _, path := range []string{"/dataset-images/../secret", "/dataset-images/%2e%2e/secret"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code == http.StatusOK {
			t.Fatalf("served traversal path %q", path)
		}
	}
}

func TestSummaryCountsPersonDrivenP1(t *testing.T) {
	image := testImage("person-1", "video_one")
	summary := Summarize([]Image{image})
	if summary.ImagesWithPerson != 1 || summary.PersonDrivenP1 != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}
