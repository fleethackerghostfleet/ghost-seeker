package command

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractDuration(t *testing.T) {
	tests := []struct {
		text     string
		minutes  int
		lookback bool
	}{
		{"boats for 10 minutes", 10, false},
		{"boats for 1 hour 30 minutes", 90, false},
		{"2 hrs and 15 mins", 135, false},
		{"boats for 1 hour, and 30 minutes", 90, false},
		{"6hrs", 360, false},
		{"for 90 mins", 90, false},
		{"for the rest of the watch", 240, false},
		{"overnight", 720, false},
		{"an hour", 60, false},
		{"last 30 min", 0, true},
		{"past 2 hours", 0, true},
		{"past the 2 hours", 0, true},
		{"for 0 minutes", 1, false},
		{"until further notice", 0, false},
		{"till further notice", 0, false},
		{"all day", 1440, false},
		{"this watch", 240, false},
		{"rest of the day", 720, false},
		{"for 30 days", 10080, false},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			minutes, rest := extractDuration(test.text)
			if minutes != test.minutes {
				t.Errorf("extractDuration() minutes = %d, want %d", minutes, test.minutes)
			}
			if test.lookback && rest != test.text {
				t.Errorf("lookback rest = %q, want unchanged %q", rest, test.text)
			}
			if !test.lookback && len(strings.Fields(rest)) > 0 && strings.Contains(rest, "hours") {
				t.Errorf("duration unit remains in rest: %q", rest)
			}
		})
	}
}

func TestExtractDurationKeepsLookbackWindow(t *testing.T) {
	minutes, rest := extractDuration("small boats from EO-P7 last 30 min for 2 hours")
	if minutes != 120 {
		t.Fatalf("duration = %d, want 120", minutes)
	}
	rules, unknown := ParseDirectiveDetailed(rest)
	if len(rules) != 1 || rules[0].WithinMin != 30 || !reflect.DeepEqual(rules[0].Sources, []string{"EO-P7"}) {
		t.Fatalf("parsed rules = %#v", rules)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown words = %v", unknown)
	}
}
