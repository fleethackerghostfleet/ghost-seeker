// Package scheduler decides, every time the link can carry something, which
// (item, form) pairs leave the ship and in what order.
//
// Rules, in order:
//  1. Manifest entries for every unmanifested item go first (shore must know
//     what exists before it gets any payload).
//  2. Among payloads, rank by priority × ladder weight / remaining bytes:
//     the most information per byte goes first.
//  3. Per item, the fidelity ladder is enforced: FACT before THUMB before
//     CROP before FULL. Partial transfers are still useful.
//  4. Items under the image threshold (or suppressed) only send FACT until
//     nothing better is waiting; then everything drains. Nothing is dropped.
//  5. The plan is rebuilt every tick from the current link estimate and
//     policy, so a bandwidth change or operator change takes effect on the
//     next exchange.
//  6. Chunks resume from the last acked offset; acks are idempotent.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/proto"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

type Strategy string

const (
	// Prioritized is the real scheduler.
	Prioritized Strategy = "prioritized"
	// FIFO is the baseline: arrival order, full payload only, no manifest.
	FIFO Strategy = "fifo"
)

type Config struct {
	EdgeID              string
	Strategy            Strategy
	Tick                time.Duration // planning interval when the link is up
	ProbeInterval       time.Duration // how often to try a dead link
	MaxExchangeBytes    int64
	MinChunk            int64
	MaxChunk            int64
	RescoreEvery        time.Duration // freshness refresh
	ManifestRetry       time.Duration // resend manifest if unacked for this long
	PullBoost           float64
	RequireSignedPolicy bool
	PolicyKeys          policy.Keyring
}

func DefaultConfig(edgeID string) Config {
	return Config{
		EdgeID: edgeID, Strategy: Prioritized, Tick: 5 * time.Second, ProbeInterval: 15 * time.Second,
		MaxExchangeBytes: 4 << 20, MinChunk: 256, MaxChunk: 64 << 10,
		RescoreEvery: 5 * time.Minute, ManifestRetry: 2 * time.Minute, PullBoost: 100,
	}
}

type Stats struct {
	Exchanges, Failures         int64
	BytesPayload, BytesManifest int64
	ChunksSent, ManifestSent    int64
	LastExchange                time.Time
}

type Scheduler struct {
	cfg            Config
	st             *store.Store
	lk             link.Link
	eng            *scoring.Engine
	clk            clock.Clock
	log            *slog.Logger
	mu             sync.Mutex
	policy         *pb.Policy
	last           time.Time // last full rescore
	guard          *policy.Store
	policyExpiry   time.Time
	nextRuleExpiry time.Time
	stats          Stats
	// OnDownlink is called for every applied downlink message (for UIs/tests).
	OnDownlink func(*pb.Downlink)
}

func New(cfg Config, st *store.Store, lk link.Link, eng *scoring.Engine, clk clock.Clock, log *slog.Logger) (*Scheduler, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Scheduler{cfg: cfg, st: st, lk: lk, eng: eng, clk: clk, log: log}
	var err error
	s.guard, err = policy.NewStore(context.Background(), st, policy.Options{Keys: cfg.PolicyKeys, RequireSigned: cfg.RequireSignedPolicy})
	if err != nil {
		return nil, err
	}
	now := clk.Now()
	lastGood, expired := s.guard.Active(now)
	if expired {
		s.policy = DefaultPolicy()
		_ = s.guard.MarkExpired(context.Background(), now)
		_ = st.Event(context.Background(), now, "policy", "", "expired policy -> default")
		_ = st.SetPolicy(context.Background(), s.policy)
		return s, nil
	}
	if lastGood != nil {
		s.policy = normalizePolicy(policy.Flatten(lastGood, cfg.EdgeID, now), now)
		s.policyExpiry = policy.Expiry(lastGood, policy.DefaultTTL)
		s.nextRuleExpiry = nextRuleExpiry(lastGood, now)
		return s, nil
	}
	p, err := st.GetPolicy(context.Background())
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = DefaultPolicy()
	}
	s.policy = p
	return s, nil
}

