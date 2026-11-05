package rnd

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"
)

type Image struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Original string `json:"original"`
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	// Tags describe the source (viewpoint, modality, labeled); GroundTruth
	// carries dataset annotations when the source has them.
	Tags        map[string]string `json:"tags,omitempty"`
	GroundTruth []GTBox           `json:"ground_truth,omitempty"`
}
type GTBox struct {
	Label string      `json:"label"`
	Box   command.Box `json:"box"`
}
type Detection struct {
	Label   string      `json:"label"`
	Conf    float64     `json:"conf"`
	Box     command.Box `json:"box"`
	Subtype string      `json:"subtype,omitempty"`
}
type Prediction struct {
	ID              string      `json:"id"`
	ModelTier       int         `json:"model_tier"`
	ModelScore      float64     `json:"model_score"`
	EdgePriority    float64     `json:"edge_priority_score"`
	AltTierMaritime int         `json:"alt_tier_maritime"`
	Detections      []Detection `json:"detections"`
	Unknownness     float64     `json:"unknownness"`
	ModelID         string      `json:"model_id"`
	LatencyMS       int64       `json:"latency_ms"`
}
type frame struct {
	path string
	read func() ([]byte, error)
	gt   []GTBox
	ext  string // stored-file extension when path has none (catalog image IDs)
}
type clip struct {
	name   string
	frames []frame
	tags   map[string]string
}

// Allocate gives each nonempty source an equal share, redistributing shortfalls
// in stable source order without selecting any frame twice.
func Allocate(capacities []int, total int) ([]int, error) {
	counts := make([]int, len(capacities))
	available := 0
	for _, n := range capacities {
		if n < 0 {
			return nil, fmt.Errorf("negative capacity")
		}
		available += n
	}
	if total < 1 || total > available {
		return nil, fmt.Errorf("need %d frames, have %d", total, available)
	}
	for remaining := total; remaining > 0; {
		for i, n := range capacities {
			if counts[i] < n && remaining > 0 {
				counts[i]++
				remaining--
			}
		}
	}
	return counts, nil
}

func imageFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}
func discover(root string) ([]clip, error) {
	paths, err := filepath.Glob(filepath.Join(root, "frames", "*.zip"))
	if err != nil {
		return nil, err
	}
	dirs, err := filepath.Glob(filepath.Join(root, "scenarios/ghostfleet_demo/frames/*/img1"))
	if err != nil {
		return nil, err
	}
	if len(paths) != 7 || len(dirs) != 3 {
		return nil, fmt.Errorf("expected 7 ZIPs and 3 clip directories, found %d ZIPs and %d directories", len(paths), len(dirs))
	}
	paths = append(paths, dirs...)
	clips := []clip{}
	for _, p := range paths {
		var c clip
		var err error
		if filepath.Ext(p) == ".zip" {
			c, err = zipClip(strings.TrimSuffix(filepath.Base(p), "_frames.zip"), p)
		} else {
			c, err = dirClip(filepath.Base(filepath.Dir(p)), p, "img1")
		}
		if err != nil {
			return nil, err
		}
		clips = append(clips, c)
	}
	sort.Slice(clips, func(i, j int) bool { return clips[i].name < clips[j].name })
	return clips, nil
}

func zipClip(name, p string) (clip, error) {
	c := clip{name: name}
	z, err := zip.OpenReader(p)
	if err != nil {
		return c, err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.FileInfo().IsDir() || !imageFile(f.Name) {
			continue
		}
		entryName := f.Name
		c.frames = append(c.frames, frame{path: entryName, read: func() ([]byte, error) {
			r, err := zip.OpenReader(p)
			if err != nil {
				return nil, err
			}
			defer r.Close()
			for _, entry := range r.File {
				if entry.Name == entryName {
					rc, err := entry.Open()
					if err != nil {
						return nil, err
					}
					defer rc.Close()
					var b bytes.Buffer
					_, err = b.ReadFrom(rc)
					return b.Bytes(), err
				}
			}
			return nil, fmt.Errorf("missing ZIP entry %s", entryName)
		}})
	}
	return finishClip(c, p)
}

