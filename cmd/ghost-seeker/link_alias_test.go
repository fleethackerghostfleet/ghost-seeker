package main

import "testing"

func TestLegacyLinkRateAlias(t *testing.T) {
	for _, tc := range []struct{ rate, legacy, want int64 }{
		{-1, -1, -1}, {-1, 0, 0}, {-1, 32000, 32000}, {64000, 32000, 64000}, {0, 32000, 0},
	} {
		if got := simulationLinkRate(tc.rate, tc.legacy); got != tc.want {
			t.Fatalf("simulationLinkRate(%d, %d) = %d, want %d", tc.rate, tc.legacy, got, tc.want)
		}
	}
	for _, rate := range []int64{-1, 0, 32000} {
		sh := liveLinkShaper("localhost:8080", rate)
		if rate == -1 {
			if sh.Distribution() == nil {
				t.Fatal("default link must use the demo distribution")
			}
		} else if sh.Bps() != rate || sh.Distribution() != nil {
			t.Fatalf("legacy live rate %d: bps %d distribution %+v", rate, sh.Bps(), sh.Distribution())
		}
	}
}
