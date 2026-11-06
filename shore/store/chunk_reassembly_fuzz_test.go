package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// Mark bytes independently of mergeReceived's sorted intervals and prefix.
type chunkCoverageOracle []bool

func (o chunkCoverageOracle) prefix() int64 {
	for i, covered := range o {
		if !covered {
			return int64(i)
		}
	}
	return int64(len(o))
}

func chunkSequenceSeed(size int, reopen byte, chunks ...[3]int) []byte {
	n := size - 1
	seed := []byte{byte(n), byte(n >> 8), byte(n >> 16), reopen}
	for _, chunk := range chunks {
		frame := make([]byte, 9)
		frame[0] = byte(chunk[0])
		binary.LittleEndian.PutUint32(frame[1:5], uint32(chunk[1]))
		binary.LittleEndian.PutUint32(frame[5:9], uint32(chunk[2]))
		seed = append(seed, frame...)
	}
	return seed
}

func FuzzChunkReassembly(f *testing.F) {
	// Orders from TestChunkRanges, TestChunkRangesPersist and the 64 KiB
	// reverse transfer. The header's high bit enables reopening.
	f.Add(chunkSequenceSeed(12, 0x81, [3]int{0, 8, 4}, [3]int{0, 4, 4}, [3]int{0, 0, 4}))
	f.Add(chunkSequenceSeed(12, 0, [3]int{0, 0, 2}, [3]int{0, 6, 6}, [3]int{0, 2, 4}))
	f.Add(chunkSequenceSeed(12, 0x82, [3]int{0, 4, 4}, [3]int{1, 4, 4}, [3]int{0, 6, 4}, [3]int{0, 0, 2}, [3]int{0, 10, 2}, [3]int{0, 1, 4}))
	f.Add(chunkSequenceSeed(12, 0, [3]int{0, 4, 8}, [3]int{0, 0, 0}, [3]int{0, 0, 4}))
	f.Add(chunkSequenceSeed(12, 0x81, [3]int{0, 8, 4}, [3]int{0, 0, 4}, [3]int{0, 4, 4}))
	f.Add(chunkSequenceSeed(128<<10, 0x80, [3]int{0, 64 << 10, 64 << 10}, [3]int{0, 0, 64 << 10}))
	f.Add(chunkSequenceSeed(12, 0x81, [3]int{0, 0, 2}, [3]int{3, 0, 0}, [3]int{4, 0, 0}, [3]int{5, 0, 0}, [3]int{6, 0, 0}, [3]int{0, 2, 10}))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, input []byte) {
		header := [4]byte{}
		copy(header[:], input)
		size := 1 + int(uint32(header[0])|uint32(header[1])<<8|uint32(header[2])<<16)%(128<<10)
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i*31 + 7)
			if len(input) > 4 {
				payload[i] ^= input[4+i%(len(input)-4)]
			}
		}
		hash := hashOf(payload)
		ctx, now := context.Background(), time.Unix(1700000000, 0)
		dir := t.TempDir()
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if st != nil {
				if err := st.Close(); err != nil {
					t.Error(err)
				}
			}
		})
		id, form := rangeItem, pb.FormKind_FORM_KIND_FULL
		partial := filepath.Join(st.blobDir, id, fmt.Sprintf("%d.partial", form))
		coverage := make(chunkCoverageOracle, size)
		haveValidWrite := false
		type formSnapshot struct {
			Total, Received int64
			Complete        int
			Ranges, Hash    string
		}
		snapshot := func() formSnapshot {
			t.Helper()
			var state formSnapshot
			err := st.db.QueryRowContext(ctx, `SELECT total,received,complete,received_ranges,hash FROM forms WHERE item_id=? AND kind=?`, id, int(form)).
				Scan(&state.Total, &state.Received, &state.Complete, &state.Ranges, &state.Hash)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
			return state
		}
		write := func(offset int64, data []byte) {
			t.Helper()
			// Data is at most 128 KiB + 1, so adding to a nonnegative
			// int64 offset cannot overflow this independent uint64 oracle.
			valid := offset >= 0 && uint64(offset)+uint64(len(data)) <= uint64(size)
			var beforeState formSnapshot
			var beforeBytes []byte
			var beforeErr error
			if !valid {
				beforeState = snapshot()
				beforeBytes, beforeErr = os.ReadFile(partial)
			}
			ack, done, err := st.WriteChunk(ctx, "edge", &pb.Chunk{
				ItemId: id, Form: form, TotalBytes: int64(size), Offset: offset,
				Data: data, ContentHash: hash,
			}, now)
			if valid {
				if err != nil {
					t.Fatalf("valid write offset=%d len=%d: %v", offset, len(data), err)
				}
				for i := int(offset); i < int(offset)+len(data); i++ {
					coverage[i] = true
				}
				haveValidWrite = true
			} else {
				if err == nil {
					t.Fatalf("accepted invalid write offset=%d len=%d", offset, len(data))
				}
				afterBytes, afterErr := os.ReadFile(partial)
				if snapshot() != beforeState || (beforeErr == nil) != (afterErr == nil) || !bytes.Equal(beforeBytes, afterBytes) {
					t.Fatalf("invalid write changed persisted state: offset=%d len=%d", offset, len(data))
				}
			}
			prefix := coverage.prefix()
			complete := prefix == int64(size)
			if valid && (ack != prefix || done != complete) {
				t.Fatalf("write offset=%d len=%d ack=%d/%v, oracle=%d/%v", offset, len(data), ack, done, prefix, complete)
			}
			item, err := st.Get(ctx, id)
			if !haveValidWrite {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("invalid first write created item state: item=%+v err=%v", item, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				state := item.Form(form)
				if state == nil || state.Total != int64(size) || state.Received != prefix || state.Complete != complete {
					t.Fatalf("persisted form=%+v, oracle prefix=%d complete=%v", state, prefix, complete)
				}
			}
			blob, err := st.ReadForm(id, form)
			if complete {
				if err != nil || !bytes.Equal(blob, payload) {
					t.Fatalf("completed bytes differ from payload: %v", err)
				}
				if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial remains after completion: %v", err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete union published a blob: %v", err)
			}
		}

		steps := 0
		if len(input) >= 4 {
			// Stay below the intentional 256-range fallback, where exact
			// union acknowledgements are explicitly no longer promised.
			steps = min((len(input)-4)/9, 64)
		}
		var previousOffset int64
		var previousData []byte
		for i := 0; i < steps; i++ {
			frame := input[4+i*9 : 4+(i+1)*9]
			offset := int64(binary.LittleEndian.Uint32(frame[1:5]) % uint32(size+1))
			length := int(binary.LittleEndian.Uint32(frame[5:9]) % uint32(size-int(offset)+1))
			data := payload[offset : offset+int64(length)]
			switch frame[0] % 7 {
			case 1:
				if previousData != nil {
					offset, data = previousOffset, previousData
				}
			case 3:
				offset, data = int64(size+1), []byte{0}
			case 4:
				data = make([]byte, size+1)
			case 5:
				offset, data = math.MaxInt64-2, []byte{0, 1, 2, 3}
			case 6:
				offset, data = -1, []byte{0}
			}
			write(offset, data)
			if offset >= 0 && uint64(offset)+uint64(len(data)) <= uint64(size) {
				previousOffset, previousData = offset, data
			}
			if header[3]&0x80 != 0 && i == int(header[3]&0x7f)%steps {
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = Open(dir)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		// Fill only holes: persisted sparse ranges must still be remembered
		// for the last acknowledgement to complete.
		for i := 0; i < size; {
			if coverage[i] {
				i++
				continue
			}
			start := i
			for i < size && !coverage[i] {
				i++
			}
			write(int64(start), payload[start:i])
		}
		write(math.MaxInt64-2, []byte{0, 1, 2, 3})
		write(0, payload[:min(size, 2)])
	})
}
