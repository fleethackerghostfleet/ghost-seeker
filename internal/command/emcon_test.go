package command

import (
	"strings"
	"testing"
)

func TestParseEmcon(t *testing.T) {
	cases := map[string]string{
		"go quiet":                                   "SILENT",
		"Radio silence until further notice":         "SILENT",
		"burst mode, flash traffic only":             "BURST",
		"resume normal ops":                          "NORMAL",
		"set emcon normal":                           "NORMAL",
		"ignore cargo ships; prioritize submarines":  "",
		"prioritize submarines and go dark":          "SILENT",
		"go quiet for 20 minutes then resume normal": "NORMAL",
		"look for the normal fishing fleet":          "",
		"keep the quiet contacts":                    "",
	}
	for text, want := range cases {
		if got := ParseEmcon(text); got != want {
			t.Errorf("ParseEmcon(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestSplitEmcon(t *testing.T) {
	tests := []struct {
		text, wantMode, wantRest string
	}{
		{"Ⱥ prioritize submarines and go quiet", "SILENT", "Ⱥ prioritize submarines and"},
		{"set the fleet to go dark", "SILENT", "set the fleet to"},
		{"prioritize ships going dark", "", "prioritize ships going dark"},
		{"flag contacts that go quiet", "", "flag contacts that go quiet"},
		{"watch for tankers which are going dark", "", "watch for tankers which are going dark"},
		{"flag targets that go quiet", "", "flag targets that go quiet"},
		{"watch tracks which are going dark", "", "watch tracks which are going dark"},
		{"prioritize submarines and go dark", "SILENT", "prioritize submarines and"},
		{"Go quiet", "SILENT", ""},
		{"we are going dark", "SILENT", "we are"},
		{"prioritize ships\n  go dark", "SILENT", "prioritize ships"},
		{"go quiet for 20 minutes then resume normal", "NORMAL", "for 20 minutes then"},
	}
	for _, test := range tests {
		mode, rest := SplitEmcon(test.text)
		if mode != test.wantMode || rest != test.wantRest {
			t.Errorf("SplitEmcon(%q) = (%q, %q), want (%q, %q)",
				test.text, mode, rest, test.wantMode, test.wantRest)
		}
		if mode != "" && strings.Contains(strings.ToLower(rest), "go dark") {
			t.Errorf("rest still contains EMCON phrase: %q", rest)
		}
	}
}
