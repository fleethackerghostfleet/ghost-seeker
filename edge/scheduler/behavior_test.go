package scheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

type behaviorLink struct {
	state    pb.LinkState
	err      error
	advance  func()
	requests []*pb.ExchangeRequest
}

func (l *behaviorLink) State() *pb.LinkState { return &l.state }
func (l *behaviorLink) Exchange(_ context.Context, request *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	l.requests = append(l.requests, request)
	if l.advance != nil {
		l.advance()
	}
	return &pb.ExchangeResponse{}, l.err
}

type stoppingClock struct {
	*clock.Virtual
	waits []time.Duration
}

func (c *stoppingClock) Sleep(_ context.Context, wait time.Duration) error {
	c.waits = append(c.waits, wait)
	return context.Canceled
}

func TestRunCancellationAndTickFailures(t *testing.T) {
	for _, mode := range []string{"canceled", "empty", "closed store"} {
		t.Run(mode, func(t *testing.T) {
			s, st, _, clk := setup(t, Prioritized)
			stopping := &stoppingClock{Virtual: clk}
			s.clk = stopping
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			if mode == "closed store" {
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run=%v", err)
			}
			if mode == "canceled" {
				if len(stopping.waits) != 0 || s.Stats().Exchanges != 0 {
					t.Fatal("canceled run did work")
				}
			} else if !reflect.DeepEqual(stopping.waits, []time.Duration{s.cfg.Tick}) {
				t.Fatalf("waits=%v", stopping.waits)
			}
			if mode == "empty" && s.Stats().Exchanges != 1 {
				t.Fatal("empty run omitted downlink exchange")
			}
		})
	}
}

