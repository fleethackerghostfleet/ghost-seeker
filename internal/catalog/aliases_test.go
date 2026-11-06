package catalog

import (
	"strings"
	"testing"
)

func TestCanonicalAliasWholePhraseNormalization(t *testing.T) {
	for _, tc := range []struct{ phrase, want string }{
		{"\u2003 KILLER\tWHALE \n", "marine mammal"},
		{"ferries", "passenger ship"},
		{"planes", "aircraft"},
		{"porpoises", "marine mammal"},
		{"dolphins", "marine mammal"},
		{"sailing yachts", "sailboat"},
		{"go-fast", "speedboat"},
		{"coast guard cutters", "coast guard"},
		{"navigation buoys", "buoy"},
		{"PERSON IN THE WATER", "person in water"},
	} {
		t.Run(tc.phrase, func(t *testing.T) {
			got, ok := CanonicalAlias(tc.phrase)
			if !ok || got != tc.want {
				t.Fatalf("CanonicalAlias(%q) = %q, %v; want %q", tc.phrase, got, ok, tc.want)
			}
		})
	}
	for _, phrase := range []string{"", " \t\n", "red whale", "whale near pier", "unidentified radar contact", "buses", "whale's", "ferry boats"} {
		if got, ok := CanonicalAlias(phrase); ok || got != "" {
			t.Errorf("qualified/unknown phrase %q broadened to %q", phrase, got)
		}
	}
}

func TestCanonicalAliasEveryConfiguredPhraseIsCaseAndWhitespaceInsensitive(t *testing.T) {
	for phrase, want := range Aliases {
		input := "\t" + strings.ReplaceAll(strings.ToUpper(phrase), " ", "\n  ") + "\u2003"
		if got, ok := CanonicalAlias(input); !ok || got != want {
			t.Errorf("normalization failed for %q", phrase)
		}
	}
}