func normalizePolicy(p *pb.Policy, now time.Time) *pb.Policy {
	if p.Id == "" {
		p.Id = fmt.Sprintf("p-%d", now.UnixMilli())
	}
	if p.IssuedAtUnixMs == 0 {
		p.IssuedAtUnixMs = now.UnixMilli()
	}
	if p.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		p.Emcon = pb.EmconMode_EMCON_MODE_NORMAL
	}
	return p
}

func DefaultPolicy() *pb.Policy {
	return &pb.Policy{Id: "default", IssuedBy: "system", Text: "default scope: everything by value, unknowns first",
		Emcon: pb.EmconMode_EMCON_MODE_NORMAL, DigestIntervalS: 3600,
		Interest:   &pb.Interest{BoostFactor: scoring.DefaultBoostFactor, SuppressFactor: scoring.DefaultSuppressFactor},
		Thresholds: &pb.Thresholds{UnknownMin: scoring.DefaultUnknownMin, DetectionMinConf: scoring.DefaultDetectionMin}}
}

func (s *Scheduler) Policy() *pb.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy
}

func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Scheduler) Link() link.Link { return s.lk }

// Run ticks until ctx is done.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait, err := s.Tick(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Debug("tick", "err", err)
		}
		if err := s.clk.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// ScoreItem scores (or rescores) one item and persists it. Called by ingest
// for new items and after overrides.
func (s *Scheduler) ScoreItem(ctx context.Context, row *store.Row) error {
	s.mu.Lock()
	sc := s.eng.Score(row.Item, &scoring.Context{Policy: s.policy, Now: s.clk.Now()}, row.PullBoost)
	s.mu.Unlock()
	row.Score = sc
	return s.st.UpdateScores(ctx, map[string]*pb.Score{row.Item.Id: sc})
}

// ApplyPolicy installs a new policy and rescores the whole backlog. Returns
// how many items are now suppressed / boosted, for operator preview.
func (s *Scheduler) ApplyPolicy(ctx context.Context, p *pb.Policy) (suppressed, boosted int64, err error) {
	normalizePolicy(p, s.clk.Now())
	s.mu.Lock()
	s.policy = p
	s.mu.Unlock()
	if err := s.st.SetPolicy(ctx, p); err != nil {
		return 0, 0, err
	}
	_ = s.st.Event(ctx, s.clk.Now(), "policy", "", p.Id+": "+p.Text)
	rows, err := s.st.All(ctx)
	if err != nil {
		return 0, 0, err
	}
	if err := s.rescore(ctx, rows); err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		if r.Score.Suppressed {
			suppressed++
		}
		for _, rs := range r.Score.Reasons {
			if rs.Scorer == "interest" && rs.Value > 0 {
				boosted++
				break
			}
		}
	}
	return suppressed, boosted, nil
}

func (s *Scheduler) AcceptPolicy(ctx context.Context, p *pb.Policy, source string) (suppressed, boosted int64, err error) {
	now := s.clk.Now()
	accepted, err := s.guard.Accept(ctx, p, source, now)
	if err != nil {
		return s.rejectPolicy(ctx, p, source, now, err)
	}
	return s.applyAcceptedPolicy(ctx, accepted, now)
}

func (s *Scheduler) AcceptLocalPolicy(ctx context.Context, p *pb.Policy, source string) (suppressed, boosted int64, err error) {
	now := s.clk.Now()
	accepted, err := s.guard.AcceptLocal(ctx, p, source, now)
	if err != nil {
		return s.rejectPolicy(ctx, p, source, now, err)
	}
	return s.applyAcceptedPolicy(ctx, accepted, now)
}

