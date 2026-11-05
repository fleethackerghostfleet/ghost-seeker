package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
)

// fakeShore is an in-memory SyncService that acks everything it gets and
// can be told to fail.
type fakeShore struct {
	mu        sync.Mutex
	fail      bool
	manifests []string
	chunks    []*pb.Chunk
	overrides []*pb.LabelOverride
	recv      map[string]int64 // item/form -> contiguous bytes
	downlink  []*pb.Downlink
	est       *link.Estimator
}

func newFakeShore() *fakeShore {
	return &fakeShore{recv: map[string]int64{}, est: link.NewEstimator(64000, 300, 1e9)}
}

func (f *fakeShore) Exchange(_ context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		f.est.Failure()
		return nil, errors.New("down")
	}
	f.est.Success(1<<20, time.Second, time.Now())
	resp := &pb.ExchangeResponse{}
	f.overrides = append(f.overrides, req.Overrides...)
	for _, m := range req.Manifest {
		f.manifests = append(f.manifests, m.ItemId)
		resp.Acks = append(resp.Acks, &pb.Ack{ItemId: m.ItemId, Manifest: true})
	}
	for _, c := range req.Chunks {
		f.chunks = append(f.chunks, c)
		k := c.ItemId + "/" + c.Form.String()
		if c.Offset <= f.recv[k] {
			if end := c.Offset + int64(len(c.Data)); end > f.recv[k] {
				f.recv[k] = end
			}
		}
		resp.Acks = append(resp.Acks, &pb.Ack{ItemId: c.ItemId, Form: c.Form, AckedThrough: f.recv[k], Complete: f.recv[k] >= c.TotalBytes})
	}
	for _, d := range f.downlink {
		if d.Seq > req.DownlinkCursor {
			resp.Downlink = append(resp.Downlink, d)
		}
	}
	return resp, nil
}

func (f *fakeShore) State() *pb.LinkState { return f.est.State() }

func setup(t *testing.T, strat Strategy) (*Scheduler, *store.Store, *fakeShore, *clock.Virtual) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	sh := newFakeShore()
	cfg := DefaultConfig("t")
	cfg.Strategy = strat
	cfg.MaxExchangeBytes = 20_000
	s, err := New(cfg, st, sh, scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, st, sh, clk
}

