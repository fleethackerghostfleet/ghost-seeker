package store

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestUploadBudgetAndPriority(t *testing.T) {
	s := New(10)
	s.SetLinkBPS(8_000)
	s.items = []command.Item{
		{ID: "routine", Tier: 3, Central: .9, Upload: command.Upload{Bytes: 1000}},
		{ID: "low-score", Tier: 1, Central: .7, Upload: command.Upload{Bytes: 1000}},
		{ID: "critical", Tier: 1, Central: .9, Upload: command.Upload{Bytes: 600}},
	}
	got := s.AdvanceUploads(time.Second)
	if len(got) != 2 || got[0].ID != "critical" || !got[0].Upload.Done || got[1].ID != "low-score" || got[1].Upload.Sent != 400 {
		t.Fatalf("unexpected transfers: %+v", got)
	}
	if s.items[0].Upload.Sent != 0 {
		t.Fatal("lower tier consumed budget")
	}
}

func TestUploadFractionalOfflineAndEmpty(t *testing.T) {
	s := New(1)
	if len(s.AdvanceUploads(time.Second)) != 0 {
		t.Fatal("empty queue")
	}
	s.SetLinkBPS(8)
	s.Add(&command.Item{ID: "one", KB: 1}, 0)
	for range 4 {
		s.AdvanceUploads(250 * time.Millisecond)
	}
	if s.items[0].Upload.Sent != 1 {
		t.Fatal("fractional bytes lost")
	}
	s.SetLinkBPS(0)
	if len(s.AdvanceUploads(time.Hour)) != 0 || len(s.AdvanceUploads(-time.Second)) != 0 {
		t.Fatal("offline transfer")
	}
}

func TestUploadEvictionAndFullSize(t *testing.T) {
	s := New(1)
	s.Add(&command.Item{ID: "old", KB: 1}, 0)
	s.Add(&command.Item{ID: "new", KB: 1}, 0)
	got := s.AdvanceUploads(time.Second)
	if len(got) != 1 || got[0].ID != "new" || !got[0].Upload.Done {
		t.Fatalf("evicted upload: %+v", got)
	}
	it, _, err := s.RequestFullRes("new", "operator")
	if err != nil || it.Upload.Done || it.Upload.Bytes < 4*1024*1024 {
		t.Fatalf("full size: %+v %v", it, err)
	}
	if _, _, err = s.RequestFullRes("missing", "operator"); err != ErrNotFound {
		t.Fatal(err)
	}
}