func TestPreviewSilentBacklogDoesNotMutateDelivery(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	row := addImage(t, s, st, clk, "boat", .9, .2, 6000)
	ctx := context.Background()
	if _, _, err := s.ApplyPolicy(ctx, &pb.Policy{Id: "silent-preview", Emcon: pb.EmconMode_EMCON_MODE_SILENT}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Stats()
	if plan := s.BuildPlan(rows, 300, clk.Now()); plan.Budget != 0 || len(plan.Manifest)+len(plan.Chunks)+len(plan.Queue) != 0 {
		t.Fatalf("silent plan=%+v", plan)
	}
	preview := s.PreviewPlan(rows, 300, clk.Now())
	if preview.Budget != 512 || len(preview.Queue) != 1 || preview.Queue[0].ItemId != row.Item.Id {
		t.Fatalf("preview=%+v", preview)
	}
	stored, err := st.Get(ctx, row.Item.Id)
	if err != nil || stored.ManifestSent || stored.ManifestAcked || s.Stats() != before || len(sh.chunks) != 0 {
		t.Fatalf("preview changed delivery: %+v %v", stored, err)
	}
	if s.Link() != sh {
		t.Fatal("wrong transport exposed")
	}
	if empty := s.PreviewPlan(nil, 300, clk.Now()); len(empty.Queue)+len(empty.Chunks)+len(empty.Manifest) != 0 {
		t.Fatalf("empty preview=%+v", empty)
	}
}

func TestLocalPolicySnapshotsAndExpiredRestart(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	ctx := context.Background()
	if s.RawPolicy() != nil {
		t.Fatal("default is not an accepted raw policy")
	}
	p := &pb.Policy{Id: "local", ExpiresAtUnixMs: clk.Now().Add(time.Minute).UnixMilli(), Interest: &pb.Interest{BoostLabels: []string{"submarine"}}}
	if _, _, err := s.AcceptLocalPolicy(ctx, p, "operator"); err != nil {
		t.Fatal(err)
	}
	raw := s.RawPolicy()
	if raw == nil || raw.Id != "local" || raw.Version <= 0 || s.Policy().GetEmcon() != pb.EmconMode_EMCON_MODE_NORMAL {
		t.Fatalf("raw=%+v applied=%+v", raw, s.Policy())
	}
	raw.Id = "mutated"
	raw.Interest.BoostLabels[0] = "mutated"
	if next := s.RawPolicy(); next.Id != "local" || next.Interest.BoostLabels[0] != "submarine" {
		t.Fatal("raw policy alias escaped")
	}
	if _, _, err := s.AcceptLocalPolicy(ctx, nil, "operator"); err == nil || s.RawPolicy().Id != "local" {
		t.Fatal("invalid local policy changed active policy")
	}
	clk.Advance(time.Minute)
	if s.RawPolicy() != nil {
		t.Fatal("expired raw policy remains active")
	}
	restarted, err := New(s.cfg, st, sh, scoring.Default(), clk, nil)
	if err != nil || restarted.Policy().Id != "default" {
		t.Fatalf("expired restart=%+v err=%v", restarted, err)
	}
	persisted, err := st.GetPolicy(ctx)
	if err != nil || persisted.Id != "default" {
		t.Fatalf("persisted fallback=%+v %v", persisted, err)
	}
}

func TestTickFailureBackoffClearsRetryStateAndCounts(t *testing.T) {
	for _, up := range []bool{false, true} {
		s, st, _, clk := setup(t, Prioritized)
		row := addImage(t, s, st, clk, "boat", .9, .2, 64)
		failure := errors.New("transport unavailable")
		lk := &behaviorLink{state: pb.LinkState{EstimateBps: 64000, Up: up}, err: failure}
		s.lk = lk
		wait, err := s.Tick(context.Background())
		wantWait := s.cfg.Tick
		if !up {
			wantWait = s.cfg.ProbeInterval
		}
		if !errors.Is(err, failure) || wait != wantWait {
			t.Fatalf("up=%v wait=%s err=%v", up, wait, err)
		}
		stats := s.Stats()
		if stats.Exchanges != 1 || stats.Failures != 1 || stats.BytesPayload != 0 || !stats.LastExchange.IsZero() {
			t.Fatalf("failure stats=%+v", stats)
		}
		stored, err := st.Get(context.Background(), row.Item.Id)
		if err != nil || stored.ManifestSent || len(lk.requests) != 1 || len(lk.requests[0].Manifest) != 1 {
			t.Fatalf("retry state=%+v err=%v", stored, err)
		}
	}
	s, st, _, clk := setup(t, Prioritized)
	addImage(t, s, st, clk, "boat", .9, .2, 64)
	lk := &behaviorLink{state: pb.LinkState{EstimateBps: 64000, Up: true}, advance: func() { clk.Advance(6 * time.Second) }}
	s.lk = lk
	if wait, err := s.Tick(context.Background()); err != nil || wait != 0 {
		t.Fatalf("airtime wait=%s err=%v", wait, err)
	}
	stats := s.Stats()
	if stats.Exchanges != 1 || stats.Failures != 0 || stats.ManifestSent != 1 || stats.BytesPayload <= 0 || stats.ChunksSent == 0 || !stats.LastExchange.Equal(clk.Now()) {
		t.Fatalf("success stats=%+v", stats)
	}
}

func TestRecordOverrideKeepsPayloadAndDownlinkSkipsFailedCommands(t *testing.T) {
	s, st, _, _ := setup(t, Prioritized)
	ctx := context.Background()
	it := &pb.Item{Id: "record", Type: pb.ItemType_ITEM_TYPE_RECORD}
	if _, err := st.Put(ctx, it, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: []byte("original-record")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Override(ctx, &pb.LabelOverride{ItemId: it.Id, Label: "boat", By: "operator"}); err != nil {
		t.Fatal(err)
	}
	row, err := st.Get(ctx, it.Id)
	if err != nil || row.Item.GetFeatures().GetLabelOverride() != "boat" {
		t.Fatalf("override=%+v %v", row, err)
	}
	data, err := st.ReadFormAll(it.Id, pb.FormKind_FORM_KIND_FACT)
	if err != nil || string(data) != "original-record" {
		t.Fatalf("record FACT=%q %v", data, err)
	}
	if err := st.SetCursor(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var applied []int64
	s.OnDownlink = func(d *pb.Downlink) { applied = append(applied, d.Seq) }
	response := &pb.ExchangeResponse{Acks: []*pb.Ack{{ItemId: it.Id, Manifest: true}}, Downlink: []*pb.Downlink{
		{Seq: 1, Msg: &pb.Downlink_Pull{Pull: &pb.Pull{ItemId: "missing"}}},
		{Seq: 2, Msg: &pb.Downlink_Pull{Pull: &pb.Pull{ItemId: it.Id, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: "operator"}}},
		{Seq: 3, Msg: &pb.Downlink_Pull{Pull: &pb.Pull{ItemId: "missing", Original: true}}},
	}}
	if err := s.applyResponse(ctx, response); err != nil {
		t.Fatal(err)
	}
	cursor, err := st.GetCursor(ctx)
	if err != nil || cursor != 3 || !reflect.DeepEqual(applied, []int64{2}) {
		t.Fatalf("cursor=%d callbacks=%v err=%v", cursor, applied, err)
	}
	row, err = st.Get(ctx, it.Id)
	if err != nil || !row.ManifestAcked || row.PullBoost != s.cfg.PullBoost {
		t.Fatalf("downlink result=%+v %v", row, err)
	}
}

func TestSchedulerReportsUnavailableStoreAndMissingItems(t *testing.T) {
	s, st, lk, clk := setup(t, Prioritized)
	ctx := context.Background()
	if err := s.Override(ctx, &pb.LabelOverride{ItemId: "missing"}); err == nil {
		t.Fatal("missing override accepted")
	}
	if err := s.Pull(ctx, &pb.Pull{ItemId: "missing"}); err == nil {
		t.Fatal("missing pull accepted")
	}
	if err := s.Pull(ctx, &pb.Pull{ItemId: "missing", Original: true, RequestedBy: "operator"}); err == nil {
		t.Fatal("missing original accepted")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := New(s.cfg, st, lk, scoring.Default(), clk, nil); err == nil {
		t.Fatal("constructor accepted unavailable store")
	}
	if _, _, err := s.ApplyPolicy(ctx, &pb.Policy{}); err == nil {
		t.Fatal("unpersisted policy reported success")
	}
	if _, err := s.Tick(ctx); err == nil {
		t.Fatal("tick ignored unavailable store")
	}
}
