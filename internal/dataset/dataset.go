package dataset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
)

const Seed int64 = 101

type Image struct {
	ImageID     string      `json:"image_id"`
	DatasetID   string      `json:"dataset_id"`
	EvalSplit   string      `json:"eval_split"`
	SourceVideo string      `json:"source_video"`
	SHA256      string      `json:"sha256"`
	File        string      `json:"file"`
	Width       int         `json:"width"`
	Height      int         `json:"height"`
	Detections  []Detection `json:"dets"`
}

type Detection struct {
	Class string             `json:"cls"`
	Box   command.Box        `json:"box"`
	Conf  float64            `json:"conf"`
	Ship  *catalog.ShipClass `json:"ship,omitempty"`
}

type Replay struct {
	images  []Image
	root    string
	sources map[string]catalog.Source
	order   []int
	seed    int64
	pass    uint64
	pos     int
	count   uint64
}

type splitConfig struct {
	SourceVideoAliases map[string]string `yaml:"source_video_aliases"`
	Test               map[string]any    `yaml:"test"`
	Quarantine         map[string]any    `yaml:"quarantine"`
}

type splitPolicy struct {
	aliases       map[string]string
	aliasKeys     []string
	lockedSources map[string]map[string]struct{}
	pathDatasets  map[string]struct{}
}

type Summary struct {
	DetectionsByClass map[string]int `json:"detections_by_class"`
	ImagesWithPerson  int            `json:"images_with_person_detection"`
	PersonDrivenP1    int            `json:"person_driven_p1"`
}

func LoadManifest(path string) ([]Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var images []Image
	if err := json.Unmarshal(data, &images); err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, errors.New("dataset manifest is empty")
	}
	for i := range images {
		if err := validateImage(images[i]); err != nil {
			return nil, fmt.Errorf("manifest image %d: %w", i, err)
		}
	}
	return images, nil
}

func ValidateSplits(images []Image, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read dataset splits %q: %w", path, err)
	}
	var config splitConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return 0, fmt.Errorf("parse dataset splits %q: %w", path, err)
	}
	policy := splitPolicy{
		aliases:       map[string]string{},
		lockedSources: map[string]map[string]struct{}{"test": {}, "quarantine": {}},
		pathDatasets:  map[string]struct{}{},
	}
	for source, target := range config.SourceVideoAliases {
		policy.aliases[splitName(source)] = splitName(target)
	}
	for alias := range policy.aliases {
		policy.aliasKeys = append(policy.aliasKeys, alias)
	}
	sort.Slice(policy.aliasKeys, func(i, j int) bool {
		return len(policy.aliasKeys[i]) > len(policy.aliasKeys[j])
	})
	for split, section := range map[string]map[string]any{
		"test": config.Test, "quarantine": config.Quarantine,
	} {
		for datasetID, value := range section {
			switch entries := value.(type) {
			case string:
				policy.pathDatasets[normalizeDatasetID(datasetID)] = struct{}{}
			case []any:
				for _, entry := range entries {
					source, ok := entry.(string)
					if !ok {
						return 0, fmt.Errorf("dataset splits %q: %s.%s entries must be strings", path, split, datasetID)
					}
					if policy.lockedSources[split] == nil {
						policy.lockedSources[split] = map[string]struct{}{}
					}
					policy.lockedSources[split][policy.canonicalSource(source)] = struct{}{}
				}
			default:
				return 0, fmt.Errorf("dataset splits %q: %s.%s must be a list or string", path, split, datasetID)
			}
		}
	}

	rejected := 0
	first := ""
	for _, image := range images {
		reason := ""
		if _, ok := policy.pathDatasets[normalizeDatasetID(image.DatasetID)]; ok && hasTestPathSegment(image.ImageID) {
			reason = "image_id contains a locked /test/ path segment"
		} else {
			source := policy.canonicalSource(image.SourceVideo)
			for split, locked := range policy.lockedSources {
				for name := range locked {
					if sourceMatches(source, name) {
						reason = fmt.Sprintf("source_video matches %s split source %q", split, name)
						break
					}
				}
				if reason != "" {
					break
				}
			}
		}
		if reason != "" {
			rejected++
			if first == "" {
				first = fmt.Sprintf("image_id=%q dataset_id=%q source_video=%q: %s",
					image.ImageID, image.DatasetID, image.SourceVideo, reason)
			}
		}
	}
	if rejected > 0 {
		return rejected, fmt.Errorf("manifest contains %d locked test/quarantine row(s); first rejected row: %s", rejected, first)
	}
	return 0, nil
}