func (s *Scheduler) rejectPolicy(ctx context.Context, p *pb.Policy, source string, now time.Time, err error) (int64, int64, error) {
	id := ""
	if p != nil {
		id = p.Id
	}
	s.log.Warn("reject policy", "id", id, "source", source, "err", err)
	_ = s.st.Event(ctx, now, "policy", "", fmt.Sprintf("rejected %s: %v", id, err))
	return 0, 0, err
}

func (s *Scheduler) applyAcceptedPolicy(ctx context.Context, accepted *pb.Policy, now time.Time) (suppressed, boosted int64, err error) {
	flat := policy.Flatten(accepted, s.cfg.EdgeID, now)
	suppressed, boosted, err = s.ApplyPolicy(ctx, flat)
	if err != nil {
		return suppressed, boosted, err
	}
	s.mu.Lock()
	s.policyExpiry = policy.Expiry(accepted, policy.DefaultTTL)
	s.nextRuleExpiry = nextRuleExpiry(accepted, now)
	s.mu.Unlock()
	return suppressed, boosted, nil
}

func nextRuleExpiry(p *pb.Policy, now time.Time) time.Time {
	var next time.Time
	if p == nil {
		return next
	}
	consider := func(expiryUnixMs int64) {
		if expiryUnixMs <= 0 {
			return
		}
		expires := time.UnixMilli(expiryUnixMs)
		if expires.After(now) && (next.IsZero() || expires.Before(next)) {
			next = expires
		}
	}
	for _, layer := range p.Layers {
		if layer == nil {
			continue
		}
		if layer.Emcon != nil {
			consider(layer.Emcon.ExpiresAtUnixMs)
		}
		for _, rule := range layer.Rules {
			if rule != nil {
				consider(rule.ExpiresAtUnixMs)
			}
		}
	}
	return next
}

func (s *Scheduler) checkPolicyExpiry(ctx context.Context, now time.Time) {
	s.mu.Lock()
	policyExpiry, ruleExpiry := s.policyExpiry, s.nextRuleExpiry
	s.mu.Unlock()
	if !policyExpiry.IsZero() && !now.Before(policyExpiry) {
		if _, _, err := s.ApplyPolicy(ctx, DefaultPolicy()); err != nil {
			s.log.Warn("apply default policy after expiry", "err", err)
		}
		if err := s.guard.MarkExpired(ctx, now); err != nil {
			s.log.Warn("audit expired policy", "err", err)
		}
		_ = s.st.Event(ctx, now, "policy", "", "expired policy -> default")
		s.log.Warn("policy expired; restored default policy")
		s.mu.Lock()
		s.policyExpiry, s.nextRuleExpiry = time.Time{}, time.Time{}
		s.mu.Unlock()
		return
	}
	if !ruleExpiry.IsZero() && !now.Before(ruleExpiry) {
		if lastGood, expired := s.guard.Active(now); lastGood != nil && !expired {
			flat := policy.Flatten(lastGood, s.cfg.EdgeID, now)
			if _, _, err := s.ApplyPolicy(ctx, flat); err != nil {
				s.log.Warn("reflatten policy after rule expiry", "err", err)
			}
			s.mu.Lock()
			s.nextRuleExpiry = nextRuleExpiry(lastGood, now)
			s.mu.Unlock()
		}
	}
}

func (s *Scheduler) PolicyAudit(ctx context.Context, limit int) ([]policy.AuditEntry, error) {
	return s.guard.Audit(ctx, limit)
}

// Override sets a human label on an item and rescores it.
// Override applies a shipboard label correction and queues it for uplink so
// shore learns the new label explicitly (manifests and record FACTs never
// carry it). Downlinked overrides from shore go through applyOverride.
func (s *Scheduler) Override(ctx context.Context, o *pb.LabelOverride) error {
	if err := s.applyOverride(ctx, o); err != nil {
		return err
	}
	return s.st.QueueOverride(ctx, o)
}