// dirClip lists the image files directly inside p; prefix is prepended to the
// recorded original path.
func dirClip(name, p, prefix string) (clip, error) {
	c := clip{name: name}
	entries, err := os.ReadDir(p)
	if err != nil {
		return c, err
	}
	for _, e := range entries {
		if e.IsDir() || !imageFile(e.Name()) {
			continue
		}
		fileName := e.Name()
		c.frames = append(c.frames, frame{path: filepath.ToSlash(filepath.Join(prefix, fileName)), read: func() ([]byte, error) { return os.ReadFile(filepath.Join(p, fileName)) }})
	}
	return finishClip(c, p)
}

func finishClip(c clip, p string) (clip, error) {
	sort.Slice(c.frames, func(i, j int) bool { return naturalLess(c.frames[i].path, c.frames[j].path) })
	if len(c.frames) == 0 {
		return c, fmt.Errorf("empty clip %s", p)
	}
	return c, nil
}

func naturalLess(a, b string) bool {
	for len(a) > 0 && len(b) > 0 {
		if a[0] >= '0' && a[0] <= '9' && b[0] >= '0' && b[0] <= '9' {
			i, j := 0, 0
			for i < len(a) && a[i] >= '0' && a[i] <= '9' {
				i++
			}
			for j < len(b) && b[j] >= '0' && b[j] <= '9' {
				j++
			}
			x, y := strings.TrimLeft(a[:i], "0"), strings.TrimLeft(b[:j], "0")
			if len(x) != len(y) {
				return len(x) < len(y)
			}
			if x != y {
				return x < y
			}
			a, b = a[i:], b[j:]
		} else {
			if a[0] != b[0] {
				return a[0] < b[0]
			}
			a, b = a[1:], b[1:]
		}
	}
	return a < b
}
func SampleIndex(i, n, count int) int {
	if count <= 1 {
		return n / 2
	}
	return i * (n - 1) / (count - 1)
}

func Predict(id string, r *pb.AnalyzeResponse) Prediction {
	epoch := time.Unix(0, 0)
	item := &pb.Item{Id: id, Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: epoch.UnixMilli(), Features: &pb.Features{Detections: r.Detections, Embedding: r.Embedding, Unknownness: r.Unknownness, ModelId: r.ModelId}}
	score := scoring.Default().Score(item, &scoring.Context{Now: epoch}, 0).Priority
	p := Prediction{ID: id, ModelScore: score, EdgePriority: score, Unknownness: float64(r.Unknownness), ModelID: r.ModelId, LatencyMS: r.LatencyMs, Detections: []Detection{}}
	scores := []float64{}
	for _, d := range r.Detections {
		b := d.GetBox()
		box := command.Box{X: float64(b.GetX()), Y: float64(b.GetY()), W: float64(b.GetW()), H: float64(b.GetH())}
		det := Detection{Label: d.Label, Conf: float64(d.Confidence), Box: box}
		for _, s := range r.Sightings {
			if s.KeyDetection != nil && s.KeyDetection.Label == d.Label && s.Subtype != "tentative" {
				det.Subtype = s.Subtype
				break
			}
		}
		p.Detections = append(p.Detections, det)
		cls := strings.ToLower(d.Label)
		if det.Subtype != "" {
			cls = strings.ToLower(det.Subtype)
		}
		scores = append(scores, command.ScoreDet(command.Detection{Class: cls, Conf: det.Conf, Box: box}, nil, "maritime").Score)
	}
	p.AltTierMaritime = command.TierFor(command.FrameScore(scores))
	return p
}
func Rank(predictions []Prediction) {
	objects := make([]*shorebridge.Object, len(predictions))
	for i, p := range predictions {
		objects[i] = &shorebridge.Object{Scene: len(p.Detections) == 0, Best: &shorebridge.Card{Score: p.EdgePriority}}
	}
	shorebridge.AssignTiers(objects)
	for i, o := range objects {
		predictions[i].ModelTier, _ = strconv.Atoi(strings.TrimPrefix(o.Priority, "P"))
	}
}

