package store

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestResetPreservesMetadataOnlyMode(t *testing.T) {
	for _, reason := range []string{"link", "emcon"} {
		t.Run(reason, func(t *testing.T) {
			s := New(1)
			if reason == "link" {
				s.SetLowbandHold(time.Second)
				s.SetLinkBPS(4000)
				s.AdvanceUploads(time.Second)
			} else {
				s.SetEMCON(true)
			}
			s.Reset()
			_, stats, _, _ := s.Snapshot()
			if !stats.MetaOnly || stats.MetaOnlyReason != reason || stats.MetaOnlySince == nil {
				t.Fatalf("reset lost metadata-only state: %+v", stats)
			}
		})
	}
}

func TestUploadNextRecordsOperatorSend(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		s := New(1)
		s.items = []command.Item{{ID: "image", Sent: automatic, AutoSent: automatic, Upload: command.Upload{Bytes: 1000}}}
		if automatic {
			s.cdr = 1
		}
		item, cdr, err := s.SetUploadOverride("image", "next", "Alice")
		if err != nil || !item.Sent || item.AutoSent || item.HumanReviewed || item.Reviewed || item.SentAt == nil || cdr != 1 {
			t.Fatalf("operator send: %+v, cdr=%d err=%v", item, cdr, err)
		}
		_, cdr, err = s.SetUploadOverride("image", "next", "Alice")
		if err != nil || cdr != 1 {
			t.Fatal("repeat upload next double counted send")
		}
	}
	s := New(1)
	s.items = []command.Item{{ID: "held", Upload: command.Upload{Bytes: 1000}}}
	item, _, _ := s.SetUploadOverride("held", "hold", "Alice")
	if item.Sent {
		t.Fatal("holding image recorded a send")
	}
}

func TestUploadHighRateBudget(t *testing.T) {
	for _, tc := range []struct {
		bps     int64
		elapsed time.Duration
		want    int64
	}{
		{5_000_000_000, 2 * time.Second, 1_250_000_000},
		{math.MaxInt64, time.Duration(math.MaxInt64), math.MaxInt64},
	} {
		s := New(1)
		s.SetLinkBPS(tc.bps)
		s.items = []command.Item{{Upload: command.Upload{Bytes: math.MaxInt64}}}
		s.AdvanceUploads(tc.elapsed)
		if got := s.items[0].Upload.Sent; got != tc.want {
			t.Fatalf("rate %d: sent %d, want %d", tc.bps, got, tc.want)
		}
	}
}

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
	if err != nil || it.Upload.Done || it.Upload.Sent != 0 || it.Upload.Bytes < 4*1024*1024 {
		t.Fatalf("full size: %+v %v", it, err)
	}
	if _, _, err = s.RequestFullRes("missing", "operator"); err != ErrNotFound {
		t.Fatal(err)
	}
	s.AdvanceUploads(time.Second)
	it, _, err = s.RequestFullRes("new", "operator")
	if err != nil || it.Upload.Sent != 32_000 {
		t.Fatalf("repeat request reset progress: %+v %v", it, err)
	}
	stats, _ := s.Stats()
	if stats.Bytes != 1536+32_000 {
		t.Fatalf("compressed transfer was lost: %+v", stats)
	}
}

func TestFullSizeRequestJumpsPriorityQueue(t *testing.T) {
	s := New(3)
	s.Add(&command.Item{ID: "critical", KB: 10, Manual: 1}, 0)
	s.Add(&command.Item{ID: "low", KB: 10, Manual: 4}, 0)
	if _, _, err := s.RequestFullRes("low", "operator"); err != nil {
		t.Fatal(err)
	}
	changed := s.AdvanceUploads(time.Second)
	if len(changed) != 1 || changed[0].ID != "low" || changed[0].Upload.Sent != 32_000 || changed[0].Upload.Override != "next" {
		t.Fatalf("full-size queue precedence: %+v", changed)
	}
}

func TestFullSizeTransferPreservesEarlierShoreReceipt(t *testing.T) {
	for _, received := range []bool{false, true} {
		s := New(1)
		s.Add(&command.Item{ID: "image", KB: 1}, 0)
		if received {
			s.AdvanceUploads(time.Second)
		}
		item, _, err := s.RequestFullRes("image", "operator")
		if err != nil || item.Upload.Received != received || item.Upload.Done {
			t.Fatalf("receipt before transfer %v: %+v %v", received, item.Upload, err)
		}
		s.AdvanceUploads(time.Second)
		snapshot, _, _, _ := s.Snapshot()
		if snapshot[0].Upload.Received != received || snapshot[0].Upload.Done {
			t.Fatalf("receipt during transfer %v: %+v", received, snapshot[0].Upload)
		}
		s.AdvanceUploads(time.Hour)
		item, _ = s.GetItem("image")
		if !item.Upload.Received || !item.Upload.Done {
			t.Fatalf("full-size receipt: %+v", item.Upload)
		}
	}
}
func TestUploadStatisticsCountTransferredBytesAndShipStorage(t *testing.T) {
	s := New(1)
	s.SetLinkBPS(0)
	s.Add(&command.Item{ID: "queued", KB: 1}, 0)
	s.AdvanceUploads(time.Second)
	stats, _ := s.Stats()
	if stats.Bytes != 0 || stats.DB != 1 {
		t.Fatalf("offline stats: %+v", stats)
	}
	s.SetLinkBPS(8_000)
	s.AdvanceUploads(time.Second)
	stats, _ = s.Stats()
	if stats.Bytes != 1000 || stats.DB != 1 {
		t.Fatalf("partial stats: %+v", stats)
	}
	s.AdvanceUploads(time.Second)
	s.AdvanceUploads(time.Second)
	stats, _ = s.Stats()
	if stats.Bytes != 1536 || stats.DB != 1 {
		t.Fatalf("completed stats: %+v", stats)
	}
	s.Add(&command.Item{ID: "replacement", KB: 1}, 0)
	stats, _ = s.Stats()
	if stats.Bytes != 1536 || stats.DB != 2 {
		t.Fatalf("eviction changed totals: %+v", stats)
	}
}

