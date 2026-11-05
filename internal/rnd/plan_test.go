package rnd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlan(t *testing.T, dir string, plan Plan) string {
	t.Helper()
	b, _ := json.Marshal(plan)
	path := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPlanValidation(t *testing.T) {
	dir := t.TempDir()
	good := Plan{Name: "x", Sources: []PlanSource{{Name: "a", Kind: "zip", Path: "a.zip", Count: 2}, {Name: "b", Kind: "catalog", Path: "b", Count: 3}}}
	plan, err := LoadPlan(writePlan(t, dir, good))
	if err != nil || plan.Total() != 5 {
		t.Fatal(plan, err)
	}
	for _, bad := range []Plan{
		{Sources: good.Sources},
		{Name: "x"},
		{Name: "x", Sources: []PlanSource{{Name: "a", Kind: "tar", Path: "a", Count: 1}}},
		{Name: "x", Sources: []PlanSource{{Name: "a", Kind: "dir", Path: "a", Count: 0}}},
		{Name: "x", Sources: []PlanSource{{Name: "a", Kind: "dir", Path: "a", Count: 1}, {Name: "a", Kind: "dir", Path: "b", Count: 1}}},
	} {
		if _, err = LoadPlan(writePlan(t, dir, bad)); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestCatalogClipGroundTruthAndCounts(t *testing.T) {
	root := t.TempDir()
	cat := filepath.Join(root, "cat")
	if err := os.Mkdir(cat, 0700); err != nil {
		t.Fatal(err)
	}
	index := `{"file":"b.jpg","image_id":"ds/train/b"}` + "\n" + `{"file":"a.jpg","image_id":"ds/train/a"}` + "\n"
	ann := `{"image_id":"ds/train/a","label":"ferry","box":[1,2,3,4]}` + "\n" + `{"image_id":"ds/train/zzz","label":"ignored","box":[0,0,1,1]}` + "\n"
	for name, body := range map[string]string{"cat/index.jsonl": index, "ann.jsonl": ann, "cat/a.jpg": "A", "cat/b.jpg": "B"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := Plan{Name: "x", Sources: []PlanSource{{Name: "cat", Kind: "catalog", Path: "cat", Annotations: "ann.jsonl", Count: 2, Tags: map[string]string{"viewpoint": "surface"}}}}
	clips, counts, err := planClips(plan, root)
	if err != nil || len(clips) != 1 || counts[0] != 2 {
		t.Fatal(clips, counts, err)
	}
	c := clips[0]
	if c.tags["viewpoint"] != "surface" || c.frames[0].path != "ds/train/a" || c.frames[1].path != "ds/train/b" || c.frames[0].ext != ".jpg" {
		t.Fatalf("%+v", c)
	}
	if len(c.frames[0].gt) != 1 || c.frames[0].gt[0].Label != "ferry" || c.frames[0].gt[0].Box.W != 3 || len(c.frames[1].gt) != 0 || c.frames[1].gt == nil {
		t.Fatalf("%+v %+v", c.frames[0].gt, c.frames[1].gt)
	}
	if data, _ := c.frames[1].read(); string(data) != "B" {
		t.Fatal(string(data))
	}
	plan.Sources[0].Count = 3
	if _, _, err = planClips(plan, root); err == nil || !strings.Contains(err.Error(), "need 3 frames, have 2") {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cat, "index.jsonl"), []byte(`{"file":"../x.jpg","image_id":"ds/x"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = planClips(plan, root); err == nil || !strings.Contains(err.Error(), "bad index row") {
		t.Fatal(err)
	}
}
