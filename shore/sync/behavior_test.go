package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	"google.golang.org/protobuf/proto"
)

func behaviorService(t *testing.T) (*Service, *store.Store, *clock.Virtual, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewVirtual(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	log := &bytes.Buffer{}
	return New(st, clk, slog.New(slog.NewTextHandler(log, nil))), st, clk, log
}

func TestExchangeFactCompletionAndHeartbeat(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "valid FACT"
		if malformed {
			name = "malformed FACT"
		}
		t.Run(name, func(t *testing.T) {
			svc, st, clk, log := behaviorService(t)
			ctx := context.Background()
			f := &fact.Fact{ID: "fact-item", Labels: []fact.Label{{Label: "boat", Conf: 0.8, Box: []float32{0.1, 0.2, 0.3, 0.4}}}, Unknown: 0.25, Model: "sensor-model"}
			raw := f.Marshal()
			if malformed {
				raw = []byte(`{"labels":`)
			}
			sum := sha256.Sum256(raw)
			hash := hex.EncodeToString(sum[:])
			mid := len(raw) / 2
			arrivals := 0
			svc.OnArrival = func(id string, kind pb.FormKind) {
				if id != f.ID || kind != pb.FormKind_FORM_KIND_FACT {
					t.Errorf("arrival: %s %v", id, kind)
				}
				arrivals++
			}
			first, err := svc.Exchange(ctx, &pb.ExchangeRequest{EdgeId: "edge-1",
				Manifest:  []*pb.ManifestEntry{{ItemId: f.ID, Type: pb.ItemType_ITEM_TYPE_IMAGE, Forms: []*pb.FormSize{{Kind: pb.FormKind_FORM_KIND_FACT, Bytes: int64(len(raw))}}}},
				Chunks:    []*pb.Chunk{{ItemId: f.ID, Form: pb.FormKind_FORM_KIND_FACT, Data: raw[:mid], TotalBytes: int64(len(raw)), ContentHash: hash}},
				Heartbeat: &pb.Heartbeat{QueuedItems: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			if first.ShoreTimeUnixMs != clk.Now().UnixMilli() || len(first.Acks) != 2 || !first.Acks[0].Manifest || first.Acks[1].Complete || first.Acks[1].AckedThrough != int64(mid) || arrivals != 0 {
				t.Fatalf("partial response=%v arrivals=%d", first, arrivals)
			}
			partial, err := st.Get(ctx, f.ID)
			if err != nil || partial.Features != nil {
				t.Fatalf("partial item=%v err=%v", partial, err)
			}
			clk.Advance(time.Minute)
			hb := &pb.Heartbeat{QueuedItems: 1, QueuedBytes: 17, PolicyId: "active-policy"}
			second, err := (Handler{Service: svc}).Exchange(ctx, connect.NewRequest(&pb.ExchangeRequest{EdgeId: "edge-1",
				Chunks: []*pb.Chunk{{ItemId: f.ID, Form: pb.FormKind_FORM_KIND_FACT, Offset: int64(mid), Data: raw[mid:], TotalBytes: int64(len(raw)), ContentHash: hash}}, Heartbeat: hb,
				Overrides: []*pb.LabelOverride{{ItemId: "../invalid", Label: "ignored"}, {ItemId: f.ID, Label: "confirmed-boat", By: "watchstander"}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Msg.Acks) != 1 || !second.Msg.Acks[0].Complete || second.Msg.Acks[0].AckedThrough != int64(len(raw)) || arrivals != 1 {
				t.Fatalf("complete response=%v arrivals=%d", second.Msg, arrivals)
			}
			item, err := st.Get(ctx, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			if item.LabelOverride != "confirmed-boat" || item.OverrideBy != "watchstander" || !item.Form(pb.FormKind_FORM_KIND_FACT).Complete {
				t.Fatalf("completed item=%+v", item)
			}
			if malformed {
				if item.Features != nil || !strings.Contains(log.String(), "bad fact") {
					t.Fatalf("malformed features=%v log=%s", item.Features, log.String())
				}
			} else if !proto.Equal(item.Features, f.ToFeatures()) {
				t.Fatalf("features=%v want=%v", item.Features, f.ToFeatures())
			}
			edges, err := st.Edges(ctx)
			if err != nil || len(edges) != 1 {
				t.Fatalf("edges=%v err=%v", edges, err)
			}
			if edges[0].BytesReceived != int64(len(raw)) || !edges[0].LastSeen.Equal(clk.Now()) || !proto.Equal(edges[0].Heartbeat, hb) {
				t.Fatalf("heartbeat=%+v", edges[0])
			}
		})
	}
}

func TestExchangeDownlinkCursorAndRejectedChunk(t *testing.T) {
	svc, st, clk, log := behaviorService(t)
	ctx := context.Background()
	for _, edge := range []string{"edge-1", "edge-2", ""} {
		if _, err := st.EnqueueDownlink(ctx, edge, &pb.Downlink{Msg: &pb.Downlink_Policy{Policy: &pb.Policy{Id: "policy-" + edge}}}, clk.Now()); err != nil {
			t.Fatal(err)
		}
	}
	arrivals := 0
	svc.OnArrival = func(string, pb.FormKind) { arrivals++ }
	resp, err := svc.Exchange(ctx, &pb.ExchangeRequest{EdgeId: "edge-1", Chunks: []*pb.Chunk{{ItemId: "bad-chunk", TotalBytes: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Acks) != 0 || arrivals != 0 || !strings.Contains(log.String(), "chunk") {
		t.Fatalf("rejected chunk: response=%v arrivals=%d log=%s", resp, arrivals, log.String())
	}
	if len(resp.Downlink) != 2 || resp.Downlink[0].GetPolicy().Id != "policy-edge-1" || resp.Downlink[1].GetPolicy().Id != "policy-" {
		t.Fatalf("downlink=%v", resp.Downlink)
	}
	last := resp.Downlink[1].Seq
	if resp.Downlink[0].Seq >= last {
		t.Fatal("downlink not oldest first")
	}
	empty, err := svc.Exchange(ctx, &pb.ExchangeRequest{EdgeId: "edge-1", DownlinkCursor: last})
	if err != nil || len(empty.Acks) != 0 || len(empty.Downlink) != 0 {
		t.Fatalf("empty exchange=%v err=%v", empty, err)
	}
}

func TestExchangePropagatesStorageFailures(t *testing.T) {
	for _, request := range []*pb.ExchangeRequest{
		{EdgeId: "edge-1", Manifest: []*pb.ManifestEntry{{ItemId: "item"}}},
		{EdgeId: "edge-1", Heartbeat: &pb.Heartbeat{}},
		{EdgeId: "edge-1"},
	} {
		svc, st, _, _ := behaviorService(t)
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		resp, err := (Handler{Service: svc}).Exchange(context.Background(), connect.NewRequest(request))
		if resp != nil || connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("response=%v err=%v", resp, err)
		}
	}
}

func TestIngestFactMissingFileAndFailedFeatureWrite(t *testing.T) {
	svc, st, clk, log := behaviorService(t)
	svc.ingestFact(context.Background(), "missing")
	if log.Len() != 0 {
		t.Fatalf("missing file logged: %s", log.String())
	}
	raw := (&fact.Fact{ID: "stored-fact", Labels: []fact.Label{{Label: "boat", Conf: 0.9}}}).Marshal()
	if _, done, err := st.WriteChunk(context.Background(), "edge-1", &pb.Chunk{ItemId: "stored-fact", Form: pb.FormKind_FORM_KIND_FACT, Data: raw, TotalBytes: int64(len(raw))}, clk.Now()); err != nil || !done {
		t.Fatalf("store FACT done=%v err=%v", done, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	svc.ingestFact(context.Background(), "stored-fact")
	if !strings.Contains(log.String(), "set features") {
		t.Fatalf("failed write not logged: %s", log.String())
	}
}
