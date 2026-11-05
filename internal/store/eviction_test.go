package store

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestEvictionPrefersUntouchedQueuedItems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		protect func(*command.Item)
	}{
		{"reviewed", func(it *command.Item) { it.Reviewed = true }},
		{"dismissed", func(it *command.Item) { it.Dismissed = true }},
		{"manual", func(it *command.Item) { it.Manual = 1 }},
		{"next", func(it *command.Item) { it.Upload.Override = "next" }},
		{"hold", func(it *command.Item) { it.Upload.Override = "hold" }},
		{"mid-transfer", func(it *command.Item) { it.Upload.Sent = 1 }},
		{"escalated", func(it *command.Item) { it.Sent = true }},
		{"decision", func(it *command.Item) { it.Decision = "approved" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(3)
			start := time.Unix(0, 0)
			for i, id := range []string{"protected", "queued-old", "queued-new"} {
				s.Add(&command.Item{ID: id, TS: start.Add(time.Duration(i) * time.Second), KB: 100}, 0)
			}
			tc.protect(&s.items[0])
			// A newly added item can have an older source timestamp.
			_, evicted := s.Add(&command.Item{ID: "added", TS: start.Add(-time.Hour)}, 0)
			if evicted != "queued-old" {
				t.Fatalf("evicted %q, want queued-old", evicted)
			}
		})
	}
}

func TestEvictionCompletedAndFallback(t *testing.T) {
	for _, completed := range []bool{false, true} {
		s := New(2)
		s.Add(&command.Item{ID: "old", TS: time.Unix(1, 0)}, 0)
		s.Add(&command.Item{ID: "newer", TS: time.Unix(2, 0)}, 0)
		s.items[0].Upload.Sent = 512
		s.items[0].Upload.Done = completed
		s.items[1].Reviewed = true
		if _, evicted := s.Add(&command.Item{ID: "added"}, 0); evicted != "old" {
			t.Fatalf("completed=%v evicted=%q, want old", completed, evicted)
		}
	}
}
