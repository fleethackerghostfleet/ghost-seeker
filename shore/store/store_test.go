package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

func hashOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestUpsertManifestNormalizesInvalidSource(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, now := context.Background(), time.Now()

	for i, source := range []string{`<style>.card{display:none}</style>`, `x"y`, `cam 1`} {
		entry := &pb.ManifestEntry{ItemId: "invalid" + string(rune('a'+i)), Source: source}
		if err := st.UpsertManifest(ctx, "edge", entry, now); err != nil {
			t.Fatalf("UpsertManifest rejected source %q: %v", source, err)
		}
		if entry.Source != source {
			t.Fatalf("UpsertManifest mutated caller source to %q, want %q", entry.Source, source)
		}
		var storedSource string
		var raw []byte
		if err := st.db.QueryRow("SELECT source, manifest FROM items WHERE id=?", entry.ItemId).Scan(&storedSource, &raw); err != nil {
			t.Fatal(err)
		}
		if storedSource != "" {
			t.Errorf("stored source = %q, want empty", storedSource)
		}
		var stored pb.ManifestEntry
		if err := proto.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Source != "" {
			t.Errorf("stored manifest source = %q, want empty", stored.Source)
		}
	}
	for i, source := range []string{"Submarine_surfacing", ""} {
		entry := &pb.ManifestEntry{ItemId: "accept" + string(rune('a'+i)), Source: source}
		if err := st.UpsertManifest(ctx, "edge", entry, now); err != nil {
			t.Fatalf("UpsertManifest rejected source %q: %v", source, err)
		}
		var storedSource string
		var raw []byte
		if err := st.db.QueryRow("SELECT source, manifest FROM items WHERE id=?", entry.ItemId).Scan(&storedSource, &raw); err != nil {
			t.Fatal(err)
		}
		if storedSource != source {
			t.Errorf("stored source = %q, want %q", storedSource, source)
		}
		var stored pb.ManifestEntry
		if err := proto.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Source != source {
			t.Errorf("stored manifest source = %q, want %q", stored.Source, source)
		}
	}
}

// A form regenerated mid-transfer (FULL after an original pull) restarts
// from zero instead of mixing old and new bytes.
func TestWriteChunkRestartsOnNewHashMidTransfer(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, now := context.Background(), time.Now()
	id := "0123456789abcdef0123456789abcdef"
	old := bytes.Repeat([]byte("j"), 100)
	orig := bytes.Repeat([]byte("p"), 60)
	full := pb.FormKind_FORM_KIND_FULL
	if got, done, err := st.WriteChunk(ctx, "edge-1", &pb.Chunk{ItemId: id, Form: full, Offset: 0, TotalBytes: 100, Data: old[:40], ContentHash: hashOf(old)}, now); err != nil || done || got != 40 {
		t.Fatalf("partial old: got %d done %v err %v", got, done, err)
	}
	if got, done, err := st.WriteChunk(ctx, "edge-1", &pb.Chunk{ItemId: id, Form: full, Offset: 0, TotalBytes: 60, Data: orig[:20], ContentHash: hashOf(orig)}, now); err != nil || done || got != 20 {
		t.Fatalf("first new chunk: got %d done %v err %v, want 20 received", got, done, err)
	}
	if got, done, err := st.WriteChunk(ctx, "edge-1", &pb.Chunk{ItemId: id, Form: full, Offset: 20, TotalBytes: 60, Data: orig[20:], ContentHash: hashOf(orig)}, now); err != nil || !done || got != 60 {
		t.Fatalf("rest of new: got %d done %v err %v", got, done, err)
	}
	if b, err := st.ReadForm(id, full); err != nil || !bytes.Equal(b, orig) {
		t.Fatalf("stored FULL = %q, %v; want the new bytes", b, err)
	}
}

func TestReplacementFormKeepsCompletedReadersImmutable(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, now := context.Background(), time.Now()
	old, next := []byte("old-complete-image"), []byte("replacement-complete-image")
	id, kind := "replace", pb.FormKind_FORM_KIND_FULL
	if _, done, err := st.WriteChunk(ctx, "edge", &pb.Chunk{ItemId: id, Form: kind, TotalBytes: int64(len(old)), Data: old, ContentHash: hashOf(old)}, now); err != nil || !done {
		t.Fatal(done, err)
	}
	observed, err := st.Get(ctx, id)
	if err != nil || !observed.Form(kind).Complete {
		t.Fatal("precondition", err)
	}
	first := &pb.Chunk{ItemId: id, Form: kind, TotalBytes: int64(len(next)), Data: next[:8], ContentHash: hashOf(next)}
	if _, done, err := st.WriteChunk(ctx, "edge", first, now); err != nil || done {
		t.Fatal(done, err)
	}
	// An API reader can have observed the old complete flag before the reset.
	// Even if it opens the file now, it must see a whole committed version.
	reader, err := st.OpenForm(id, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if b, err := st.ReadForm(id, kind); err != nil || !bytes.Equal(b, old) {
		t.Fatal("published partial replacement", string(b), err)
	}
	state, _ := st.Get(ctx, id)
	if state.Form(kind).Complete {
		t.Fatal("replacement is not complete yet")
	}
	last := &pb.Chunk{ItemId: id, Form: kind, Offset: 8, TotalBytes: int64(len(next)), Data: next[8:], ContentHash: hashOf(next)}
	if _, done, err := st.WriteChunk(ctx, "edge", last, now); err != nil || !done {
		t.Fatal(done, err)
	}
	if b, err := st.ReadForm(id, kind); err != nil || !bytes.Equal(b, next) {
		t.Fatal("complete replacement not published", string(b), err)
	}
	if b, err := io.ReadAll(reader); err != nil || !bytes.Equal(b, old) {
		t.Fatal("open reader mutated during replacement", string(b), err)
	}
}
