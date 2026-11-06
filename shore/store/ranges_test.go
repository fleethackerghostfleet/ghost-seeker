package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

const rangeItem = "01M6MSQ0RSB2E2N3ARPS6QMJHT"

func writeRange(t *testing.T, st *Store, data []byte, start, end, want int64, done bool) {
	t.Helper()
	received, complete, err := st.WriteChunk(context.Background(), "edge", &pb.Chunk{
		ItemId: rangeItem, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: int64(len(data)),
		Offset: start, Data: data[start:end], ContentHash: hashOf(data),
	}, time.Now())
	if err != nil || received != want || complete != done {
		t.Fatalf("[%d,%d): received=%d complete=%v err=%v; want %d/%v", start, end, received, complete, err, want, done)
	}
	it, err := st.Get(context.Background(), rangeItem)
	if err != nil {
		t.Fatal(err)
	}
	f := it.Form(pb.FormKind_FORM_KIND_FULL)
	if f == nil || f.Received != want || f.Complete != done {
		t.Fatalf("stored state: %+v", f)
	}
}

func TestChunkRanges(t *testing.T) {
	for name, steps := range map[string][][3]int64{
		"reverse":                       {{8, 12, 0}, {4, 8, 0}, {0, 4, 12}},
		"gap-fill":                      {{0, 2, 2}, {6, 12, 2}, {2, 6, 12}},
		"overlap-duplicate-interleaved": {{4, 8, 0}, {4, 8, 0}, {6, 10, 0}, {0, 2, 2}, {10, 12, 2}, {1, 5, 12}},
		"empty-does-not-fill-gap":       {{4, 12, 0}, {0, 0, 0}, {0, 4, 12}},
	} {
		t.Run(name, func(t *testing.T) {
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			data := []byte("abcdefghijkl")
			for _, step := range steps {
				writeRange(t, st, data, step[0], step[1], step[2], step[2] == int64(len(data)))
			}
			got, err := st.ReadForm(rangeItem, pb.FormKind_FORM_KIND_FULL)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("blob=%q, err=%v", got, err)
			}
			writeRange(t, st, data, 0, 2, 12, true)
		})
	}
}

func TestChunkRangesReverse64K(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	data := bytes.Repeat([]byte("abcd"), 32<<10)
	writeRange(t, st, data, 64<<10, 128<<10, 0, false)
	writeRange(t, st, data, 0, 64<<10, 128<<10, true)
}

func TestChunkRangesContentChange(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "complete"}[completed], func(t *testing.T) {
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			old := []byte("abcdefghijkl")
			writeRange(t, st, old, 8, 12, 0, false)
			writeRange(t, st, old, 0, 4, 4, false)
			if completed {
				writeRange(t, st, old, 4, 8, 12, true)
			}
			fresh := []byte("ABCDEFGHIJKL")
			writeRange(t, st, fresh, 4, 8, 0, false)
			writeRange(t, st, fresh, 0, 4, 8, false)
			writeRange(t, st, fresh, 8, 12, 12, true)
		})
	}
}

func TestChunkRangesHashMismatchReset(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	data := []byte("abcdefghijkl")
	writeRange(t, st, data, 8, 12, 0, false)
	recv, done, err := st.WriteChunk(context.Background(), "edge", &pb.Chunk{ItemId: rangeItem, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 12, Data: []byte("XXXXXXXX"), ContentHash: hashOf(data)}, time.Now())
	if err == nil || recv != 0 || done {
		t.Fatalf("corruption: %d/%v/%v", recv, done, err)
	}
	var raw string
	if err := st.db.QueryRow(`SELECT received_ranges FROM forms WHERE item_id=?`, rangeItem).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "[]" {
		t.Fatalf("ranges not reset: %s", raw)
	}
	writeRange(t, st, data, 0, 8, 8, false)
	writeRange(t, st, data, 8, 12, 12, true)
}

func TestChunkRangesPersist(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("abcdefghijkl")
	writeRange(t, st, data, 8, 12, 0, false)
	writeRange(t, st, data, 0, 4, 4, false)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	writeRange(t, st, data, 4, 8, 12, true)
}

func TestChunkRangesRegeneratedHashMismatch(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	writeRange(t, st, []byte("abcdefghijkl"), 0, 12, 12, true)
	fresh := []byte("ABCDEFGHIJKL")
	_, done, err := st.WriteChunk(context.Background(), "edge", &pb.Chunk{ItemId: rangeItem, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 12, Data: []byte("XXXXXXXXXXXX"), ContentHash: hashOf(fresh)}, time.Now())
	if err == nil || done {
		t.Fatalf("corrupt regeneration: complete=%v err=%v", done, err)
	}
	writeRange(t, st, fresh, 8, 12, 0, false)
	writeRange(t, st, fresh, 0, 8, 12, true)
}

func TestChunkRangesMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "shore.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE forms (
	 item_id TEXT NOT NULL, kind INTEGER NOT NULL, total INTEGER NOT NULL,
	 received INTEGER NOT NULL DEFAULT 0, complete INTEGER NOT NULL DEFAULT 0,
	 hash TEXT NOT NULL DEFAULT '', mime TEXT NOT NULL DEFAULT '',
	 first_at INTEGER NOT NULL DEFAULT 0, complete_at INTEGER NOT NULL DEFAULT 0,
	 PRIMARY KEY(item_id,kind));`)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("abcdefghijkl")
	_, err = db.Exec(`INSERT INTO forms(item_id,kind,total,received,hash) VALUES(?,?,?,?,?)`, rangeItem, int(pb.FormKind_FORM_KIND_FULL), 12, 4, hashOf(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate bytes represented by the legacy contiguous prefix.
	writeRange(t, st, data, 0, 4, 4, false)
	writeRange(t, st, data, 8, 12, 4, false)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	writeRange(t, st, data, 4, 8, 12, true)
}

func TestChunkRangesOverflowFallsBackToPrefix(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	data := bytes.Repeat([]byte("x"), 2*maxReceivedRanges+4)
	writeRange(t, st, data, 0, 1, 1, false)
	for i := range maxReceivedRanges + 1 {
		start := int64(2*i + 2)
		writeRange(t, st, data, start, start+1, 1, false)
	}
	var raw string
	if err := st.db.QueryRow(`SELECT received_ranges FROM forms WHERE item_id=?`, rangeItem).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var ranges [][2]int64
	if err := json.Unmarshal([]byte(raw), &ranges); err != nil || len(ranges) != 0 {
		t.Fatalf("overflow ranges=%s err=%v", raw, err)
	}
	writeRange(t, st, data, 1, int64(len(data)), int64(len(data)), true)
}
