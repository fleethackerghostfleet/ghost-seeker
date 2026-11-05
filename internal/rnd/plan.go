package rnd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

// Plan lists the sources of a testset and how many frames each contributes.
// Paths are relative to the sources root passed to BuildPlan.
type Plan struct {
	Name    string       `json:"name"`
	Sources []PlanSource `json:"sources"`
}

// PlanSource kinds: "zip" (frame ZIP), "dir" (flat image directory) and
// "catalog" (directory holding index.jsonl rows from the dataset catalog, with
// optional catalog annotations JSONL for ground truth).
type PlanSource struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Path        string            `json:"path"`
	Count       int               `json:"count"`
	Annotations string            `json:"annotations,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

type catalogRow struct {
	File    string `json:"file"`
	ImageID string `json:"image_id"`
}

type catalogAnnotation struct {
	ImageID string     `json:"image_id"`
	Label   string     `json:"label"`
	Box     [4]float64 `json:"box"`
}

func LoadPlan(path string) (Plan, error) {
	var plan Plan
	b, err := os.ReadFile(path)
	if err != nil {
		return plan, err
	}
	if err = json.Unmarshal(b, &plan); err != nil {
		return plan, err
	}
	seen := map[string]bool{}
	total := 0
	for _, src := range plan.Sources {
		if src.Name == "" || src.Path == "" || src.Count < 1 || seen[src.Name] {
			return plan, fmt.Errorf("plan source %q: need unique name, path and count >= 1", src.Name)
		}
		switch src.Kind {
		case "zip", "dir", "catalog":
		default:
			return plan, fmt.Errorf("plan source %q: unknown kind %q", src.Name, src.Kind)
		}
		seen[src.Name] = true
		total += src.Count
	}
	if plan.Name == "" || total == 0 {
		return plan, fmt.Errorf("plan needs a name and at least one source")
	}
	return plan, nil
}

// Total is the number of frames the plan selects.
func (p Plan) Total() int {
	n := 0
	for _, src := range p.Sources {
		n += src.Count
	}
	return n
}

func planClips(plan Plan, root string) ([]clip, []int, error) {
	clips, counts := []clip{}, []int{}
	for _, src := range plan.Sources {
		var c clip
		var err error
		p := filepath.Join(root, src.Path)
		switch src.Kind {
		case "zip":
			c, err = zipClip(src.Name, p)
		case "dir":
			c, err = dirClip(src.Name, p, "")
		case "catalog":
			ann := ""
			if src.Annotations != "" {
				ann = filepath.Join(root, src.Annotations)
			}
			c, err = catalogClip(src.Name, p, ann)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("source %s: %w", src.Name, err)
		}
		if len(c.frames) < src.Count {
			return nil, nil, fmt.Errorf("source %s: need %d frames, have %d", src.Name, src.Count, len(c.frames))
		}
		c.tags = src.Tags
		clips = append(clips, c)
		counts = append(counts, src.Count)
	}
	return clips, counts, nil
}

// catalogClip orders frames by catalog image_id and attaches ground-truth
// boxes (pixel x, y, w, h) from the catalog annotations file.
func catalogClip(name, dir, annotations string) (clip, error) {
	c := clip{name: name}
	rows, err := readRows[catalogRow](filepath.Join(dir, "index.jsonl"))
	if err != nil {
		return c, err
	}
	gt := map[string][]GTBox{}
	if annotations != "" {
		anns, err := readRows[catalogAnnotation](annotations)
		if err != nil {
			return c, err
		}
		for _, a := range anns {
			gt[a.ImageID] = append(gt[a.ImageID], GTBox{Label: a.Label, Box: command.Box{X: a.Box[0], Y: a.Box[1], W: a.Box[2], H: a.Box[3]}})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ImageID < rows[j].ImageID })
	for _, r := range rows {
		if r.File == "" || r.ImageID == "" || filepath.Base(r.File) != r.File || !imageFile(r.File) {
			return c, fmt.Errorf("bad index row %q", r.ImageID)
		}
		fileName := r.File
		boxes := gt[r.ImageID]
		if boxes == nil {
			boxes = []GTBox{}
		}
		c.frames = append(c.frames, frame{path: r.ImageID, ext: filepath.Ext(fileName), gt: boxes, read: func() ([]byte, error) { return os.ReadFile(filepath.Join(dir, fileName)) }})
	}
	if len(c.frames) == 0 {
		return c, fmt.Errorf("empty catalog %s", dir)
	}
	return c, nil
}

// BuildPlan selects, stores and scores frames according to plan; the output
// manifest keeps each frame's source tags and ground truth.
func BuildPlan(ctx context.Context, plan Plan, root, out string, an inference.Analyzer) error {
	clips, counts, err := planClips(plan, root)
	if err != nil {
		return err
	}
	return build(ctx, clips, counts, out, an)
}