func (s *Scheduler) applyOverride(ctx context.Context, o *pb.LabelOverride) error {
	row, err := s.st.Get(ctx, o.ItemId)
	if err != nil {
		return err
	}
	if row.Item.Features == nil {
		row.Item.Features = &pb.Features{}
	}
	row.Item.Features.LabelOverride, row.Item.Features.OverrideBy = o.Label, o.By
	// For images the FACT is generated from Features, so regenerate and
	// re-send it to carry the override to shore. Records and digests store
	// their own bytes as FACT; those stay untouched and the override rides
	// the next manifest/downlink instead.
	for i, f := range row.Item.Forms {
		if f.Kind != pb.FormKind_FORM_KIND_FACT || !fact.Generated(row.Item.Type) {
			continue
		}
		nf, err := s.st.ReplaceForm(ctx, row.Item.Id, f.Kind, fact.FromItem(row.Item).Marshal())
		if err != nil {
			return err
		}
		row.Item.Forms[i] = nf
	}
	if err := s.st.UpdateItem(ctx, row.Item); err != nil {
		return err
	}
	_ = s.st.Event(ctx, s.clk.Now(), "override", o.ItemId, o.By+": "+o.Label)
	return s.ScoreItem(ctx, row)
}

// Pull boosts an item because shore asked for it. An original pull is an
// operator override: FULL goes as the capture's original bytes and the item
// skips THUMB/CROP from then on.
func (s *Scheduler) Pull(ctx context.Context, p *pb.Pull) error {
	boost := p.PriorityBoost
	if boost <= 0 {
		boost = s.cfg.PullBoost
	}
	form, event := p.Form, "pull"
	if p.Original {
		if strings.TrimSpace(p.RequestedBy) == "" {
			return fmt.Errorf("original pull of %s: requested_by is required", p.ItemId)
		}
		form, event = pb.FormKind_FORM_KIND_FULL, "pull-original"
		if _, err := s.st.RestoreOriginal(ctx, p.ItemId); err != nil {
			return err
		}
	}
	if err := s.st.SetPull(ctx, p.ItemId, form, boost, p.RequestedBy, p.Original); err != nil {
		return err
	}
	row, err := s.st.Get(ctx, p.ItemId)
	if err != nil {
		return err
	}
	_ = s.st.Event(ctx, s.clk.Now(), event, p.ItemId, p.RequestedBy)
	return s.ScoreItem(ctx, row)
}

func (s *Scheduler) rescore(ctx context.Context, rows []*store.Row) error {
	s.mu.Lock()
	sctx := &scoring.Context{Policy: s.policy, Now: s.clk.Now()}
	scores := make(map[string]*pb.Score, len(rows))
	for _, r := range rows {
		r.Score = s.eng.Score(r.Item, sctx, r.PullBoost)
		scores[r.Item.Id] = r.Score
	}
	s.last = sctx.Now
	s.mu.Unlock()
	return s.st.UpdateScores(ctx, scores)
}

// Plan is what one tick decided to send; exposed for the console and tests.
type Plan struct {
	Budget   int64
	Manifest []*pb.ManifestEntry
	Chunks   []*pb.Chunk
	Queue    []*pb.QueueEntry // full ranked backlog (not just what fits)
}

type cand struct {
	row       *store.Row
	form      *store.FormState
	remaining int64
	rank      float64
	tier      int // 0 = normal, 1 = fact-only items' non-fact forms
}

func ladderWeight(k pb.FormKind) float64 {
	switch k {
	case pb.FormKind_FORM_KIND_FACT:
		return 1
	case pb.FormKind_FORM_KIND_THUMB:
		return 0.6
	case pb.FormKind_FORM_KIND_CROP:
		return 0.4
	default:
		return 0.3
	}
}