func normalizeDatasetID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func splitName(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, filepath.Ext(value))
	value = strings.TrimPrefix(strings.ToLower(value), "video_")
	return nonID.ReplaceAllString(value, "")
}

func (p splitPolicy) canonicalSource(value string) string {
	name := splitName(value)
	for _, alias := range p.aliasKeys {
		if name == alias {
			return p.aliases[alias]
		}
		if strings.HasPrefix(name, alias+"seg") {
			return p.aliases[alias] + strings.TrimPrefix(name, alias)
		}
	}
	return name
}

func sourceMatches(source, locked string) bool {
	return source == locked || strings.HasPrefix(source, locked+"seg")
}

func hasTestPathSegment(imageID string) bool {
	for _, part := range strings.Split(strings.ReplaceAll(imageID, `\`, "/"), "/") {
		if strings.EqualFold(part, "test") {
			return true
		}
	}
	return false
}

func validateImage(image Image) error {
	if image.EvalSplit == "" {
		return errors.New("eval_split is required; rebuild the manifest from the dev-only selection")
	}
	if image.EvalSplit != "dev" {
		return fmt.Errorf("eval_split %q is not 'dev' and cannot be reviewed", image.EvalSplit)
	}
	if image.DatasetID == "singapore_maritime" {
		return errors.New("singapore_maritime is excluded from review")
	}
	if image.ImageID == "" || image.DatasetID == "" || image.SourceVideo == "" {
		return errors.New("image_id, dataset_id and source_video are required")
	}
	if image.Width <= 0 || image.Height <= 0 {
		return errors.New("width and height must be positive")
	}
	if len(image.SHA256) != sha256.Size*2 {
		return errors.New("sha256 must be a 64-character hex digest")
	}
	if _, err := hex.DecodeString(image.SHA256); err != nil {
		return errors.New("sha256 must be a 64-character hex digest")
	}
	if err := validateFile(image.File); err != nil {
		return err
	}
	for _, detection := range image.Detections {
		if _, ok := catalog.Classes[detection.Class]; !ok {
			return fmt.Errorf("unknown catalog class %q", detection.Class)
		}
		if math.IsNaN(detection.Conf) || math.IsInf(detection.Conf, 0) || detection.Conf < 0 || detection.Conf > 1 {
			return errors.New("detection confidence must be between 0 and 1")
		}
		box := detection.Box
		if math.IsNaN(box.X) || math.IsNaN(box.Y) || math.IsNaN(box.W) || math.IsNaN(box.H) ||
			math.IsInf(box.X, 0) || math.IsInf(box.Y, 0) || math.IsInf(box.W, 0) || math.IsInf(box.H, 0) ||
			box.X < 0 || box.Y < 0 || box.W <= 0 || box.H <= 0 || box.X+box.W > 480 || box.Y+box.H > 300 {
			return errors.New("detection box must be within 480x300 with positive area")
		}
	}
	return nil
}

func validateFile(file string) error {
	if file == "" || strings.Contains(file, `\`) || filepath.IsAbs(file) || !filepath.IsLocal(filepath.FromSlash(file)) {
		return fmt.Errorf("unsafe image file path %q", file)
	}
	return nil
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

func sourceID(datasetID string) string {
	return "real-" + strings.Trim(nonID.ReplaceAllString(strings.ToLower(datasetID), "-"), "-")
}

func sourceName(image Image) string {
	switch image.DatasetID {
	case "seaships7000":
		return "SeaShips 7000 · clean v2 dev"
	case "smd_vis_onboard_sample":
		return "SMD onboard · clean v2 dev"
	default:
		if strings.HasPrefix(image.DatasetID, "video_") {
			return "Ghost Fleet · " + image.SourceVideo
		}
		return image.DatasetID + " · clean v2 dev"
	}
}

func Sources(images []Image) []catalog.Source {
	byID := map[string]catalog.Source{}
	for _, image := range images {
		id := sourceID(image.DatasetID)
		if _, exists := byID[id]; exists {
			continue
		}
		byID[id] = catalog.Source{
			ID: id, Name: sourceName(image), Scene: "open", Mode: "dataset",
			Loc: "clean v2 dev",
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sources := make([]catalog.Source, 0, len(ids))
	for _, id := range ids {
		sources = append(sources, byID[id])
	}
	return sources
}

func NewReplay(images []Image, imageDir string, seed int64) (*Replay, error) {
	if len(images) == 0 {
		return nil, errors.New("dataset manifest is empty")
	}
	root, err := filepath.EvalSymlinks(imageDir)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, image := range images {
		if err := validateImage(image); err != nil {
			return nil, err
		}
		path, err := resolveFile(root, image.File)
		if err != nil {
			return nil, err
		}
		if err := verifyImageSHA(path, image.SHA256); err != nil {
			return nil, fmt.Errorf("dataset image %q: %w", image.File, err)
		}
	}
	replay := &Replay{images: images, root: root, seed: seed, sources: map[string]catalog.Source{}}
	for _, source := range Sources(images) {
		replay.sources[sourceIDForName(images, source.ID)] = source
	}
	replay.shuffle()
	return replay, nil
}

func verifyImageSHA(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

func sourceIDForName(images []Image, id string) string {
	for _, image := range images {
		if sourceID(image.DatasetID) == id {
			return image.DatasetID
		}
	}
	return ""
}

func (r *Replay) shuffle() {
	r.order = rand.New(rand.NewSource(r.seed + int64(r.pass))).Perm(len(r.images))
	r.pos = 0
}

func (r *Replay) Next(ts time.Time) command.Item {
	if r.pos == len(r.order) {
		r.pass++
		r.shuffle()
	}
	image := r.images[r.order[r.pos]]
	r.pos++
	r.count++
	file, _ := resolveFile(r.root, image.File)
	info, err := os.Stat(file)
	size := int64(0)
	if err == nil {
		size = info.Size()
	}
	detections := make([]command.Detection, 0, len(image.Detections))
	for _, detection := range image.Detections {
		detections = append(detections, command.Detection{
			Class: detection.Class,
			Box:   detection.Box,
			Conf:  detection.Conf,
			Ship:  detection.Ship,
		})
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", r.pass, image.ImageID)))
	edgeRNG := rand.New(rand.NewSource(int64(h[0])<<24 | int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])))
	edge := sensor.EdgeScore(edgeRNG, detections)
	item := command.Item{
		ID:          fmt.Sprintf("real-%d-%s", r.count, hex.EncodeToString(h[:6])),
		Img:         "dataset-images/" + image.File,
		Frame:       int(r.count),
		SourceID:    sourceID(image.DatasetID),
		TS:          ts.Truncate(time.Millisecond),
		Seed:        uint32(h[4])<<24 | uint32(h[5])<<16 | uint32(h[6])<<8 | uint32(h[7]),
		KB:          int(size / 1024),
		Edge:        edge,
		Dets:        detections,
		DatasetID:   image.DatasetID,
		ImageID:     image.ImageID,
		SourceVideo: image.SourceVideo,
		SHA256:      image.SHA256,
	}
	return item
}

func Summarize(images []Image) Summary {
	summary := Summary{DetectionsByClass: map[string]int{}}
	for _, image := range images {
		item := command.Item{SourceID: sourceID(image.DatasetID)}
		for _, detection := range image.Detections {
			summary.DetectionsByClass[detection.Class]++
			item.Dets = append(item.Dets, command.Detection{
				Class: detection.Class, Box: detection.Box, Conf: detection.Conf, Ship: detection.Ship,
			})
		}
		hasPerson := false
		withoutPerson := item
		withoutPerson.Dets = nil
		for _, detection := range item.Dets {
			if detection.Class == "person" {
				hasPerson = true
			} else {
				withoutPerson.Dets = append(withoutPerson.Dets, detection)
			}
		}
		if !hasPerson {
			continue
		}
		summary.ImagesWithPerson++
		command.Rescore(&item, "maritime", nil)
		if command.TierFor(item.Central) == 1 {
			command.Rescore(&withoutPerson, "maritime", nil)
			if command.TierFor(withoutPerson.Central) != 1 {
				summary.PersonDrivenP1++
			}
		}
	}
	return summary
}

func resolveFile(root, file string) (string, error) {
	if err := validateFile(file); err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("image path escapes dataset directory: %q", file)
	}
	return target, nil
}

type imageHandler struct {
	root string
}

func ImageHandler(root string) http.Handler {
	abs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "dataset image directory unavailable", http.StatusInternalServerError)
		})
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "dataset image directory unavailable", http.StatusInternalServerError)
		})
	}
	return imageHandler{root: abs}
}

func (h imageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/dataset-images/")
	file, err := url.PathUnescape(escaped)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path, err := resolveFile(h.root, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if kind := mime.TypeByExtension(filepath.Ext(path)); kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}
