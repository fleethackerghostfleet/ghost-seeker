package scoring

import (
	"slices"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestParseDirective(t *testing.T) {
	cases := []struct {
		text string
		want []Rule
	}{
		{"Small boats approaching the pier at speed; ignore birds", []Rule{
			{Type: "boost", Classes: []string{"small boat"}, Attrs: []string{"approaching", "moving"}},
			{Type: "suppress", Classes: []string{"bird"}, Attrs: []string{}},
		}},
		{"ignore fast boats", []Rule{{Type: "suppress", Classes: []string{"small boat"}, Attrs: []string{"moving"}}}},
		{"Only drones and helicopters", []Rule{{Type: "boost", Classes: []string{"drone", "helicopter"}, Attrs: []string{}, Only: true}}},
		{"small boats and ships", []Rule{{Type: "boost", Classes: []string{"cargo ship", "fishing vessel", "small boat", "submarine", "tanker", "warship"}, Attrs: []string{}}}},
		{"warships and ignore buoys", []Rule{
			{Type: "boost", Classes: []string{"warship"}, Attrs: []string{}},
			{Type: "suppress", Classes: []string{"buoy"}, Attrs: []string{}},
		}},
		{"hello there", nil},
	}
	for _, c := range cases {
		got := ParseDirective(c.text)
		if len(got) != len(c.want) {
			t.Fatalf("%q: got %+v, want %+v", c.text, got, c.want)
		}
		for i := range got {
			g, w := got[i], c.want[i]
			if g.Type != w.Type || g.Only != w.Only || !slices.Equal(g.Classes, w.Classes) || !slices.Equal(g.Attrs, w.Attrs) {
				t.Errorf("%q rule %d: got %+v, want %+v", c.text, i, g, w)
			}
		}
	}
}

func det(cls string, attrs ...string) catalog.Detection {
	a := map[string]bool{}
	for _, k := range attrs {
		a[k] = true
	}
	return catalog.Detection{Class: cls, Conf: .9, Attrs: a}
}

func TestScoreDirectives(t *testing.T) {
	base := Config{Model: "maritime"}
	sub := Score(base, []catalog.Detection{det("submarine")})
	if Tier(sub.Central) != 1 {
		t.Errorf("submarine should be P1, got %.2f", sub.Central)
	}
	bird := Score(base, []catalog.Detection{det("bird")})
	if Tier(bird.Central) != 4 {
		t.Errorf("bird should be P4, got %.2f", bird.Central)
	}

	d := Directive{ID: "d1", Text: "fast boats", Rules: ParseDirective("fast boats")}
	cfg := Config{Model: "maritime", Directives: []Directive{d}}
	fast := Score(cfg, []catalog.Detection{det("small boat", "moving")})
	slow := Score(cfg, []catalog.Detection{det("small boat")})
	if fast.Central <= fast.Base || !fast.Det[0].Matched || len(fast.Det[0].Notes) != 1 || fast.Det[0].Notes[0].Directive != "d1" {
		t.Errorf("moving boat should be boosted with a note: %+v", fast)
	}
	if slow.Central >= slow.Base || !slow.Det[0].Focus {
		t.Errorf("stationary boat should be de-weighted in focus mode: %+v", slow)
	}

	ign := Config{Model: "maritime", Directives: []Directive{{ID: "d2", Rules: ParseDirective("ignore warships")}}}
	w := Score(ign, []catalog.Detection{det("warship")})
	if !w.Det[0].Sup || w.Central >= w.Base*.2 {
		t.Errorf("warship should be suppressed: %+v", w)
	}
}

func TestWeightsSorted(t *testing.T) {
	ws := Weights(Config{Model: "sar"})
	if ws[0].Class != "person" || len(ws) != len(catalog.ClassOrder) {
		t.Errorf("unexpected weights: %+v", ws[:2])
	}
}
