package store

import (
	"bytes"
	"context"
	"math"
	"math/big"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func FuzzChunkBounds(f *testing.F) {
	for _, seed := range []struct {
		offset, total int64
		data          []byte
	}{
		{math.MaxInt64 - 2, 1024, []byte("abcd")},
		{1025, 1024, nil}, {2, 4, []byte("abcd")},
		{0, MaxBlobBytes + 1, []byte("a")},
		{0, MaxBlobBytes, make([]byte, MaxChunkBytes+1)},
		{-1, 1024, []byte("a")}, {0, 0, nil},
		{0, 4, []byte("ab")}, {0, 4, []byte("abcd")},
		{2, 4, []byte("ab")}, {4, 4, nil},
	} {
		f.Add(seed.offset, seed.total, seed.data)
	}
	f.Fuzz(func(t *testing.T, offset, total int64, data []byte) {
		end := new(big.Int).Add(big.NewInt(offset), new(big.Int).SetUint64(uint64(len(data))))
		valid := offset >= 0 && total > 0 && total <= MaxBlobBytes && len(data) <= MaxChunkBytes && end.Cmp(big.NewInt(total)) <= 0
		st := &Store{}
		if valid {
			var err error
			st, err = Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
		}
		chunk := &pb.Chunk{ItemId: "01M6MSQ0RSB2E2N3ARPS6QMJHT", Form: pb.FormKind_FORM_KIND_FULL, Offset: offset, TotalBytes: total, Data: data}
		received, done, err := st.WriteChunk(context.Background(), "edge-1", chunk, time.Unix(1700000000, 0))
		if !valid {
			if err == nil || received != 0 || done {
				t.Fatal("out-of-bounds chunk was not rejected before storage")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if offset == 0 {
			want = int64(len(data))
		}
		if received != want || received < 0 || received > total || done != (received == total) {
			t.Fatalf("invalid fresh-chunk acknowledgement: received=%d done=%t total=%d want=%d", received, done, total, want)
		}
		if done {
			raw, err := st.ReadForm(chunk.ItemId, chunk.Form)
			if err != nil || !bytes.Equal(raw, data) {
				t.Fatal("completed chunk payload did not round trip")
			}
		}
	})
}
