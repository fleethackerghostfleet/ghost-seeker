package console

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

func TestStatusEvictionCounter(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, id := range []string{"a", "b"} {
		if _, err := st.Put(ctx, &pb.Item{Id: id}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkManifestAcked(ctx, []string{id}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetAcked(ctx, id, pb.FormKind_FORM_KIND_FACT, 1, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetLimits(ctx, store.Limits{MaxItems: 1}); err != nil {
		t.Fatal(err)
	}
	sch, err := scheduler.New(scheduler.DefaultConfig("edge-1"), st, link.NewConnect("http://unused", time.Second, link.NewEstimator(64000, 300, 1e9)), scoring.Default(), clock.Real{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{NodeID: "edge-1", St: st, Sch: sch, Clk: clock.Real{}}
	resp, err := s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.ItemsEvicted != 1 || resp.Msg.ItemsTotal != 1 {
		t.Fatal(resp.Msg)
	}
}