// Rerank updates only model tiers, preserving the stored inference outputs.
func Rerank(dir string) error {
	path := filepath.Join(dir, "predictions.jsonl")
	predictions, err := readRows[Prediction](path)
	if err != nil {
		return err
	}
	if len(predictions) == 0 {
		return fmt.Errorf("predictions must be nonempty")
	}
	Rank(predictions)
	f, err := os.CreateTemp(dir, ".predictions-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	encoder := json.NewEncoder(f)
	for _, p := range predictions {
		if err = encoder.Encode(p); err != nil {
			f.Close()
			return err
		}
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func selectUnique(frames []frame, target int, seen map[[32]byte]bool) (frame, []byte, [32]byte, error) {
	for distance := 0; distance < len(frames); distance++ {
		candidates := []int{target + distance}
		if distance > 0 {
			candidates = append(candidates, target-distance)
		}
		for _, index := range candidates {
			if index < 0 || index >= len(frames) {
				continue
			}
			f := frames[index]
			data, err := f.read()
			if err != nil {
				return frame{}, nil, [32]byte{}, err
			}
			digest := sha256.Sum256(data)
			if seen[digest] {
				continue
			}
			seen[digest] = true
			return f, data, digest, nil
		}
	}
	return frame{}, nil, [32]byte{}, fmt.Errorf("no byte-unique frame available near index %d", target)
}

func Build(ctx context.Context, root, out string, an inference.Analyzer) error {
	clips, err := discover(root)
	if err != nil {
		return err
	}
	capacities := []int{}
	for _, c := range clips {
		capacities = append(capacities, len(c.frames))
	}
	counts, err := Allocate(capacities, 500)
	if err != nil {
		return err
	}
	return build(ctx, clips, counts, out, an)
}

func build(ctx context.Context, clips []clip, counts []int, out string, an inference.Analyzer) error {
	var err error
	if err = os.Mkdir(out, 0700); err != nil {
		return fmt.Errorf("output must be new (parent must exist): %w", err)
	}
	if err = os.Mkdir(filepath.Join(out, "images"), 0700); err != nil {
		return err
	}
	manifest, predictions := []Image{}, []Prediction{}
	run := time.Now().UTC().Format("20060102T150405.000000000")
	seen := map[[32]byte]bool{}
	for ci, c := range clips {
		for i := 0; i < counts[ci]; i++ {
			f, data, digest, err := selectUnique(c.frames, SampleIndex(i, len(c.frames), counts[ci]), seen)
			if err != nil {
				return fmt.Errorf("source %s: %w", c.name, err)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return fmt.Errorf("%s: %w", f.path, err)
			}
			id := fmt.Sprintf("rnd-%04d", len(manifest)+1)
			ext := f.ext
			if ext == "" {
				ext = filepath.Ext(f.path)
			}
			file := id + strings.ToLower(ext)
			if err = os.WriteFile(filepath.Join(out, "images", file), data, 0600); err != nil {
				return err
			}
			manifest = append(manifest, Image{ID: id, Source: c.name, Original: f.path, File: file, SHA256: hex.EncodeToString(digest[:]), Width: cfg.Width, Height: cfg.Height, Tags: c.tags, GroundTruth: f.gt})
			response, err := an.Analyze(ctx, inference.Frame{Image: data, Source: "rnd-" + run + "-" + id}, nil, nil)
			if err != nil {
				return fmt.Errorf("%s inference: %w", id, err)
			}
			if response.ModelId == "" {
				return fmt.Errorf("%s: missing inference model ID", id)
			}
			predictions = append(predictions, Predict(id, response))
			fmt.Printf("%s source=%s model=%s score=%.6f\n", id, c.name, response.ModelId, predictions[len(predictions)-1].ModelScore)
		}
	}
	Rank(predictions)
	if err = writeJSONL(filepath.Join(out, "manifest.jsonl"), manifest); err != nil {
		return err
	}
	return writeJSONL(filepath.Join(out, "predictions.jsonl"), predictions)
}
func writeJSONL[T any](path string, rows []T) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	e := json.NewEncoder(f)
	for _, r := range rows {
		if err = e.Encode(r); err != nil {
			return err
		}
	}
	return f.Sync()
}
