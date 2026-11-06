package ids

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func FuzzIdentifiers(f *testing.F) {
	for _, s := range []string{"", "01M6MSQ0RSB2E2N3ARPS6QMJHT", "Submarine_surfacing", "sim-hash-1-1730000000/EO-P7", "EO-P7:naive000001", "../escape", `<style>`, `x"y`, "é", strings.Repeat("a", 64), strings.Repeat("a", 65), strings.Repeat("a", 128), strings.Repeat("a", 129), strings.Repeat("a", 160), strings.Repeat("a", 161)} {
		f.Add(s)
	}
	item := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	name := regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)
	sighting := regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,160}$`)
	f.Fuzz(func(t *testing.T, s string) {
		if Valid(s) != item.MatchString(s) || ValidName(s) != name.MatchString(s) || ValidSightingID(s) != sighting.MatchString(s) {
			t.Fatalf("identifier validation disagrees with allowed alphabet/length for %q", s)
		}
		if Valid(s) && (!filepath.IsLocal(s) || filepath.Base(s) != s || filepath.Clean(s) != s) {
			t.Fatalf("accepted item ID is not a single local path component: %q", s)
		}
	})
}
