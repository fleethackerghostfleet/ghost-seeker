package store

import (
	"testing"
	"time"
)

func TestMetaOnlySinceTracksContinuousMode(t *testing.T) {
	s := New(1)
	s.SetLowbandHold(time.Minute)
	start := s.clock
	for minute, step := range []struct {
		burst  bool
		bps    int64
		since  int
		reason string
	}{
		{false, 20000, -1, ""},
		{true, 20000, 1, "emcon"},
		{true, 20000, 1, "emcon"},
		{true, 1000, 1, "emcon"},
		{true, 1000, 1, "emcon"},
		{true, 20000, 1, "emcon"},
		{true, 20000, 1, "emcon"},
		{false, 20000, -1, ""},
		{true, 20000, 8, "emcon"},
		{false, 1000, -1, ""},
		{false, 1000, 10, "link"},
		{true, 1000, 10, "emcon"},
		{false, 1000, 10, "link"},
		{false, 20000, 10, "link"},
		{false, 20000, -1, ""},
	} {
		s.clock = start.Add(time.Duration(minute) * time.Minute)
		s.SetLinkBPS(step.bps)
		s.SetEMCON(step.burst)
		stats, _ := s.Stats()
		if stats.MetaOnly != (step.since >= 0) || stats.MetaOnlyReason != step.reason {
			t.Fatalf("minute %d: %+v", minute, stats)
		}
		if step.since < 0 {
			if stats.MetaOnlySince != nil {
				t.Fatalf("minute %d: inactive since=%v", minute, stats.MetaOnlySince)
			}
			continue
		}
		want := start.Add(time.Duration(step.since) * time.Minute)
		if stats.MetaOnlySince == nil || !stats.MetaOnlySince.Equal(want) {
			t.Fatalf("minute %d: since=%v, want %v", minute, stats.MetaOnlySince, want)
		}
	}
}
