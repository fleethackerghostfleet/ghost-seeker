package bench

import (
	"slices"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestUnknownOracleAndScores(t *testing.T) {
	for model := range Missions {
		for _, tc := range []struct {
			name  string
			attrs command.Attributes
			tier  int
		}{
			{"persistent", command.Attributes{}, 2},
			{"approaching", command.Attributes{Approaching: true}, 1},
			{"dark", command.Attributes{Dark: true}, 1},
			{"transient", command.Attributes{Transient: true}, 4},
			{"transient approaching", command.Attributes{Transient: true, Approaching: true, Dark: true}, 4},
		} {
			for _, conf := range []float64{0, .1, .55, .69, .7, .98} {
				d := command.Detection{Class: "unknown", Conf: conf, Attrs: tc.attrs}
				if got := OracleTier(model, d); got != tc.tier {
					t.Errorf("%s %s oracle %d", model, tc.name, got)
				}
				if got := command.TierFor(command.ScoreDet(d, nil, model).Score); got != tc.tier {
					t.Errorf("%s %s conf %v tier %d", model, tc.name, conf, got)
				}
			}
		}
	}
}

func TestUnknownInjectionPreservesOriginalDraws(t *testing.T) {
	base, injected := GenerateWithUnknowns(101, 1000, false), Generate(101, 1000)
	unknowns := 0
	for i, f := range injected {
		original := base[i]
		n := len(original.Dets)
		if f.Src != original.Src || !slices.Equal(f.Dets[:n], original.Dets) || !slices.Equal(f.FP[:n], original.FP) {
			t.Fatalf("original draws changed at %d", i)
		}
		if len(f.Dets) == n {
			if f.Edge != original.Edge {
				t.Fatalf("edge changed without injection at %d", i)
			}
			continue
		}
		unknowns++
		if len(f.Dets) != n+1 || f.Dets[n].Class != "unknown" || f.FP[n] {
			t.Fatalf("bad injection at %d", i)
		}
	}
	if unknowns < 70 || unknowns > 170 {
		t.Fatalf("injection rate: %d/1000", unknowns)
	}
	if got := Generate(1, 0); len(got) != 0 {
		t.Fatal("nonempty zero-frame generation")
	}
}

func TestClosedSetUnknownWeights(t *testing.T) {
	for model, m := range catalog.Models {
		old := m.W["unknown"]
		m.W["unknown"] = 0
		for _, a := range []command.Attributes{{}, {Approaching: true}, {Transient: true}} {
			if got := command.ScoreDet(command.Detection{Class: "unknown", Conf: 1, Attrs: a}, nil, model).Score; got != 0 {
				t.Errorf("before-mode %s: %v", model, got)
			}
		}
		m.W["unknown"] = old
	}
}