func addImage(t *testing.T, s *Scheduler, st *store.Store, clk *clock.Virtual, label string, conf, unknown float32, fullBytes int) *store.Row {
	t.Helper()
	it := &pb.Item{Id: fmt.Sprintf("%s-%d-%d", label, fullBytes, clk.Now().UnixNano()), Type: pb.ItemType_ITEM_TYPE_IMAGE,
		CapturedAtUnixMs: clk.Now().UnixMilli(), Features: &pb.Features{Unknownness: unknown}}
	if label != "" {
		it.Features.Detections = []*pb.Detection{{Label: label, Confidence: conf}}
	}
	payloads := map[pb.FormKind][]byte{
		pb.FormKind_FORM_KIND_FACT:  []byte(`{"id":"` + it.Id + `"}`),
		pb.FormKind_FORM_KIND_THUMB: make([]byte, 2000),
		pb.FormKind_FORM_KIND_FULL:  make([]byte, fullBytes),
	}
	if _, err := st.Put(context.Background(), it, payloads); err != nil {
		t.Fatal(err)
	}
	row, err := st.Get(context.Background(), it.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ScoreItem(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	return row
}

func TestManifestFirstAndLadder(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	ctx := context.Background()
	addImage(t, s, st, clk, "cargo ship", 0.9, 0, 30_000)
	unk := addImage(t, s, st, clk, "", 0, 0.9, 30_000)
	rows, _ := st.All(ctx)
	plan := s.BuildPlan(rows, 64000, clk.Now())
	if len(plan.Manifest) != 2 {
		t.Fatalf("manifest entries = %d, want 2", len(plan.Manifest))
	}
	if plan.Manifest[0].ItemId != unk.Item.Id {
		t.Fatalf("unknown should be manifested first, got %s", plan.Manifest[0].ItemId)
	}
	if len(plan.Chunks) == 0 || plan.Chunks[0].Form != pb.FormKind_FORM_KIND_FACT || plan.Chunks[0].ItemId != unk.Item.Id {
		t.Fatalf("first chunk should be FACT of unknown, got %+v", plan.Chunks[0])
	}
	// Drive until everything is delivered; verify per-item ladder order.
	for i := 0; i < 50; i++ {
		if _, err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]pb.FormKind{}
	for _, c := range sh.chunks {
		if prev, ok := seen[c.ItemId]; ok && c.Form < prev {
			t.Fatalf("ladder violated for %s: %v after %v", c.ItemId, c.Form, prev)
		}
		seen[c.ItemId] = c.Form
	}
	rows, _ = st.All(ctx)
	for _, r := range rows {
		for _, f := range r.Forms {
			if !f.Complete {
				t.Fatalf("%s/%v not complete", r.Item.Id, f.Kind)
			}
		}
	}
}

func TestSuppressStillSendsFact(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	ctx := context.Background()
	sail := addImage(t, s, st, clk, "sailboat", 0.9, 0, 30_000)
	cargo := addImage(t, s, st, clk, "cargo ship", 0.9, 0, 30_000)
	if _, _, err := s.ApplyPolicy(ctx, &pb.Policy{Id: "p1", Interest: &pb.Interest{SuppressLabels: []string{"sailboat"}}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.All(ctx)
	var sr, cr *store.Row
	for _, r := range rows {
		if r.Item.Id == sail.Item.Id {
			sr = r
		} else {
			cr = r
		}
	}
	if !sr.Score.Suppressed || sr.Score.Priority >= cr.Score.Priority {
		t.Fatalf("sailboat should be suppressed and below cargo: %v vs %v", sr.Score.Priority, cr.Score.Priority)
	}
	if sr.Score.Priority <= 0 {
		t.Fatal("suppressed item must keep a positive priority (nothing is dropped)")
	}
	// Small budget: cargo full must finish before any sailboat image bytes.
	for i := 0; i < 6; i++ {
		if _, err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cargoDone := false
	for _, c := range sh.chunks {
		if c.ItemId == cargo.Item.Id && c.Form == pb.FormKind_FORM_KIND_FULL && c.Offset+int64(len(c.Data)) >= c.TotalBytes {
			cargoDone = true
		}
		if c.ItemId == sail.Item.Id && c.Form != pb.FormKind_FORM_KIND_FACT && !cargoDone {
			t.Fatalf("sailboat image bytes sent before cargo full completed")
		}
	}
	fact := false
	for _, c := range sh.chunks {
		if c.ItemId == sail.Item.Id && c.Form == pb.FormKind_FORM_KIND_FACT {
			fact = true
		}
	}
	if !fact {
		t.Fatal("suppressed item's FACT should still be sent")
	}
	// With bandwidth to spare, the suppressed image eventually goes too.
	for i := 0; i < 50; i++ {
		_, _ = s.Tick(ctx)
	}
	r, _ := st.Get(ctx, sail.Item.Id)
	if !r.Form(pb.FormKind_FORM_KIND_FULL).Complete {
		t.Fatal("suppressed item never drained even with idle link")
	}
}

func TestResumeAfterFailure(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	ctx := context.Background()
	img := addImage(t, s, st, clk, "cargo ship", 0.9, 0, 100_000)
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sh.fail = true
	for i := 0; i < 3; i++ {
		_, _ = s.Tick(ctx)
	}
	sh.fail = false
	before := len(sh.chunks)
	for i := 0; i < 40; i++ {
		_, _ = s.Tick(ctx)
	}
	r, _ := st.Get(ctx, img.Item.Id)
	if !r.Form(pb.FormKind_FORM_KIND_FULL).Complete {
		t.Fatal("full form never completed after link recovery")
	}
	// Chunks after recovery must start at the acked offset, not from 0.
	for _, c := range sh.chunks[before:] {
		if c.Form == pb.FormKind_FORM_KIND_FULL {
			if c.Offset == 0 && sh.recv[img.Item.Id+"/"+c.Form.String()] > 0 {
				for _, d := range sh.chunks {
					t.Logf("chunk %v off=%d len=%d", d.Form, d.Offset, len(d.Data))
				}
				t.Fatal("restarted from offset 0 after partial delivery")
			}
			break
		}
	}
	total := int64(0)
	for _, c := range sh.chunks {
		if c.Form == pb.FormKind_FORM_KIND_FULL {
			total += int64(len(c.Data))
		}
	}
	if total > 100_000+2*20_000 {
		t.Fatalf("too many bytes resent: %d", total)
	}
}

func TestDownlinkPolicyAndPull(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	ctx := context.Background()
	a := addImage(t, s, st, clk, "sailboat", 0.9, 0, 30_000)
	b := addImage(t, s, st, clk, "sailboat", 0.9, 0, 30_000)
	sh.downlink = []*pb.Downlink{
		{Seq: 1, Msg: &pb.Downlink_Policy{Policy: &pb.Policy{Id: "p2", Interest: &pb.Interest{SuppressLabels: []string{"sailboat"}}}}},
		{Seq: 2, Msg: &pb.Downlink_Pull{Pull: &pb.Pull{ItemId: b.Item.Id, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: "shore"}}},
	}
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Policy().Id != "p2" {
		t.Fatalf("policy not applied: %s", s.Policy().Id)
	}
	cur, _ := st.GetCursor(ctx)
	if cur != 2 {
		t.Fatalf("cursor = %d", cur)
	}
	rows, _ := st.All(ctx)
	plan := s.BuildPlan(rows, 64000, clk.Now())
	// Pulled item should rank above the other suppressed one for image bytes.
	var firstImg string
	for _, q := range plan.Queue {
		if q.Form != pb.FormKind_FORM_KIND_FACT {
			firstImg = q.ItemId
			break
		}
	}
	if firstImg != b.Item.Id {
		t.Fatalf("pulled item %s should be first image in queue, got %s (a=%s)", b.Item.Id, firstImg, a.Item.Id)
	}
}

func TestAcceptSignedLayeredPolicyAndConflicts(t *testing.T) {
	s, st, _, clk := setup(t, Prioritized)
	pub, priv, err := policy.GenerateKey("command")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.PolicyKeys = policy.Keyring{"command": pub}
	s.guard, err = policy.NewStore(context.Background(), st, policy.Options{Keys: s.cfg.PolicyKeys})
	if err != nil {
		t.Fatal(err)
	}
	layers := &pb.Policy{Id: "layered", Version: 1, IssuedAtUnixMs: clk.Now().UnixMilli(), Layers: []*pb.PolicyLayer{
		{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72", Rules: []*pb.InterestRule{
			{Id: "locked-sub", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3},
		}},
		{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Rules: []*pb.InterestRule{
			{Id: "suppress-sub", SuppressLabels: []string{"submarine"}},
		}},
	}}
	if err := policy.Sign(layers, "command", priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptPolicy(context.Background(), layers, "downlink"); err != nil {
		t.Fatal(err)
	}
	applied := s.Policy()
	if len(applied.Interest.BoostLabels) != 1 || applied.Interest.BoostLabels[0] != "submarine" ||
		len(applied.Conflicts) != 1 || applied.Conflicts[0].RuleId != "suppress-sub" {
		t.Fatalf("applied layered policy=%+v", applied)
	}
}

func TestRejectedPolicyDownlinkAdvancesCursorAndStaleVersion(t *testing.T) {
	s, st, sh, _ := setup(t, Prioritized)
	s.cfg.RequireSignedPolicy = true
	var err error
	s.guard, err = policy.NewStore(context.Background(), st, policy.Options{RequireSigned: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptPolicy(context.Background(), &pb.Policy{Id: "accepted", Version: 4}, "console"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptPolicy(context.Background(), &pb.Policy{Id: "stale", Version: 4}, "console"); !errors.Is(err, policy.ErrStaleVersion) {
		t.Fatalf("stale version error=%v", err)
	}
	sh.downlink = []*pb.Downlink{{Seq: 7, Msg: &pb.Downlink_Policy{Policy: &pb.Policy{Id: "unsigned", Version: 5}}}}
	if _, err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().Id; got != "accepted" {
		t.Fatalf("rejected downlink replaced policy: %q", got)
	}
	cursor, err := st.GetCursor(context.Background())
	if err != nil || cursor != 7 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}
	audit, err := s.PolicyAudit(context.Background(), 0)
	if err != nil || len(audit) < 2 || audit[0].Result != "rejected" || audit[0].Source != "downlink" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}

func TestPolicyExpiryAndRuleExpiryOnTick(t *testing.T) {
	s, _, _, clk := setup(t, Prioritized)
	ctx := context.Background()
	start := clk.Now()
	withRuleExpiry := &pb.Policy{
		Id: "rule-expiry", IssuedAtUnixMs: start.UnixMilli(), ExpiresAtUnixMs: start.Add(time.Hour).UnixMilli(),
		Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_SHIP, Rules: []*pb.InterestRule{
			{Id: "short", BoostLabels: []string{"submarine"}, ExpiresAtUnixMs: start.Add(time.Second).UnixMilli()},
		}}},
	}
	if _, _, err := s.AcceptPolicy(ctx, withRuleExpiry, "console"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().Interest.BoostLabels; len(got) != 0 {
		t.Fatalf("expired rule remains active: %v", got)
	}
	audit, err := s.PolicyAudit(ctx, 0)
	if err != nil || len(audit) != 1 || audit[0].Result != "applied" {
		t.Fatalf("rule expiry audit=%+v err=%v", audit, err)
	}

	expiring := &pb.Policy{Id: "whole-policy", IssuedAtUnixMs: clk.Now().UnixMilli(), ExpiresAtUnixMs: clk.Now().Add(time.Second).UnixMilli()}
	if _, _, err := s.AcceptPolicy(ctx, expiring, "console"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().Id; got != "default" {
		t.Fatalf("expired policy fallback=%q, want default", got)
	}
	audit, err = s.PolicyAudit(ctx, 0)
	if err != nil || len(audit) < 2 || audit[0].Result != "expired" {
		t.Fatalf("policy expiry audit=%+v err=%v", audit, err)
	}
}

func TestPolicyRestartRestoresFlattenedLastGood(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cfg := DefaultConfig("edge-7")
	first, err := New(cfg, st, newFakeShore(), scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &pb.Policy{Id: "persisted", Version: 3, IssuedAtUnixMs: clk.Now().UnixMilli(), Layers: []*pb.PolicyLayer{
		{Echelon: pb.Echelon_ECHELON_SHIP, Rules: []*pb.InterestRule{{Id: "sub", BoostLabels: []string{"submarine"}}}},
	}}
	if _, _, err := first.AcceptPolicy(ctx, p, "console"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := New(cfg, reopened, newFakeShore(), scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Policy().Id != "persisted" || len(restored.Policy().Interest.BoostLabels) != 1 ||
		restored.Policy().Interest.BoostLabels[0] != "submarine" {
		t.Fatalf("restored policy=%+v", restored.Policy())
	}
}

func TestFIFOBaselineSendsFullInOrder(t *testing.T) {
	s, st, sh, clk := setup(t, FIFO)
	ctx := context.Background()
	first := addImage(t, s, st, clk, "sailboat", 0.9, 0, 10_000)
	addImage(t, s, st, clk, "", 0, 0.9, 10_000)
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sh.manifests) != 0 {
		t.Fatal("FIFO must not send manifest")
	}
	if len(sh.chunks) == 0 || sh.chunks[0].ItemId != first.Item.Id || sh.chunks[0].Form != pb.FormKind_FORM_KIND_FULL {
		t.Fatalf("FIFO should send first item's FULL first: %+v", sh.chunks[0])
	}
}

func TestEmconSilentSendsNothing(t *testing.T) {
	s, st, sh, clk := setup(t, Prioritized)
	ctx := context.Background()
	addImage(t, s, st, clk, "cargo ship", 0.9, 0, 10_000)
	if _, _, err := s.ApplyPolicy(ctx, &pb.Policy{Id: "silent", Emcon: pb.EmconMode_EMCON_MODE_SILENT}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _ = s.Tick(ctx)
	}
	if len(sh.chunks) != 0 || len(sh.manifests) != 0 {
		t.Fatal("SILENT must transmit nothing")
	}
}

func TestScenePullOnlyFull(t *testing.T) {
	s, st, _, clk := setup(t, Prioritized)
	ctx := context.Background()
	it := &pb.Item{Id: "scene-1", Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: clk.Now().UnixMilli(),
		Features: &pb.Features{Tags: map[string]string{"event": "scene"}}}
	payloads := map[pb.FormKind][]byte{
		pb.FormKind_FORM_KIND_FACT:  []byte(`{"id":"scene-1"}`),
		pb.FormKind_FORM_KIND_THUMB: make([]byte, 2000),
		pb.FormKind_FORM_KIND_FULL:  make([]byte, 5000),
	}
	if _, err := st.Put(ctx, it, payloads); err != nil {
		t.Fatal(err)
	}
	row, err := st.Get(ctx, it.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ScoreItem(ctx, row); err != nil {
		t.Fatal(err)
	}
	hasFull := func() bool {
		rows, _ := st.All(ctx)
		for _, r := range rows {
			r.ManifestAcked = true
			for _, k := range []pb.FormKind{pb.FormKind_FORM_KIND_FACT, pb.FormKind_FORM_KIND_THUMB} {
				if f := r.Form(k); f != nil {
					f.AckedThrough, f.Complete = f.Bytes, true
				}
			}
		}
		for _, q := range s.BuildPlan(rows, 2_000_000, clk.Now()).Queue {
			if q.ItemId == it.Id && q.Form == pb.FormKind_FORM_KIND_FULL {
				return true
			}
		}
		return false
	}
	if hasFull() {
		t.Fatal("scene FULL must not be offered on the ladder before a pull")
	}
	if err := s.Pull(ctx, &pb.Pull{ItemId: it.Id, Form: pb.FormKind_FORM_KIND_THUMB, RequestedBy: "shore"}); err != nil {
		t.Fatal(err)
	}
	if hasFull() {
		t.Fatal("pulling the THUMB must not release the scene FULL")
	}
	if err := s.Pull(ctx, &pb.Pull{ItemId: it.Id, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: "shore"}); err != nil {
		t.Fatal(err)
	}
	if !hasFull() {
		t.Fatal("pulled scene FULL must be queued")
	}
}
