package catalog

import (
	"os"
	"regexp"
	"testing"
)

func TestRealDatasetClassesHaveModelWeightsWithoutChangingPools(t *testing.T) {
	if got := Classes["marine mammal"]; got != (Class{Group: "other", Length: .1, Aspect: .4, Label: "Marine mammal (whale)"}) || EdgeSubtypes["marine mammal"] {
		t.Fatalf("marine mammal class = %+v", got)
	}
	for name, want := range map[string]float64{"maritime": .1, "air": .05, "sar": .1} {
		if got := Models[name].W["marine mammal"]; got != want {
			t.Errorf("%s mammal weight = %v; want %v", name, got, want)
		}
	}
	if got := Classes["vessel"]; got.Group != "vessel" || got.Label != "Vessel — type not classified" {
		t.Fatalf("vessel class = %+v", got)
	}
	if got := Classes["aircraft"]; got.Group != "aircraft" || got.Label != "Aircraft — type not classified" {
		t.Fatalf("aircraft class = %+v", got)
	}
	for name, model := range Models {
		if model.W["vessel"] != model.W["cargo ship"] {
			t.Errorf("%s vessel weight = %v, want cargo ship weight %v", name, model.W["vessel"], model.W["cargo ship"])
		}
		if model.W["aircraft"] != model.W["military aircraft"] {
			t.Errorf("%s aircraft weight = %v, want military aircraft weight %v", name, model.W["aircraft"], model.W["military aircraft"])
		}
	}
	for scene, pool := range Pools {
		if _, ok := pool["marine mammal"]; ok {
			t.Errorf("%s simulation pool includes marine mammal", scene)
		}
		if _, ok := pool["vessel"]; ok {
			t.Errorf("%s simulation pool includes vessel", scene)
		}
		if _, ok := pool["aircraft"]; ok {
			t.Errorf("%s simulation pool includes aircraft", scene)
		}
	}
}

func TestEdgeSubtypesCoverSidecarSubtypes(t *testing.T) {
	src, err := os.ReadFile("../../edgeml/src/edgeml/embed/zeroshot.py")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)SUBTYPES: dict\[str, list\[str\]\] = \{(.*?)\n\}`).FindSubmatch(src)
	if block == nil {
		t.Fatal("SUBTYPES not found in zeroshot.py")
	}
	labels := regexp.MustCompile(`(?m)^\s*"([^"]+)":`).FindAllSubmatch(block[1], -1)
	if len(labels) == 0 {
		t.Fatal("no SUBTYPES labels parsed")
	}
	for _, m := range labels {
		label := string(m[1])
		if _, ok := Classes[label]; !ok && !EdgeSubtypes[label] && label != "fishing boat" {
			t.Errorf("sidecar subtype %q is neither a catalog class nor an EdgeSubtype", label)
		}
	}
}