// nextForm returns the next form to send for a row under the strategy and
// EMCON mode, or nil if the item is fully delivered (or blocked).
func (s *Scheduler) nextForm(r *store.Row, emcon pb.EmconMode) *store.FormState {
	if s.cfg.Strategy == FIFO {
		// Baseline: whole file, nothing else.
		var last *store.FormState
		for i := range r.Forms {
			if r.Forms[i].Ready {
				last = &r.Forms[i]
			}
		}
		if last == nil || last.Complete {
			return nil
		}
		return last
	}
	for i := range r.Forms {
		f := &r.Forms[i]
		if !f.Ready || f.Complete || PullOnly(r, f) {
			continue
		}
		if skipForm(r, f) {
			continue
		}
		if emcon == pb.EmconMode_EMCON_MODE_BURST && f.Kind != pb.FormKind_FORM_KIND_FACT {
			return nil
		}
		return f
	}
	return nil
}

// skipForm: an operator's original pull sends FULL first; THUMB/CROP wait
// until it is complete so the item still finishes.
func skipForm(r *store.Row, f *store.FormState) bool {
	if !r.PullOriginal || (f.Kind != pb.FormKind_FORM_KIND_THUMB && f.Kind != pb.FormKind_FORM_KIND_CROP) {
		return false
	}
	for _, o := range r.Forms {
		if o.Kind == pb.FormKind_FORM_KIND_FULL {
			return !o.Complete
		}
	}
	return false
}

// PullOnly reports whether a form is held on the edge for shore to pull
// rather than offered on the ladder: the FULL of an object-free "scene"
// item, until shore asks for that form specifically. Such a form is not
// backlog: it never counts as queued.
func PullOnly(r *store.Row, f *store.FormState) bool {
	return f.Kind == pb.FormKind_FORM_KIND_FULL &&
		r.Item.GetFeatures().GetTags()["event"] == "scene" &&
		!(r.PullBoost > 0 && r.PullForm == pb.FormKind_FORM_KIND_FULL)
}

// nextFormAfter returns the ladder rung following kind, if sendable.
func (s *Scheduler) nextFormAfter(r *store.Row, kind pb.FormKind, emcon pb.EmconMode) *store.FormState {
	for i := range r.Forms {
		f := &r.Forms[i]
		if f.Kind <= kind || !f.Ready || f.Complete || PullOnly(r, f) || skipForm(r, f) {
			continue
		}
		if emcon == pb.EmconMode_EMCON_MODE_BURST && f.Kind != pb.FormKind_FORM_KIND_FACT {
			return nil
		}
		return f
	}
	return nil
}

func (s *Scheduler) mkCand(r *store.Row, f *store.FormState, minImg float64) *cand {
	c := &cand{row: r, form: f, remaining: f.Bytes - f.AckedThrough}
	if c.remaining <= 0 {
		c.remaining = 1
	}
	switch s.cfg.Strategy {
	case FIFO:
		c.rank = -float64(r.Item.IngestSeq)
	default:
		c.rank = r.Score.Priority * ladderWeight(f.Kind) / float64(c.remaining)
		if f.Kind != pb.FormKind_FORM_KIND_FACT && r.PullBoost == 0 &&
			(r.Score.Suppressed || r.Score.Priority < minImg) {
			c.tier = 1
		}
		if r.Item.GetFeatures().GetTags()["event"] == "scene" {
			c.tier = 2
		}
	}
	return c
}

func sortCands(cands []*cand) {
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].tier != cands[j].tier {
			return cands[i].tier < cands[j].tier
		}
		return cands[i].rank > cands[j].rank
	})
}

