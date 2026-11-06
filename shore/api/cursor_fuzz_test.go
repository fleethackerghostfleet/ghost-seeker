package api

import (
	"math/big"
	"regexp"
	"strconv"
	"testing"
)

func FuzzCursor(f *testing.F) {
	for _, s := range []string{"", "0", "7", "8", "-1", "-0", "+1", "0007", "9223372036854775807", "9223372036854775808", "-9223372036854775809", "1.5", " 7", "1_000"} {
		f.Add(s)
	}
	decimal := regexp.MustCompile(`^[+-]?[0-9]+$`)
	f.Fuzz(func(t *testing.T, s string) {
		want := new(big.Int)
		valid := s == ""
		if decimal.MatchString(s) {
			_, ok := want.SetString(s, 10)
			valid = ok && want.Sign() >= 0 && want.IsInt64()
		}
		got, err := parseCursor(s)
		if (err == nil) != valid {
			t.Fatalf("cursor %q: acceptance=%t, want %t", s, err == nil, valid)
		}
		if err != nil {
			if got != 0 {
				t.Fatal("invalid cursor returned a usable value")
			}
			return
		}
		if got < 0 || got != want.Int64() {
			t.Fatalf("cursor %q decoded to %d, want %s", s, got, want)
		}
		if again, err := parseCursor(strconv.FormatInt(got, 10)); err != nil || again != got {
			t.Fatal("cursor did not round trip")
		}
	})
}
