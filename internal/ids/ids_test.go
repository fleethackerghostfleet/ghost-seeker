package ids

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	valid := []string{
		"Submarine_surfacing",
		"Cruise_ship_Mediterranean_sunset_off_Italy",
		"sim-hash-1-1730000000/EO-P7",
		"Submarine_surfacing:0001",
		"EO-P7-mock-1",
		"EO-P7:naive000001",
	}
	for _, s := range valid {
		t.Run(s, func(t *testing.T) {
			if !ValidName(s) {
				t.Fatalf("ValidName(%q) = false", s)
			}
		})
	}

	invalid := []string{
		"",
		strings.Repeat("a", 129),
		"<style>",
		`x"y`,
		"a'b",
		"a&b",
		"a b",
		"a>b",
		"é",
	}
	for _, s := range invalid {
		t.Run("invalid_"+s, func(t *testing.T) {
			if ValidName(s) {
				t.Fatalf("ValidName(%q) = true", s)
			}
		})
	}
}

func TestValidSightingID(t *testing.T) {
	valid := []string{
		strings.Repeat("a", 124) + ":0001",
		strings.Repeat("a", 128) + ":12345",
	}
	for _, s := range valid {
		t.Run(s, func(t *testing.T) {
			if !ValidSightingID(s) {
				t.Fatalf("ValidSightingID(%q) = false", s)
			}
		})
	}

	invalid := []string{
		strings.Repeat("a", 161),
		`x"y`,
		"",
	}
	for _, s := range invalid {
		t.Run("invalid_"+s, func(t *testing.T) {
			if ValidSightingID(s) {
				t.Fatalf("ValidSightingID(%q) = true", s)
			}
		})
	}
}
