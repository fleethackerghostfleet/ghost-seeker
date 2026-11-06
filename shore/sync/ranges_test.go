package sync_test

import (
	"context"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

func TestOutOfOrderChunkAcks(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := ssync.New(st, clock.Real{}, nil)
	arrivals := 0
	svc.OnArrival = func(string, pb.FormKind) { arrivals++ }
	for i, part := range []struct {
		offset   int64
		data     string
		received int64
		complete bool
	}{{4, "efgh", 0, false}, {0, "abcd", 8, true}, {4, "efgh", 8, true}} {
		resp, err := svc.Exchange(context.Background(), &pb.ExchangeRequest{EdgeId: "edge", Chunks: []*pb.Chunk{{ItemId: "01M6MSQ0RSB2E2N3ARPS6QMJHT", Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: 8, Offset: part.offset, Data: []byte(part.data)}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Acks) != 1 || resp.Acks[0].AckedThrough != part.received || resp.Acks[0].Complete != part.complete {
			t.Fatalf("exchange %d: %+v", i, resp)
		}
		if i == 0 && arrivals != 0 {
			t.Fatal("arrival emitted before gap filled")
		}
		if i == 1 && arrivals != 1 {
			t.Fatalf("arrival count after completion=%d", arrivals)
		}
	}
}
