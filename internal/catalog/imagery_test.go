package catalog

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryImages(t *testing.T) {
	sources := make(map[string]Source, len(Sources))
	for _, source := range Sources {
		sources[source.ID] = source
	}
	shipTypes := map[string]bool{
		"cargo": true, "container": true, "tanker": true, "passenger": true,
		"fishing": true, "tug": true, "sailboat": true, "small_boat": true,
		"warship": true, "submarine": true, "coast_guard": true, "unknown": true,
	}

	for sourceID, images := range Library {
		source, ok := sources[sourceID]
		if !ok {
			t.Errorf("library references unknown source %q", sourceID)
			continue
		}
		for _, image := range images {
			if image.Background {
				archive, err := zip.OpenReader(filepath.Join("..", "..", "web", "static", "imagery", "empty-ocean.zip"))
				if err != nil {
					t.Fatal(err)
				}
				f, err := archive.Open(strings.TrimPrefix(image.File, "imagery/empty-ocean/"))
				if err != nil {
					t.Fatal(err)
				}
				f.Close()
				archive.Close()
				if len(image.Dets) != 0 {
					t.Errorf("background %q has invented detections", image.File)
				}
			} else if _, err := os.Stat(filepath.Join("..", "..", "web", "static", filepath.FromSlash(image.File))); err != nil {
				t.Errorf("image %q: %v", image.File, err)
			}
			for _, detection := range image.Dets {
				if _, ok := Classes[detection.Cls]; !ok {
					t.Errorf("image %q has unknown class %q", image.File, detection.Cls)
				}
				if _, ok := Pools[source.Scene][detection.Cls]; !ok {
					t.Errorf("image %q class %q is not in the %q source pool", image.File, detection.Cls, source.Scene)
				}
				box := detection.B
				if box.X < 0 || box.Y < 0 || box.W <= 0 || box.H <= 0 || box.X+box.W > 480 || box.Y+box.H > 300 {
					t.Errorf("image %q class %q has out-of-bounds box %+v", image.File, detection.Cls, box)
				}
				if ship := detection.Ship; ship != nil {
					if !shipTypes[ship.Type] {
						t.Errorf("image %q class %q has unknown ship type %q", image.File, detection.Cls, ship.Type)
					}
					if ship.Conf < 0 || ship.Conf > 1 {
						t.Errorf("image %q class %q has ship confidence %.3f outside [0, 1]", image.File, detection.Cls, ship.Conf)
					}
					if ship.Label == "" {
						t.Errorf("image %q class %q has empty ship label", image.File, detection.Cls)
					}
					if ship.Aspect != "" && ship.Aspect != "bow" && ship.Aspect != "stern" && ship.Aspect != "beam" && ship.Aspect != "quarter" {
						t.Errorf("image %q class %q has invalid ship aspect %q", image.File, detection.Cls, ship.Aspect)
					}
					if ship.Size != "" && ship.Size != "small" && ship.Size != "medium" && ship.Size != "large" {
						t.Errorf("image %q class %q has invalid ship size %q", image.File, detection.Cls, ship.Size)
					}
					if ship.Markings != "" && ship.Markings != "visible" && ship.Markings != "not visible" {
						t.Errorf("image %q class %q has invalid ship markings %q", image.File, detection.Cls, ship.Markings)
					}
				}
			}
		}
	}

	for _, source := range Sources {
		if len(Library[source.ID]) == 0 {
			t.Errorf("source %q has no library images", source.ID)
		}
	}
}

func TestNightConfig(t *testing.T) {
	if NightConfig.Burst.BaseFPS != 1 || NightConfig.Burst.BurstFPS != 8 || NightConfig.Burst.DurationS != 3 ||
		NightConfig.Burst.MaxDuty != 0.2 || NightConfig.Burst.WindowS != 60 {
		t.Fatalf("unexpected burst configuration: %+v", NightConfig.Burst)
	}
	for _, trigger := range []string{"possible_flashing_yellow", "flashing_rate_unresolved",
		"unresolved_light_stack", "low_confidence_pattern"} {
		if !NightConfig.Burst.Triggers[trigger] {
			t.Errorf("burst trigger %q is not enabled", trigger)
		}
	}
	if NightConfig.DarkContact.Label != "dark contact" || NightConfig.DarkContact.SidePad != 0.15 ||
		NightConfig.DarkContact.Above != 1.5 || NightConfig.DarkContact.Below != 0.1 ||
		NightConfig.DarkContact.PriorityWeight != 1 || len(NightConfig.DarkContact.VesselTerms) != 19 {
		t.Fatalf("unexpected dark-contact configuration: %+v", NightConfig.DarkContact)
	}
}
