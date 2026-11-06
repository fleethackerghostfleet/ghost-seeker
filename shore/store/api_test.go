package store

import (
	"context"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestAPIJournalReceivedOrderAndRestart(t *testing.T) {
	dir, ctx := t.TempDir(), context.Background()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	now := time.Now()
	data := []byte("partial-payload-before-manifest")
	_, _, err = st.WriteChunk(ctx, "edge", &pb.Chunk{ItemId: "first", Form: pb.FormKind_FORM_KIND_THUMB, Data: data, TotalBytes: int64(len(data))}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rs, err := st.Receipts(ctx, 0, "", 10); err != nil || len(rs) != 0 {
		t.Fatalf("placeholder exposed: %+v, %v", rs, err)
	}
	first := &pb.ManifestEntry{ItemId: "first", Source: "camera", Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: 10000, Priority: 1}
	if err := st.UpsertManifest(ctx, "edge", first, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManifest(ctx, "edge", &pb.ManifestEntry{ItemId: "second", CapturedAtUnixMs: 1, Priority: 2}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	rs, err := st.Receipts(ctx, 0, "", 10)
	if err != nil || len(rs) != 2 || rs[0].ID != "first" || rs[0].Rank != 2 || rs[1].ID != "second" {
		t.Fatalf("receipt order/rank: %+v, %v", rs, err)
	}
	seq := rs[0].Sequence
	item, err := st.Get(ctx, "first")
	if err != nil || item.Manifest.Source != "camera" || !item.ManifestAt.Equal(time.UnixMilli(now.Add(time.Second).UnixMilli())) {
		t.Fatalf("late manifest lost metadata: %+v, %v", item, err)
	}
	events, err := st.Events(ctx, 0, 10)
	if err != nil || len(events) != 3 || events[0].Kind != "created" {
		t.Fatalf("events: %+v, %v", events, err)
	}
	resume, _ := st.EventCursor(ctx)
	if err := st.UpsertManifest(ctx, "edge", first, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := st.EventCursor(ctx); cursor != resume {
		t.Fatal("unchanged manifest produced an event")
	}
	first.Priority = 3
	if err := st.UpsertManifest(ctx, "edge", first, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	r, err := st.Receipt(ctx, "first")
	if err != nil || r.Sequence != seq || r.Rank != 1 {
		t.Fatalf("update moved receipt: %+v, %v", r, err)
	}
	item, _ = st.Get(ctx, "first")
	if !item.ManifestAt.Equal(time.UnixMilli(now.Add(time.Second).UnixMilli())) {
		t.Fatal("update changed first receipt time")
	}
	cursor, _ := st.EventCursor(ctx)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if next, _ := st.EventCursor(ctx); next != cursor {
		t.Fatal("restart changed event cursor")
	}
	events, err = st.Events(ctx, resume, 10)
	updated := map[string]bool{}
	for _, e := range events {
		if e.Kind == "updated" {
			updated[e.ID] = true
		}
	}
	if err != nil || !updated["first"] || !updated["second"] {
		t.Fatalf("restart/resume lost journal: %+v, %v", events, err)
	}
	rs, err = st.Receipts(ctx, 0, "", 10)
	if err != nil || len(rs) != 2 || rs[0].Sequence != seq {
		t.Fatal("restart moved receipt cursors", rs, err)
	}
}
