package store

import (
	"context"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShoreItemLimitCoversChunkStubsAndManifests(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.maxItems = 1
	ctx, now := context.Background(), time.Now()
	if err := st.UpsertManifest(ctx, "edge", &pb.ManifestEntry{ItemId: "first"}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManifest(ctx, "edge", &pb.ManifestEntry{ItemId: "second"}, now); err == nil {
		t.Fatal("manifest exceeded item cap")
	}
	chunk := &pb.Chunk{ItemId: "second", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 1, Data: []byte("x")}
	if _, _, err := st.WriteChunk(ctx, "edge", chunk, now); err == nil {
		t.Fatal("chunk stub exceeded item cap")
	}
	chunk.ItemId = "first"
	if _, done, err := st.WriteChunk(ctx, "edge", chunk, now); err != nil || !done {
		t.Fatalf("existing item should complete: %v", err)
	}
}

func TestShoreBlobQuotaPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.blobQuota = 4
	chunk := &pb.Chunk{ItemId: "first", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 3, Data: []byte("abc")}
	if _, _, err := st.WriteChunk(context.Background(), "edge", chunk, time.Now()); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.blobQuota = 4
	chunk.ItemId = "second"
	if _, _, err := st.WriteChunk(context.Background(), "edge", chunk, time.Now()); err == nil {
		t.Fatal("disk quota bypassed after restart")
	}
	chunk.Data = []byte("x")
	chunk.TotalBytes = 1
	if _, _, err := st.WriteChunk(context.Background(), "edge", chunk, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st.blobBytes != 4 {
		t.Fatalf("wrong disk accounting: %d", st.blobBytes)
	}
}

func TestHashMismatchRemovesLargePartial(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chunk := &pb.Chunk{ItemId: "corrupt", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 32, Data: make([]byte, 32), ContentHash: hashOf([]byte("wrong"))}
	if _, _, err := st.WriteChunk(context.Background(), "edge", chunk, time.Now()); err == nil {
		t.Fatal("corrupt hash accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", "corrupt", "4.partial")); !os.IsNotExist(err) {
		t.Fatalf("large partial retained for repeated tiny-chunk rehash: %v", err)
	}
	if st.blobBytes != 0 {
		t.Fatalf("corrupt bytes still accounted: %d", st.blobBytes)
	}
}

func TestShoreInvalidLimitsFailClosed(t *testing.T) {
	for _, name := range []string{"GS_SHORE_MAX_ITEMS", "GS_SHORE_MAX_BLOB_BYTES"} {
		t.Setenv(name, "-1")
		if st, err := Open(t.TempDir()); err == nil {
			st.Close()
			t.Fatalf("invalid %s accepted", name)
		}
		t.Setenv(name, "")
	}
}

func TestShoreChunkMetadataBounded(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chunk := &pb.Chunk{ItemId: "metadata", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 1, Data: []byte("x"), Mime: strings.Repeat("m", 257)}
	if _, _, err := st.WriteChunk(context.Background(), "edge", chunk, time.Now()); err == nil {
		t.Fatal("oversized chunk metadata accepted")
	}
	chunk.Mime = "image/jpeg"
	chunk.ContentHash = strings.Repeat("h", 65)
	chunk.TotalBytes = 2
	if _, _, err := st.WriteChunk(context.Background(), "edge", chunk, time.Now()); err == nil {
		t.Fatal("oversized hash accepted")
	}
}

func TestShoreManifestMetadataBounded(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entry := &pb.ManifestEntry{ItemId: "metadata", TopLabel: strings.Repeat("m", 64<<10)}
	if err := st.UpsertManifest(context.Background(), "edge", entry, time.Now()); err == nil {
		t.Fatal("oversized manifest accepted")
	}
	entry.TopLabel = ""
	entry.Forms = []*pb.FormSize{{Kind: pb.FormKind(999), Bytes: 1}}
	if err := st.UpsertManifest(context.Background(), "edge", entry, time.Now()); err == nil {
		t.Fatal("unbounded form kinds accepted")
	}
}

func TestShoreBlobQuotaAllowsFormReplacement(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.blobQuota = 4
	ctx, now := context.Background(), time.Now()
	chunk := &pb.Chunk{ItemId: "item", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 4, Data: []byte("abcd"), ContentHash: hashOf([]byte("abcd"))}
	if _, done, err := st.WriteChunk(ctx, "edge", chunk, now); err != nil || !done {
		t.Fatalf("first form: %v", err)
	}
	chunk.Data, chunk.ContentHash = []byte("wxyz"), hashOf([]byte("wxyz"))
	if _, done, err := st.WriteChunk(ctx, "edge", chunk, now); err != nil || !done {
		t.Fatalf("replacement rejected at full quota: %v", err)
	}
	if st.blobBytes != 4 {
		t.Fatalf("wrong disk accounting: %d", st.blobBytes)
	}
	other := &pb.Chunk{ItemId: "other", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 1, Data: []byte("x")}
	if _, _, err := st.WriteChunk(ctx, "edge", other, now); err == nil {
		t.Fatal("quota bypassed after replacement")
	}
}