// BuildPlan ranks the backlog and fills the byte budget. Pure function of
// (rows, link estimate, policy); no side effects.
func (s *Scheduler) BuildPlan(rows []*store.Row, bps float64, now time.Time) *Plan {
	s.mu.Lock()
	policy := s.policy
	s.mu.Unlock()
	emcon := policy.GetEmcon()
	plan := &Plan{}
	if emcon == pb.EmconMode_EMCON_MODE_SILENT {
		return plan
	}
	budget := int64(bps / 8 * s.cfg.Tick.Seconds())
	if budget < 512 {
		budget = 512
	}
	if budget > s.cfg.MaxExchangeBytes {
		budget = s.cfg.MaxExchangeBytes
	}
	if emcon == pb.EmconMode_EMCON_MODE_BURST && budget > 8<<10 {
		budget = 8 << 10
	}
	plan.Budget = budget
	left := budget

	// 1. Manifest, highest priority first.
	if s.cfg.Strategy == Prioritized {
		var pend []*store.Row
		for _, r := range rows {
			if r.ManifestAcked {
				continue
			}
			if r.ManifestSent && now.Sub(r.ManifestSentAt) < s.cfg.ManifestRetry {
				continue
			}
			pend = append(pend, r)
		}
		sort.SliceStable(pend, func(i, j int) bool { return pend[i].Score.Priority > pend[j].Score.Priority })
		for _, r := range pend {
			me := manifestEntry(r, policy)
			sz := int64(estimateManifestBytes(me))
			if left-sz < 0 {
				break
			}
			left -= sz
			plan.Manifest = append(plan.Manifest, me)
		}
	}

	// 2. Payload candidates: the next form of every item.
	minImg := policy.GetThresholds().GetMinPriorityForImages()
	var cands []*cand
	for _, r := range rows {
		f := s.nextForm(r, emcon)
		if f == nil {
			continue
		}
		if s.cfg.Strategy == Prioritized && !r.ManifestAcked && !containsManifest(plan.Manifest, r.Item.Id) {
			// Don't send payload for an item shore doesn't know about yet,
			// unless it's a pull (shore knows, we just lost the ack).
			if r.PullBoost == 0 {
				continue
			}
		}
		cands = append(cands, s.mkCand(r, f, minImg))
	}
	sortCands(cands)
	for _, c := range cands {
		label, _ := scoring.TopLabel(c.row.Item, 0)
		plan.Queue = append(plan.Queue, &pb.QueueEntry{ItemId: c.row.Item.Id, Form: c.form.Kind,
			BytesRemaining: c.remaining, Rank: c.rank, Score: c.row.Score, TopLabel: label})
	}

	// 3. Fill chunks, strictly in rank order: the best candidate gets all
	// the bytes it needs before the next one gets any. Small, valuable forms
	// already rank first by priority-per-byte, so nothing starves behind a
	// large file; a large file simply spans several exchanges. When a form
	// fits entirely, the item's next form joins the candidates (ranked on
	// its own merits), so one exchange can carry fact+thumb+full of a hot
	// item instead of one rung per tick.
	maxChunk := int64(bps / 8 * 2)
	if maxChunk < s.cfg.MinChunk {
		maxChunk = s.cfg.MinChunk
	}
	if maxChunk > s.cfg.MaxChunk {
		maxChunk = s.cfg.MaxChunk
	}
	for len(cands) > 0 && left >= s.cfg.MinChunk && len(plan.Chunks) < 256 {
		c := cands[0]
		cands = cands[1:]
		var sent int64
		for sent < c.remaining && left >= s.cfg.MinChunk && len(plan.Chunks) < 256 {
			n := min(c.remaining-sent, maxChunk, left)
			plan.Chunks = append(plan.Chunks, &pb.Chunk{ItemId: c.row.Item.Id, Form: c.form.Kind,
				Offset: c.form.AckedThrough + sent, TotalBytes: c.form.Bytes, ContentHash: c.form.Hash, Mime: c.form.Mime,
				Data: make([]byte, n)}) // placeholder for sizing; real bytes read at send time
			sent += n
			left -= n + 48
		}
		if sent >= c.remaining && s.cfg.Strategy == Prioritized {
			if nf := s.nextFormAfter(c.row, c.form.Kind, emcon); nf != nil {
				nc := s.mkCand(c.row, nf, minImg)
				cands = append(cands, nc)
				sortCands(cands)
			}
		}
	}
	return plan
}

