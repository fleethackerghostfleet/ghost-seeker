package scheduler

import (
	"context"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestMetaOnlySinceTracksContinuousMode(t *testing.T) {
	s, _, _, clk := setup(t, Prioritized)
	s.lowband.Hold = time.Minute
	start := clk.Now()
	for minute, step := range []struct {
		burst  bool
		bps    float64
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
		mode := pb.EmconMode_EMCON_MODE_NORMAL
		if step.burst {
			mode = pb.EmconMode_EMCON_MODE_BURST
		}
		if _, _, err := s.ApplyPolicy(context.Background(), &pb.Policy{Id: "mode", Emcon: mode}); err != nil {
			t.Fatal(err)
		}
		s.BuildPlan(nil, step.bps, start.Add(time.Duration(minute)*time.Minute))
		stats := s.Stats()
		if stats.MetaOnly != (step.since >= 0) || stats.MetaOnlyReason != step.reason {
			t.Fatalf("minute %d: %+v", minute, stats)
		}
		want := time.Time{}
		if step.since >= 0 {
			want = start.Add(time.Duration(step.since) * time.Minute)
		}
		if !stats.MetaOnlySince.Equal(want) {
			t.Fatalf("minute %d: since=%v, want %v", minute, stats.MetaOnlySince, want)
		}
	}
}
