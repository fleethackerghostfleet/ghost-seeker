package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func hashOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestUpsertManifestValidatesSource(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, now := context.Background(), time.Now()

	for i, source := range []string{`<style>.card{display:none}</style>`, `x"y`} {
		err := st.UpsertManifest(ctx, "edge", &pb.ManifestEntry{ItemId: "reject" + string(rune('a'+i)), Source: source}, now)
		if err == nil {
			t.Errorf("UpsertManifest accepted source %q", source)
		}
	}
	for i, source := range []string{"Submarine_surfacing", ""} {
		err := st.UpsertManifest(ctx, "edge", &pb.ManifestEntry{ItemId: "accept" + string(rune('a'+i)), Source: source}, now)
		if err != nil {
			t.Errorf("UpsertManifest rejected source %q: %v", source, err)
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
	if got, done, err := st.WriteChunk(ctx, &pb.Chunk{ItemId: id, Form: full, Offset: 0, TotalBytes: 100, Data: old[:40], ContentHash: hashOf(old)}, now); err != nil || done || got != 40 {
		t.Fatalf("partial old: got %d done %v err %v", got, done, err)
	}
	if got, done, err := st.WriteChunk(ctx, &pb.Chunk{ItemId: id, Form: full, Offset: 0, TotalBytes: 60, Data: orig[:20], ContentHash: hashOf(orig)}, now); err != nil || done || got != 20 {
		t.Fatalf("first new chunk: got %d done %v err %v, want 20 received", got, done, err)
	}
	if got, done, err := st.WriteChunk(ctx, &pb.Chunk{ItemId: id, Form: full, Offset: 20, TotalBytes: 60, Data: orig[20:], ContentHash: hashOf(orig)}, now); err != nil || !done || got != 60 {
		t.Fatalf("rest of new: got %d done %v err %v", got, done, err)
	}
	if b, err := st.ReadForm(id, full); err != nil || !bytes.Equal(b, orig) {
		t.Fatalf("stored FULL = %q, %v; want the new bytes", b, err)
	}
}
