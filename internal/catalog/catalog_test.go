package catalog

import "testing"

func TestRealDatasetClassesHaveModelWeightsWithoutChangingPools(t *testing.T) {
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
		if _, ok := pool["vessel"]; ok {
			t.Errorf("%s simulation pool includes vessel", scene)
		}
		if _, ok := pool["aircraft"]; ok {
			t.Errorf("%s simulation pool includes aircraft", scene)
		}
	}
}
