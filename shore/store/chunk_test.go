package store

import (
	"context"
	"math"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestWriteChunkBounds(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := "01M6MSQ0RSB2E2N3ARPS6QMJHT"
	now := time.Now()
	for name, c := range map[string]*pb.Chunk{
		"offset overflow": {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 1024, Offset: math.MaxInt64 - 2, Data: []byte("abcd")},
		"offset past end": {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 1024, Offset: 1025, Data: nil},
		"len past end":    {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 4, Offset: 2, Data: []byte("abcd")},
		"total too large": {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: MaxBlobBytes + 1, Offset: 0, Data: []byte("a")},
		"chunk too large": {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: MaxBlobBytes, Offset: 0, Data: make([]byte, MaxChunkBytes+1)},
		"negative offset": {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 1024, Offset: -1, Data: []byte("a")},
		"zero total":      {ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 0, Offset: 0, Data: nil},
	} {
		if _, _, err := st.WriteChunk(context.Background(), c, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := st.WriteChunk(context.Background(), &pb.Chunk{ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 4, Offset: 0, Data: []byte("ab")}, now); err != nil {
		t.Fatalf("valid chunk: %v", err)
	}
}