func TestUploadOverridesOrderHoldAndRelease(t *testing.T) {
	s := New(10)
	s.SetLinkBPS(8_000)
	s.items = []command.Item{
		{ID: "held", Tier: 1, Central: 1, Upload: command.Upload{Bytes: 1000}},
		{ID: "priority", Tier: 1, Central: .9, Upload: command.Upload{Bytes: 1000}},
		{ID: "second", Tier: 3, Central: .5, Upload: command.Upload{Bytes: 1000}},
		{ID: "first", Tier: 4, Central: .1, Upload: command.Upload{Bytes: 1000}},
	}
	for _, change := range []struct{ id, override string }{{"held", "hold"}, {"first", "next"}, {"second", "next"}, {"first", "next"}} {
		if _, _, err := s.SetUploadOverride(change.id, change.override, "operator"); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"first", "second", "priority"} {
		got := s.AdvanceUploads(time.Second)
		if len(got) != 1 || got[0].ID != want {
			t.Fatalf("want %s, got %+v", want, got)
		}
	}
	if got := s.AdvanceUploads(time.Second); len(got) != 0 {
		t.Fatalf("held item uploaded: %+v", got)
	}
	it, _, err := s.SetUploadOverride("held", "", "operator")
	if err != nil || it.Upload.Override != "" || it.Upload.OverrideBy != "operator" || it.Upload.OverrideAt == nil {
		t.Fatalf("release: %+v %v", it, err)
	}
	if got := s.AdvanceUploads(time.Second); len(got) != 1 || got[0].ID != "held" {
		t.Fatalf("released item: %+v", got)
	}
	it, _, err = s.SetUploadOverride("held", "next", "operator")
	if err != nil || it.Upload.Override != "" {
		t.Fatalf("completed override: %+v %v", it, err)
	}
	if _, _, err := s.SetUploadOverride("held", "invalid", "operator"); err != ErrInvalidUploadOverride {
		t.Fatal(err)
	}
	if _, _, err := s.SetUploadOverride("missing", "hold", "operator"); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestMetadataPassCompletesBeforeImagePass(t *testing.T) {
	s := New(2)
	s.SetLinkBPS(800)
	s.items = []command.Item{
		{ID: "p1", Tier: 1, Upload: command.Upload{Bytes: 1000, MetaDone: true}},
		{ID: "p4", Tier: 4, Upload: command.Upload{Bytes: 1000, MetaBytes: 100}},
	}
	changed := s.AdvanceUploads(time.Second)
	p1, _ := s.GetItem("p1")
	p4, _ := s.GetItem("p4")
	if p4.Upload.MetaSent != 100 || !p4.Upload.MetaDone || p1.Upload.Sent != 0 {
		t.Fatalf("metadata must precede every image: p1=%+v p4=%+v changes=%+v", p1.Upload, p4.Upload, changed)
	}
}

func TestMetadataOnlyStillSendsOverridesWithoutMarkingImage(t *testing.T) {
	s := New(3)
	s.SetLinkBPS(8000)
	s.SetEMCON(true)
	s.items = []command.Item{
		{ID: "held", Tier: 4, Upload: command.Upload{Bytes: 1000, MetaBytes: 100}},
		{ID: "next", Tier: 2, Upload: command.Upload{Bytes: 1000, MetaDone: true, Override: "next"}},
		{ID: "normal", Tier: 1, Upload: command.Upload{Bytes: 1000, MetaDone: true}},
	}
	s.AdvanceUploads(time.Second)
	held, _ := s.GetItem("held")
	next, _ := s.GetItem("next")
	normal, _ := s.GetItem("normal")
	if !held.Upload.MetaDone || held.Upload.Sent != 0 || held.Upload.Done || held.Upload.Received || held.Upload.Active || held.Sent {
		t.Fatalf("metadata delivery changed image/send state: item=%+v", held)
	}
	if next.Upload.Sent == 0 || next.Upload.Done {
		t.Fatalf("next override did not bypass metadata-only mode: %+v", next.Upload)
	}
	if normal.Upload.Sent != 0 || normal.Upload.Done {
		t.Fatalf("normal image bypassed metadata-only mode: %+v", normal.Upload)
	}
}

func TestBackgroundImageRemainsOnShipAfterMetadata(t *testing.T) {
	s := New(1)
	s.SetLinkBPS(800)
	s.items = []command.Item{{ID: "scene", SourceID: "CAM", Background: true, Tier: 4, Upload: command.Upload{Bytes: 1000, MetaBytes: 100}}}
	s.AdvanceUploads(time.Second)
	s.AdvanceUploads(time.Second)
	item, _ := s.GetItem("scene")
	if !item.Upload.MetaDone || item.Upload.Sent != 0 || item.Upload.Done {
		t.Fatalf("background image uploaded automatically: %+v", item.Upload)
	}
}

func TestUploadNextReturnsToPriorityOrder(t *testing.T) {
	s := New(2)
	s.SetLinkBPS(8_000)
	s.items = []command.Item{
		{ID: "critical", Tier: 1, Upload: command.Upload{Bytes: 1000}},
		{ID: "routine", Tier: 3, Upload: command.Upload{Bytes: 1000}},
	}
	s.SetUploadOverride("routine", "next", "operator")
	s.SetUploadOverride("routine", "", "operator")
	got := s.AdvanceUploads(time.Second)
	if len(got) != 1 || got[0].ID != "critical" {
		t.Fatalf("priority order not restored: %+v", got)
	}
}

func TestSeedUploadedMarksOnlyTopP1(t *testing.T) {
	s := New(10)
	for i, manual := range []int{1, 1, 1, 2} {
		s.Add(&command.Item{ID: fmt.Sprint(i), KB: 10, Manual: manual, Dets: []command.Detection{{Conf: 0.5 + 0.1*float64(i)}}}, 0)
	}
	s.SeedUploaded(2)
	items, _, _, _ := s.Snapshot()
	done, minDone, maxQueuedP1 := 0, 2.0, -1.0
	for _, it := range items {
		switch {
		case it.Upload.Done:
			done++
			if it.Tier != 1 || it.Upload.Sent != it.Upload.Bytes {
				t.Fatalf("seeded %+v", it)
			}
			minDone = min(minDone, it.Central)
		case it.Tier == 1:
			maxQueuedP1 = max(maxQueuedP1, it.Central)
		}
	}
	if done != 2 || maxQueuedP1 > minDone {
		t.Fatalf("done=%d, lowest seeded central %.3f, highest queued P1 %.3f", done, minDone, maxQueuedP1)
	}
}

func TestSeedUploadedPrefersCommanderSendsAndCountsBytes(t *testing.T) {
	s := New(10)
	s.Add(&command.Item{ID: "p1", KB: 10, Manual: 1, Dets: []command.Detection{{Conf: 0.9}}}, 0)
	s.Add(&command.Item{ID: "p2", KB: 10, Manual: 2, Dets: []command.Detection{{Conf: 0.9}}}, 0)
	if _, _, ok := s.AutoEscalate(); !ok {
		t.Fatal("nothing escalated")
	}
	s.SeedUploaded(1)
	items, stats, _, _ := s.Snapshot()
	for _, it := range items {
		if it.Upload.Done != it.Sent {
			t.Fatalf("%s: sent=%v but upload done=%v", it.ID, it.Sent, it.Upload.Done)
		}
		if it.Upload.Done && int64(stats.Bytes) != it.Upload.MetaBytes+it.Upload.Bytes {
			t.Fatalf("stats.Bytes %d, want %d", stats.Bytes, it.Upload.MetaBytes+it.Upload.Bytes)
		}
	}
}

func TestSeedUploadedCountsOnlyRemainingBytes(t *testing.T) {
	for _, progress := range []struct{ meta, image int64 }{{0, 0}, {40, 0}, {100, 400}, {100, 1000}} {
		s := New(1)
		s.items = []command.Item{{ID: "seed", Tier: 1, Upload: command.Upload{MetaBytes: 100, MetaSent: progress.meta, Bytes: 1000, Sent: progress.image}}}
		s.stats.Bytes = int(progress.meta + progress.image)
		s.SeedUploaded(1)
		item, _ := s.GetItem("seed")
		stats, _ := s.Stats()
		if stats.Bytes != 1100 || !item.Upload.MetaDone || !item.Upload.Done || item.Upload.MetaSent != 100 || item.Upload.Sent != 1000 {
			t.Fatalf("progress=%+v stats=%+v upload=%+v", progress, stats, item.Upload)
		}
		s.SeedUploaded(1)
		stats, _ = s.Stats()
		if stats.Bytes != 1100 {
			t.Fatalf("reseed double counted: %+v", stats)
		}
	}
	s := New(1)
	s.SeedUploaded(1)
	stats, _ := s.Stats()
	if stats.Bytes != 0 {
		t.Fatalf("empty store counted bytes: %+v", stats)
	}
}
