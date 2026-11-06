package store

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestDetectorNegativeUploadsInPriorityOrder(t *testing.T) {
	s := New(10)
	s.SetLinkBPS(8 * 1536)
	negative, _ := s.Add(&command.Item{ID: "negative", DetectorNegative: true, KB: 1}, 0)
	normal, _ := s.Add(&command.Item{ID: "priority", KB: 1, Manual: 2}, 0)
	if negative.Tier != 4 || negative.Upload.Bytes != 1536 || normal.Tier != 2 {
		t.Fatalf("queued items: %+v %+v", negative, normal)
	}
	stats, _ := s.Stats()
	if stats.Keep != 2 || stats.DetectorNegatives != 1 || stats.Drop != 0 || stats.Bytes != 0 {
		t.Fatalf("queued stats: %+v", stats)
	}
	first := s.AdvanceUploads(time.Second)
	second := s.AdvanceUploads(time.Second)
	if len(first) != 1 || first[0].ID != normal.ID || !first[0].Upload.Done || len(second) != 1 || second[0].ID != negative.ID || !second[0].Upload.Done {
		t.Fatalf("upload order: %+v %+v", first, second)
	}
	stats, _ = s.Stats()
	if stats.Bytes != 3072 {
		t.Fatalf("transferred bytes: %+v", stats)
	}
}
