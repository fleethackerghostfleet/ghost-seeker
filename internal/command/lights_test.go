//go:build !prod

package command

import (
	"encoding/json"
	"math"
	"os"
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
				if det.Signal != nil && (det.Signal.Boost != image.Dets[i].Interpretation.Boost || det.Signal.LowConfidence != image.Dets[i].Interpretation.LowConfidence) {
					t.Errorf("%s contact %d: Python/Go boost confidence differs", image.File, i)
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

func TestSharedLightConfidenceCases(t *testing.T) {
	data, err := os.ReadFile("../catalog/light_confidence_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string  `json:"name"`
		Lights []Light `json:"lights"`
		Signal string  `json:"signal"`
		RuleID string  `json:"rule_id"`
		Boost  float64 `json:"boost"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			signal := InterpretLights(tc.Lights, Box{W: 200, H: 150}, tc.Signal)
			if signal == nil || signal.RuleID != tc.RuleID || signal.Boost != tc.Boost {
				t.Fatalf("shared Python/Go case: %+v", signal)
			}
		})
	}
}

func TestDistressTrackFlashingSurvivesEveryFrame(t *testing.T) {
	count := 0
	for _, images := range catalog.NightLibrary {
		for _, image := range images {
			if image.File < "imagery/night/night-05.jpg" || image.File > "imagery/night/night-08.jpg" {
				continue
			}
			count++
			det := NightDetections(image)[0]
			if len(det.Lights) != 1 || det.Lights[0].Colour != "red" || !det.Lights[0].Flashing {
				t.Errorf("%s lost flashing track light: %+v", image.File, det.Lights)
			}
			if det.Signal == nil || !det.Signal.Assisted || det.Signal.Boost != 1 {
				t.Errorf("%s lost assisted distress signal", image.File)
			}
		}
	}
	if count != 4 {
		t.Fatalf("expected four distress frames, got %d", count)
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
	lights := []Light{{Colour: "red", X: 100, Y: 30, Conf: 1}, {Colour: "red", X: 100, Y: 60, Conf: 1}}
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

func TestLightConfidenceGate(t *testing.T) {
	box := Box{W: 200, H: 150}
	for _, tc := range []struct {
		name       string
		confidence float64
		boost      bool
	}{
		{"missing", 0, false}, {"below", .849, false}, {"at", .85, true},
		{"above", .95, true}, {"invalid", math.NaN(), false}, {"out_of_range", 1.1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lights := []Light{{Colour: "red", X: 100, Y: 30, Conf: 1}, {Colour: "red", X: 100, Y: 60, Conf: tc.confidence}}
			signal := InterpretLights(lights, box, "")
			if signal == nil || signal.RuleID != "nuc" || (signal.Boost > 0) != tc.boost {
				t.Fatalf("confidence gate: %+v", signal)
			}
			det := Detection{Class: "small boat", Box: box, Conf: 1, Lights: lights, Signal: &LightSignal{RuleID: "nuc", Boost: .9}}
			if ScoreDet(det, nil, "maritime", true).Light != tc.boost {
				t.Fatal("ScoreDet did not recheck confidence")
			}
			item := Item{SourceID: "EO-P7", Night: boolPointer(true), Dets: []Detection{det}}
			RescoreAt(&item, "maritime", nil, time.Now())
			if item.Det[0].Light != tc.boost {
				t.Fatal("RescoreAt did not recheck confidence")
			}
			assisted := InterpretLights(lights, box, "sos")
			if assisted == nil || assisted.Boost != 1 || !assisted.Assisted {
				t.Fatal("confidence suppressed assisted distress")
			}
		})
	}
}

func TestStaleNUCGeometryCannotBoost(t *testing.T) {
	box := Box{W: 200, H: 150}
	lights := []Light{{Colour: "red", X: 20, Y: 40}, {Colour: "red", X: 180, Y: 40}}
	if InterpretLights(lights, box, "") != nil {
		t.Fatal("horizontal reds should not match a vertical NUC pattern")
	}
	det := Detection{Class: "small boat", Box: box, Conf: 1, Lights: lights, Signal: &LightSignal{RuleID: "nuc", Boost: .9}}
	if ScoreDet(det, nil, "maritime", true).Light {
		t.Fatal("stale NUC boosted horizontally separated reds")
	}
	item := Item{SourceID: "EO-P7", Night: boolPointer(true), Dets: []Detection{det}}
	RescoreAt(&item, "maritime", nil, time.Now())
	if item.Det[0].Light || item.Dets[0].Signal != nil {
		t.Fatal("rescore retained geometrically invalid NUC")
	}
}

func TestExtraLightsCannotBoost(t *testing.T) {
	box := Box{W: 200, H: 150}
	stack := []Light{{Colour: "red", X: 100, Y: 30}, {Colour: "red", X: 100, Y: 60}}
	for _, extras := range [][]Light{{{Colour: "white", X: 180, Y: 20}}, {{Colour: "green", X: 180, Y: 20}}, {{Colour: "white", X: 160, Y: 20}, {Colour: "green", X: 190, Y: 40}, {Colour: "red", X: 20, Y: 80}}} {
		lights := append(append([]Light(nil), stack...), extras...)
		for i := range lights {
			lights[i].Conf = 1
		}
		signal := InterpretLights(lights, box, "")
		if signal == nil || signal.RuleID != "nuc" || !signal.LowConfidence || signal.Boost != 0 || signal.ConfidenceNote != "extra unexplained lights — verify, no priority boost" {
			t.Fatalf("partial NUC must remain visible but unboosted: %+v", signal)
		}
		stale := *signal
		stale.Boost = .9
		stale.LowConfidence = false
		det := Detection{Class: "small boat", Box: box, Conf: 1, Lights: lights, Signal: &stale}
		if ScoreDet(det, nil, "maritime", true).Light {
			t.Fatal("stale signal bypassed score gate")
		}
		item := Item{SourceID: "EO-P7", Night: boolPointer(true), Dets: []Detection{det}}
		RescoreAt(&item, "maritime", nil, time.Now())
		if item.Det[0].Light || !item.Dets[0].Signal.LowConfidence || item.Dets[0].Signal.Boost != 0 {
			t.Fatal("rescore bypassed confidence gate")
		}
		assisted := InterpretLights(lights, box, "sos")
		if assisted.Boost != 1 || assisted.LowConfidence || !assisted.Assisted {
			t.Fatal("signal-assisted distress was suppressed")
		}
	}
}

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
