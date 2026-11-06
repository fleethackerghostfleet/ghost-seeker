package command

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNegativeScoring(t *testing.T) {
	it := Item{DetectorNegative: true, Edge: .1, Dets: []Detection{{Class: "person", Conf: 1}}}
	for _, model := range []string{"maritime", "rescue"} {
		RescoreAt(&it, model, nil, time.Now())
		if it.BaseTier != 4 || it.CentralTier != 4 || it.Tier != 4 || it.Edge != .1 {
			t.Fatalf("%+v", it)
		}
	}
	it.Manual = 2
	Rescore(&it, "maritime", nil)
	if it.Tier != 2 || it.CentralTier != 4 {
		t.Fatal("manual review lost")
	}
	data, _ := json.Marshal(Item{})
	if strings.Contains(string(data), "detectorNegative") {
		t.Fatal("normal item flag not omitted")
	}
}
