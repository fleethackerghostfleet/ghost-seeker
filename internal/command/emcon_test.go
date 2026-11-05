package command

import "testing"

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
