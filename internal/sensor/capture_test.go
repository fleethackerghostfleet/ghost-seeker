package sensor

import (
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"testing"
)

func TestRepeatCapture(t *testing.T) {
	g := NewGenerator()
	a := &command.Item{SourceID: "BY-12", Seed: 42}
	command.EnsureCapture(a)
	b := g.Repeat(a, "maritime", nil)
	c := g.Repeat(b, "maritime", nil)
	if a.Capture.TrackID != b.Capture.TrackID || b.Capture.TrackID != c.Capture.TrackID || a.Capture.TrackFrames != 1 || b.Capture.TrackFrames != 2 || c.Capture.TrackFrames != 3 {
		t.Fatal("repeat track mismatch")
	}
}
