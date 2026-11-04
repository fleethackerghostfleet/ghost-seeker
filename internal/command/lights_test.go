package command

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestNightLibraryPythonParity(t *testing.T) {
	count := 0
	for _, images := range catalog.NightLibrary {
		for _, image := range images {
			dets := NightDetections(image)
			for i, det := range dets {
				got := ""
				if det.Signal != nil {
					got = det.Signal.RuleID
				}
				if got != image.Dets[i].Interpretation.RuleID {
					t.Errorf("%s contact %d: Go %q Python %q", image.File, i, got, image.Dets[i].Interpretation.RuleID)
				}
				if image.Dets[i].Assisted && (det.Signal == nil || !det.Signal.Assisted) {
					t.Errorf("assisted flag lost: %s", image.File)
				}
				for _, p := range det.Lights {
					if p.X < 0 || p.X > 480 || p.Y < 0 || p.Y > 300 {
						t.Fatal("light coordinates not scaled")
					}
				}
				count++
			}
		}
	}
	if count != 15 {
		t.Fatalf("parity contacts = %d, want 15", count)
	}
}

func TestLightRulesFromTable(t *testing.T) {
	for _, rule := range catalog.LightRules {
		t.Run(rule.ID, func(t *testing.T) {
			var lights []Light
			for i, c := range rule.Pattern.Colours {
				x, y := 50+float64(i)*60, 30.0
				if rule.Pattern.Layout == "vertical" {
					x, y = 100, 30+float64(i)*25
				}
				lights = append(lights, Light{Colour: c, X: x, Y: y, Flashing: rule.Flashing != nil && *rule.Flashing})
			}
			sig := InterpretLights(lights, Box{W: 200, H: 150}, rule.Pattern.Signal)
			if sig == nil || sig.RuleID != rule.ID {
				t.Fatalf("did not match data rule %s: %+v", rule.ID, sig)
			}
		})
	}
	if InterpretLights(nil, Box{W: 200, H: 150}, "") != nil || InterpretLights(nil, Box{}, "sos") != nil {
		t.Fatal("empty/invalid pattern matched")
	}
}

func TestNightLightScoringGate(t *testing.T) {
	lights := []Light{{Colour: "red", X: 100, Y: 30}, {Colour: "red", X: 100, Y: 60}}
	box := Box{W: 200, H: 150}
	nuc := InterpretLights(lights, box, "")
	base := Detection{Class: "small boat", Box: box, Conf: 1}
	plain := ScoreDet(base, nil, "maritime")
	base.Signal = nuc
	if ScoreDet(base, nil, "maritime").Score != plain.Score {
		t.Fatal("ungated ScoreDet boosted")
	}
	for _, test := range []struct {
		source string
		night  *bool
		boost  bool
	}{
		{"EO-P7", boolPointer(true), true}, {"EO-P7", boolPointer(false), false}, {"EO-P7", nil, false},
		{"IR-HE", boolPointer(true), false}, {"SAR-7", boolPointer(true), false}, {"unknown", boolPointer(true), false},
	} {
		det := base
		det.Lights = lights
		item := Item{SourceID: test.source, Night: test.night, Dets: []Detection{det}}
		RescoreAt(&item, "maritime", nil, time.Now())
		if item.Det[0].Light != test.boost {
			t.Fatalf("%s night %v: boost = %v", test.source, test.night, item.Det[0].Light)
		}
		if test.boost {
			if item.Central <= plain.Score || item.Tier >= TierFor(plain.Score) {
				t.Fatal("NUC did not raise score and tier")
			}
		} else if item.Central != plain.Score || len(item.Dets[0].Lights) != 0 || item.Dets[0].Signal != nil {
			t.Fatal("ineligible signal retained/scored")
		}
		if test.source == "SAR-7" && item.Night != nil {
			t.Fatal("SAR night must be n/a")
		}
	}
}

func boolPointer(value bool) *bool { return &value }

func TestNightJSONOmission(t *testing.T) {
	encoded, _ := json.Marshal(Item{Dets: []Detection{{}}})
	for _, field := range []string{`"night"`, `"basis"`, `"lum"`, `"lights"`, `"sig"`} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("legacy JSON contains %s", field)
		}
	}
	encoded, _ = json.Marshal(Item{Night: boolPointer(false)})
	if !strings.Contains(string(encoded), `"night":false`) {
		t.Fatal("day must differ from unknown")
	}
}
