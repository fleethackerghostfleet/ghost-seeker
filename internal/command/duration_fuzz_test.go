package command

import (
	"strconv"
	"testing"
)

func FuzzDuration(f *testing.F) {
	for _, text := range []string{"", "boats for 10 minutes", "boats for 1 hour 30 minutes", "2 hrs and 15 mins", "6hrs", "an hour", "last 30 min", "past the 2 hours", "for 0 minutes", "until further notice", "all day", "for 30 days", "small boats from EO-P7 last 30 min for 2 hours"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 8192 {
			return
		}
		minutes, rest := extractDuration(text)
		if minutes < 0 || minutes > 10080 {
			t.Fatalf("duration outside 0..10080: %d", minutes)
		}
		again, againRest := extractDuration(text)
		if minutes != again || rest != againRest || ExtractDurationMin(text) != minutes {
			t.Fatal("duration parsing is not deterministic")
		}
		if minutes > 0 && ExtractDurationMin(strconv.Itoa(minutes)+" minutes") != minutes {
			t.Fatal("canonical duration did not round trip")
		}
	})
}
