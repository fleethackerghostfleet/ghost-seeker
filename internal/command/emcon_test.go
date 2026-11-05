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
	mode, rest := SplitEmcon("prioritize submarines and go dark")
	if mode != "SILENT" {
		t.Fatalf("mode = %q, want SILENT", mode)
	}
	if strings.Contains(strings.ToLower(rest), "dark") || strings.Contains(strings.ToLower(rest), "go") {
		t.Fatalf("rest still contains EMCON phrase: %q", rest)
	}
	if rest != "prioritize submarines and" {
		t.Fatalf("rest = %q", rest)
	}
}