func containsManifest(m []*pb.ManifestEntry, id string) bool {
	for _, e := range m {
		if e.ItemId == id {
			return true
		}
	}
	return false
}

func manifestEntry(r *store.Row, p *pb.Policy) *pb.ManifestEntry {
	label, conf := scoring.TopLabel(r.Item, p.GetThresholds().GetDetectionMinConf())
	me := &pb.ManifestEntry{ItemId: r.Item.Id, Type: r.Item.Type, CapturedAtUnixMs: r.Item.CapturedAtUnixMs,
		Source: r.Item.Source, TopLabel: label, TopConf: conf, Unknownness: r.Item.GetFeatures().GetUnknownness(),
		Priority: r.Score.Priority, Geo: r.Item.Geo}
	for _, f := range r.Forms {
		me.Forms = append(me.Forms, &pb.FormSize{Kind: f.Kind, Bytes: f.Bytes})
	}
	return me
}

func estimateManifestBytes(me *pb.ManifestEntry) int {
	return 40 + len(me.ItemId) + len(me.Source) + len(me.TopLabel) + 4*len(me.Forms)
}

// Tick runs one planning + exchange cycle and returns how long to wait
// before the next one.
func (s *Scheduler) Tick(ctx context.Context) (time.Duration, error) {
	now := s.clk.Now()
	s.checkPolicyExpiry(ctx, now)
	rows, err := s.st.All(ctx)
	if err != nil {
		return s.cfg.Tick, err
	}
	s.mu.Lock()
	needRescore := now.Sub(s.last) >= s.cfg.RescoreEvery
	s.mu.Unlock()
	if needRescore {
		if err := s.rescore(ctx, rows); err != nil {
			return s.cfg.Tick, err
		}
	}
	ls := s.lk.State()
	// In EMCON SILENT the plan is empty (BuildPlan), but the exchange still
	// runs with no manifest, chunks or overrides so the edge keeps receiving
	// downlink (the policy that lifts SILENT arrives that way).
	silent := s.Policy().GetEmcon() == pb.EmconMode_EMCON_MODE_SILENT
	plan := s.BuildPlan(rows, ls.EstimateBps, now)
	req := &pb.ExchangeRequest{EdgeId: s.cfg.EdgeID, Manifest: plan.Manifest}
	cursor, _ := s.st.GetCursor(ctx)
	req.DownlinkCursor = cursor
	// Only uplink overrides for items shore knows (manifest acked, or in
	// this request's manifest); the rest stay queued until it does.
	if pend, _ := s.st.PendingOverrides(ctx); len(pend) > 0 && !silent {
		known := map[string]bool{}
		for _, r := range rows {
			if r.ManifestAcked {
				known[r.Item.Id] = true
			}
		}
		for _, m := range plan.Manifest {
			known[m.ItemId] = true
		}
		for _, o := range pend {
			if known[o.ItemId] {
				req.Overrides = append(req.Overrides, o)
			}
		}
	}
	var payload int64
	for _, c := range plan.Chunks {
		data, err := s.st.ReadForm(c.ItemId, c.Form, c.Offset, int64(len(c.Data)))
		if err != nil {
			s.log.Warn("read form", "item", c.ItemId, "err", err)
			continue
		}
		c.Data = data
		payload += int64(len(data))
		req.Chunks = append(req.Chunks, c)
	}
	var queued, queuedBytes, unman int64
	for _, r := range rows {
		if !r.ManifestAcked {
			unman++
		}
		for _, f := range r.Forms {
			if !f.Complete {
				queued++
				queuedBytes += f.Bytes - f.AckedThrough
				break
			}
		}
	}
	req.Heartbeat = &pb.Heartbeat{SentAtUnixMs: now.UnixMilli(), QueuedItems: queued, QueuedBytes: queuedBytes,
		UnmanifestedItems: unman, PolicyId: s.Policy().Id, LinkEstimateBps: ls.EstimateBps}

	var mids []string
	for _, m := range plan.Manifest {
		mids = append(mids, m.ItemId)
	}
	if err := s.st.MarkManifestSent(ctx, mids, now); err != nil {
		return s.cfg.Tick, err
	}
	// Generous timeout: three times the expected airtime of the whole request
	// (manifest included: with a deep backlog it outweighs the chunks), at
	// least 10 s.
	wire := int64(proto.Size(req))
	if wire < payload {
		wire = payload
	}
	est := time.Duration(float64(wire+4096)*8/math.Max(ls.EstimateBps, 300)*float64(time.Second)) * 3
	if est < 10*time.Second {
		est = 10 * time.Second
	}
	xctx, cancel := context.WithTimeout(ctx, est)
	resp, err := s.lk.Exchange(xctx, req)
	cancel()
	s.mu.Lock()
	s.stats.Exchanges++
	s.mu.Unlock()
	if err != nil {
		s.mu.Lock()
		s.stats.Failures++
		s.mu.Unlock()
		_ = s.st.ClearManifestSent(ctx, mids)
		if !s.lk.State().Up {
			return s.cfg.ProbeInterval, err
		}
		return s.cfg.Tick, err
	}
	s.mu.Lock()
	s.stats.BytesPayload += payload
	s.stats.ChunksSent += int64(len(req.Chunks))
	s.stats.ManifestSent += int64(len(mids))
	s.stats.LastExchange = s.clk.Now()
	s.mu.Unlock()
	if len(req.Overrides) > 0 {
		_ = s.st.ClearOverrides(ctx, req.Overrides)
	}
	if err := s.applyResponse(ctx, resp); err != nil {
		return s.cfg.Tick, err
	}
	// If we used the whole budget there is more to send: go again right away.
	if payload > 0 && plan.Budget-payload < s.cfg.MinChunk*4 {
		return 0, nil
	}
	if len(req.Chunks) == 0 && len(mids) == 0 {
		return s.cfg.Tick, nil
	}
	// Otherwise wait out the rest of the tick in link time.
	spent := s.clk.Now().Sub(now)
	if spent >= s.cfg.Tick {
		return 0, nil
	}
	return s.cfg.Tick - spent, nil
}

