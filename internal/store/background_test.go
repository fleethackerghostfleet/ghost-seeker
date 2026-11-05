package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestBackgroundWireHasEmptyDetectionArrays(t *testing.T) {
	s := New(1)
	added, _ := s.Add(&command.Item{ID: "ocean", Background: true, KB: 1}, 0)
	stored, _ := s.GetItem("ocean")
	snapshot, _, _, _ := s.Snapshot()
	for _, item := range []command.Item{added, stored, snapshot[0]} {
		data, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if string(wire["dets"]) != "[]" || string(wire["det"]) != "[]" {
			t.Fatalf("zero-contact arrays must render safely: %s", data)
		}
	}
}

func TestBackgroundBacklogLeavesLinkForContacts(t *testing.T) {
	s := New(10)
	s.Add(&command.Item{ID: "ocean", Background: true, KB: 500}, 0)
	if len(s.AdvanceUploads(time.Hour)) != 0 {
		t.Fatal("empty water used idle bandwidth")
	}
	if it, _ := s.GetItem("ocean"); it.Tier != 4 || it.Upload.Sent != 0 {
		t.Fatalf("background: %+v", it)
	}
	s.Add(&command.Item{ID: "contact", KB: 10, Dets: []command.Detection{{Class: "warship", Conf: 1}}}, 0)
	changed := s.AdvanceUploads(time.Minute)
	if len(changed) != 1 || changed[0].ID != "contact" || !changed[0].Upload.Done {
		t.Fatalf("contact must go first: %+v", changed)
	}
	if len(s.AdvanceUploads(time.Hour)) != 0 {
		t.Fatal("background drained after contact")
	}
	if _, _, err := s.SetUploadOverride("ocean", "next", "watch"); err != nil {
		t.Fatal(err)
	}
	changed = s.AdvanceUploads(time.Minute)
	if len(changed) != 1 || changed[0].ID != "ocean" || !changed[0].Upload.Done {
		t.Fatalf("operator override: %+v", changed)
	}
}

func TestBackgroundCanBeReleasedByExplicitOperatorActions(t *testing.T) {
	for _, action := range []string{"priority", "report", "full-size"} {
		t.Run(action, func(t *testing.T) {
			s := New(1)
			s.Add(&command.Item{ID: "ocean", Background: true, KB: 1}, 0)
			switch action {
			case "priority":
				p := 2
				s.PatchItem("ocean", &p, nil, nil, nil)
			case "report":
				sent := true
				s.PatchItem("ocean", nil, nil, nil, &sent)
			case "full-size":
				if _, _, err := s.RequestFullRes("ocean", "watch"); err != nil {
					t.Fatal(err)
				}
			}
			if len(s.AdvanceUploads(time.Minute)) != 1 {
				t.Fatal("explicit operator action did not release image")
			}
		})
	}
}
