package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestItemOwnership(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id := "01M6MSQ0RSB2E2N3ARPS6QMJHT"
	full := pb.FormKind_FORM_KIND_FULL
	original := []byte("AAAAAA")
	originalHash := hashOf(original)
	manifest := func(priority float64, label string) *pb.ManifestEntry {
		return &pb.ManifestEntry{ItemId: id, Type: pb.ItemType_ITEM_TYPE_IMAGE, Priority: priority,
			TopLabel: label, Forms: []*pb.FormSize{{Kind: full, Bytes: int64(len(original))}}}
	}
	if err := st.UpsertManifest(ctx, "edge-1", manifest(0.9, "warship"), now); err != nil {
		t.Fatal(err)
	}
	if got, complete, err := st.WriteChunk(ctx, "edge-1", &pb.Chunk{ItemId: id, Form: full, TotalBytes: int64(len(original)),
		Data: original, ContentHash: originalHash}, now); err != nil || !complete || got != int64(len(original)) {
		t.Fatalf("edge-1 chunk: received=%d complete=%v err=%v", got, complete, err)
	}

	if err := st.UpsertManifest(ctx, "edge-2", manifest(0.01, "buoy"), now); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("edge-2 manifest error = %v, want ErrNotOwner", err)
	}
	item, err := st.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if item.EdgeID != "edge-1" || item.Manifest.Priority != 0.9 || item.Manifest.TopLabel != "warship" {
		t.Fatalf("manifest changed after rejected write: owner=%q priority=%v label=%q",
			item.EdgeID, item.Manifest.Priority, item.Manifest.TopLabel)
	}
	attacker := []byte("BBBBBB")
	attackerHash := sha256.Sum256(attacker)
	if _, _, err := st.WriteChunk(ctx, "edge-2", &pb.Chunk{ItemId: id, Form: full, TotalBytes: int64(len(attacker)),
		Data: attacker, ContentHash: hex.EncodeToString(attackerHash[:])}, now); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("edge-2 chunk error = %v, want ErrNotOwner", err)
	}
	stored, err := st.ReadForm(id, full)
	if err != nil || !bytes.Equal(stored, original) {
		t.Fatalf("stored FULL = %q, err=%v; want original bytes", stored, err)
	}
	item, err = st.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if form := item.Form(full); form == nil || !form.Complete {
		t.Fatalf("FULL form state = %+v, want complete", form)
	}
	if err := st.SetOverrideFrom(ctx, "edge-2", id, "buoy", "edge-2"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("edge-2 override error = %v, want ErrNotOwner", err)
	}
	item, err = st.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if item.LabelOverride != "" {
		t.Fatalf("rejected override set label %q", item.LabelOverride)
	}
	if err := st.SetOverride(ctx, id, "operator label", "operator"); err != nil {
		t.Fatalf("operator override: %v", err)
	}
	if err := st.SetOverrideFrom(ctx, "edge-1", id, "edge label", "edge-1"); err != nil {
		t.Fatalf("owner override: %v", err)
	}
	if err := st.UpsertManifest(ctx, "edge-1", manifest(0.8, "warship"), now); err != nil {
		t.Fatalf("owner re-manifest: %v", err)
	}
	item, err = st.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if item.Manifest.Priority != 0.8 || item.LabelOverride != "edge label" {
		t.Fatalf("owner update not retained: priority=%v override=%q", item.Manifest.Priority, item.LabelOverride)
	}

	stubID := "01M6MSQ0RSB2E2N3ARPS6QMJHV"
	stubBytes := []byte("stub")
	if _, _, err := st.WriteChunk(ctx, "edge-2", &pb.Chunk{ItemId: stubID, Form: full,
		TotalBytes: int64(len(stubBytes)), Data: stubBytes}, now); err != nil {
		t.Fatalf("new stub chunk: %v", err)
	}
	stub, err := st.Get(ctx, stubID)
	if err != nil || stub.EdgeID != "edge-2" {
		t.Fatalf("stub owner = %v, err=%v; want edge-2", stub, err)
	}
	if err := st.UpsertManifest(ctx, "edge-1", &pb.ManifestEntry{ItemId: stubID}, now); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("other edge manifest for owned stub error = %v, want ErrNotOwner", err)
	}
}

func TestLegacyUnownedItemAdoption(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := "01M6MSQ0RSB2E2N3ARPS6QMJHW"
	if _, err := st.db.ExecContext(ctx, `INSERT INTO items(id,edge_id,type,captured_at,source,manifest,manifest_at)
		VALUES(?, '', 0, ?, '', x'', 0)`, id, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, id); err != nil {
		t.Fatalf("legacy item Get: %v", err)
	}
	if err := st.SetOverrideFrom(ctx, "edge-3", id, "legacy label", "edge-3"); err != nil {
		t.Fatalf("legacy override: %v", err)
	}
	if err := st.UpsertManifest(ctx, "edge-1", &pb.ManifestEntry{ItemId: id, Priority: 0.5}, now); err != nil {
		t.Fatalf("legacy manifest adoption: %v", err)
	}
	item, err := st.Get(ctx, id)
	if err != nil || item.EdgeID != "edge-1" {
		t.Fatalf("adopted owner = %v, err=%v; want edge-1", item, err)
	}
	if _, _, err := st.WriteChunk(ctx, "edge-2", &pb.Chunk{ItemId: id, Form: pb.FormKind_FORM_KIND_FULL,
		TotalBytes: 1, Data: []byte("x")}, now); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("post-adoption other edge chunk error = %v, want ErrNotOwner", err)
	}
}

func TestEmptyEdgeCannotWriteItems(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := "01M6MSQ0RSB2E2N3ARPS6QMJHT"
	if err := st.UpsertManifest(ctx, "", &pb.ManifestEntry{ItemId: id}, now); !errors.Is(err, ErrNotOwner) {
		t.Errorf("empty-edge manifest error = %v, want ErrNotOwner", err)
	}
	if _, _, err := st.WriteChunk(ctx, "", &pb.Chunk{ItemId: id, Form: pb.FormKind_FORM_KIND_FULL,
		TotalBytes: 1, Data: []byte("x")}, now); !errors.Is(err, ErrNotOwner) {
		t.Errorf("empty-edge chunk error = %v, want ErrNotOwner", err)
	}
	if err := st.SetOverrideFrom(ctx, "", id, "label", ""); !errors.Is(err, ErrNotOwner) {
		t.Errorf("empty-edge override error = %v, want ErrNotOwner", err)
	}
}