func (s *Scheduler) applyResponse(ctx context.Context, resp *pb.ExchangeResponse) error {
	var mack []string
	for _, a := range resp.Acks {
		if a.Manifest {
			mack = append(mack, a.ItemId)
			continue
		}
		if err := s.st.SetAcked(ctx, a.ItemId, a.Form, a.AckedThrough, a.Complete); err != nil {
			return err
		}
	}
	if err := s.st.MarkManifestAcked(ctx, mack); err != nil {
		return err
	}
	cursor, err := s.st.GetCursor(ctx)
	if err != nil {
		return err
	}
	for _, d := range resp.Downlink {
		if d.Seq <= cursor {
			continue
		}
		var err error
		switch m := d.Msg.(type) {
		case *pb.Downlink_Policy:
			_, _, err = s.AcceptPolicy(ctx, m.Policy, "downlink")
		case *pb.Downlink_Pull:
			err = s.Pull(ctx, m.Pull)
		case *pb.Downlink_LabelOverride:
			err = s.applyOverride(ctx, m.LabelOverride)
		}
		if err != nil {
			s.log.Warn("apply downlink", "seq", d.Seq, "err", err)
			if errors.Is(err, store.ErrFull) {
				return err // Retry this correction, not the successfully applied prefix.
			}
		} else if s.OnDownlink != nil {
			s.OnDownlink(d)
		}
		if err := s.st.SetCursor(ctx, d.Seq); err != nil {
			return err
		}
		cursor = d.Seq
	}
	if err := s.st.EnforceLimits(ctx); err != nil && !errors.Is(err, store.ErrFull) {
		return err
	}
	return nil
}
