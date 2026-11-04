// sim runs edge + shore in one process over a simulated link with a virtual
// clock, for both strategies, and prints what reached shore when. This is
// the metrics harness behind the FIFO-vs-prioritized chart.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	estore "github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scenario"
	sstore "github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

type sample struct {
	T             float64 `json:"t_s"`
	Ingested      int64   `json:"ingested"`
	Known         int64   `json:"shore_knows"` // manifest or any bytes
	Fact          int64   `json:"fact"`
	Thumb         int64   `json:"thumb"`
	Full          int64   `json:"full"`
	UnknownsKnown int64   `json:"unknowns_known"`
	UnknownsFact  int64   `json:"unknowns_fact"`
	UnknownsFull  int64   `json:"unknowns_full"`
	BytesOnAir    int64   `json:"bytes_on_air"`
	LinkBps       float64 `json:"link_bps"`
}

type result struct {
	Strategy            string           `json:"strategy"`
	Trace               string           `json:"trace"`
	DurationS           float64          `json:"duration_s"`
	Ingested            int64            `json:"ingested"`
	Unknowns            int64            `json:"unknowns"`
	FirstUnknownAnyS    float64          `json:"first_unknown_any_s"` // capture → first byte/manifest at shore
	FirstUnknownFactS   float64          `json:"first_unknown_fact_s"`
	FirstUnknownFullS   float64          `json:"first_unknown_full_s"`
	MedianKnownLatencyS float64          `json:"median_known_latency_s"`
	ShoreKnowsAtEnd     int64            `json:"shore_knows_at_end"`
	FullAtEnd           int64            `json:"full_at_end"`
	BytesOnAir          int64            `json:"bytes_on_air"`
	FullByLabel         map[string]int64 `json:"full_by_label"`
	FullAfterPolicy     map[string]int64 `json:"full_after_policy_by_label"`
	Exchanges           int64            `json:"exchanges"`
	Failures            int64            `json:"failures"`
	Samples             []sample         `json:"samples"`
}

func main() {
	traceName := flag.String("trace", "satcom_contested", "link trace: steady_64k|trickle_2k4|starlink_turns|satcom_contested|los_bursty")
	frames := flag.String("frames", "frames", "frame zip directory (for the default scenario)")
	scen := flag.String("scenario", "", "scenario JSON (default: built-in from -frames)")
	dur := flag.Duration("duration", 90*time.Minute, "simulated duration")
	strategies := flag.String("strategies", "fifo,prioritized", "comma list")
	out := flag.String("out", "", "write JSON results here")
	seed := flag.Int64("seed", 7, "loss seed")
	sampleEvery := flag.Duration("sample", time.Minute, "metrics sample interval")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	tr, ok := link.Traces[*traceName]
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown trace", *traceName)
		os.Exit(2)
	}
	var sc *scenario.Scenario
	var err error
	if *scen != "" {
		sc, err = scenario.Load(*scen)
	} else {
		sc = scenario.Default(*frames)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var results []result
	for _, s := range splitComma(*strategies) {
		r, err := run(scheduler.Strategy(s), tr, sc, *dur, *seed, *sampleEvery)
		if err != nil {
			fmt.Fprintln(os.Stderr, s, err)
			os.Exit(1)
		}
		results = append(results, *r)
	}
	printTable(results)
	if *out != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func run(strat scheduler.Strategy, tr link.Trace, sc *scenario.Scenario, dur time.Duration, seed int64, sampleEvery time.Duration) (*result, error) {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "gs-sim-"+string(strat)+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)

	sst, err := sstore.Open(filepath.Join(dir, "shore"))
	if err != nil {
		return nil, err
	}
	defer sst.Close()
	shore := ssync.New(sst, clk, nil)

	est, err := estore.Open(filepath.Join(dir, "edge"))
	if err != nil {
		return nil, err
	}
	defer est.Close()
	lk := link.NewSim(tr, clk, shore.Exchange, seed)
	cfg := scheduler.DefaultConfig("edge-1")
	cfg.Strategy = strat
	sch, err := scheduler.New(cfg, est, lk, scoring.Default(), clk, nil)
	if err != nil {
		return nil, err
	}
	mock := inference.NewMock()
	icfg := ingest.DefaultConfig()
	icfg.EdgeID = fmt.Sprintf("edge-%v", strat)
	ing := ingest.New(icfg, est, mock, sch, clk, nil)
	src, err := ingest.NewReplay(clk, 0, sc.Sets...)
	if err != nil {
		return nil, err
	}

	// Bookkeeping: capture time per item, unknown flags, first arrival times.
	captured := map[string]time.Time{}
	labels := map[string]string{}
	unknown := map[string]bool{}
	var unknownOrder []string
	firstAny := map[string]time.Time{}
	firstForm := map[string]map[pb.FormKind]time.Time{}
	ing.OnItem = func(r *estore.Row) {
		captured[r.Item.Id] = time.UnixMilli(r.Item.CapturedAtUnixMs)
		labels[r.Item.Id], _ = scoring.TopLabel(r.Item, 0)
		if r.Item.GetFeatures().GetUnknownness() >= scoring.DefaultUnknownMin {
			unknown[r.Item.Id] = true
			unknownOrder = append(unknownOrder, r.Item.Id)
		}
	}
	noteKnown := func(id string) {
		if _, ok := firstAny[id]; !ok {
			firstAny[id] = clk.Now()
		}
	}
	shore.OnArrival = func(id string, k pb.FormKind) {
		noteKnown(id)
		if firstForm[id] == nil {
			firstForm[id] = map[pb.FormKind]time.Time{}
		}
		if _, ok := firstForm[id][k]; !ok {
			firstForm[id][k] = clk.Now()
		}
	}

	period := time.Duration(sc.PeriodS * float64(time.Second))
	nextCapture := start
	nextTick := start
	nextSample := start
	events := append([]scenario.Event(nil), sc.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].AtS < events[j].AtS })
	res := &result{Strategy: string(strat), Trace: tr.Name, DurationS: dur.Seconds()}
	end := start.Add(dur)
	srcDone := false

	for clk.Now().Before(end) {
		now := clk.Now()
		// Fire due operator events (via shore, like a real operator would).
		for len(events) > 0 && !now.Before(start.Add(events[0].At())) {
			ev := events[0]
			events = events[1:]
			if p, _ := ev.PolicyMsg(); p != nil {
				p.Id = fmt.Sprintf("p-%d", ev.At()/time.Second)
				_ = sst.SetPolicy(ctx, p)
				_, _ = sst.EnqueueDownlink(ctx, "", &pb.Downlink{Msg: &pb.Downlink_Policy{Policy: p}}, now)
			}
			if ev.PullNthUnknown > 0 && ev.PullNthUnknown <= len(unknownOrder) {
				id := unknownOrder[ev.PullNthUnknown-1]
				_, _ = sst.EnqueueDownlink(ctx, "", &pb.Downlink{Msg: &pb.Downlink_Pull{Pull: &pb.Pull{ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: "shore/analyst"}}}, now)
			}
			if p, _ := ev.PullMsg(); p != nil {
				_, _ = sst.EnqueueDownlink(ctx, "", &pb.Downlink{Msg: &pb.Downlink_Pull{Pull: p}}, now)
			}
			if o, _ := ev.OverrideMsg(); o != nil {
				_, _ = sst.EnqueueDownlink(ctx, "", &pb.Downlink{Msg: &pb.Downlink_LabelOverride{LabelOverride: o}}, now)
			}
		}
		if !now.Before(nextSample) {
			res.Samples = append(res.Samples, snapshot(ctx, now.Sub(start), sst, ing.Count, unknown, lk))
			nextSample = nextSample.Add(sampleEvery)
		}
		// Discrete events: whichever is due first, capture or scheduler tick.
		if !srcDone && !nextCapture.After(nextTick) {
			if nextCapture.After(now) {
				clk.Advance(nextCapture.Sub(now))
			}
			c, done, err := src.Next(ctx)
			if err != nil {
				return nil, err
			}
			if done {
				srcDone = true
				continue
			}
			if _, err := ing.Ingest(ctx, c); err != nil {
				return nil, err
			}
			nextCapture = nextCapture.Add(period)
			continue
		}
		if nextTick.After(now) {
			clk.Advance(nextTick.Sub(now))
		}
		wait, _ := sch.Tick(ctx)
		if wait <= 0 {
			wait = 100 * time.Millisecond
		}
		nextTick = clk.Now().Add(wait)
	}
	res.Samples = append(res.Samples, snapshot(ctx, clk.Now().Sub(start), sst, ing.Count, unknown, lk))

	// Shore "knows" via manifest too: fold manifest times in.
	items, _, err := sst.List(ctx, sstore.ListFilter{Limit: 1 << 20})
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ManifestAt.UnixMilli() > 0 {
			if t, ok := firstAny[it.Manifest.ItemId]; !ok || it.ManifestAt.Before(t) {
				firstAny[it.Manifest.ItemId] = it.ManifestAt
			}
		}
	}
	res.Ingested = ing.Count
	res.Unknowns = int64(len(unknown))
	res.ShoreKnowsAtEnd = int64(len(firstAny))
	var lat []float64
	fu := func(get func(id string) (time.Time, bool)) float64 {
		best := -1.0
		for id := range unknown {
			if t, ok := get(id); ok {
				d := t.Sub(captured[id]).Seconds()
				if best < 0 || d < best {
					best = d
				}
			}
		}
		return best
	}
	res.FirstUnknownAnyS = fu(func(id string) (time.Time, bool) { t, ok := firstAny[id]; return t, ok })
	res.FirstUnknownFactS = fu(func(id string) (time.Time, bool) { t, ok := firstForm[id][pb.FormKind_FORM_KIND_FACT]; return t, ok })
	res.FirstUnknownFullS = fu(func(id string) (time.Time, bool) { t, ok := firstForm[id][pb.FormKind_FORM_KIND_FULL]; return t, ok })
	for id, t := range firstAny {
		lat = append(lat, t.Sub(captured[id]).Seconds())
	}
	sort.Float64s(lat)
	if len(lat) > 0 {
		res.MedianKnownLatencyS = lat[len(lat)/2]
	}
	res.FullByLabel, res.FullAfterPolicy = map[string]int64{}, map[string]int64{}
	var policyAt time.Time
	for _, ev := range sc.Events {
		if ev.Policy != nil && (policyAt.IsZero() || start.Add(ev.At()).Before(policyAt)) {
			policyAt = start.Add(ev.At())
		}
	}
	for _, it := range items {
		f := firstForm[it.Manifest.ItemId]
		t, ok := f[pb.FormKind_FORM_KIND_FULL]
		if !ok {
			continue
		}
		res.FullAtEnd++
		label := labels[it.Manifest.ItemId]
		if label == "" {
			label = "(none)"
		}
		if unknown[it.Manifest.ItemId] {
			label = "UNKNOWN"
		}
		res.FullByLabel[label]++
		if !policyAt.IsZero() && t.After(policyAt) {
			res.FullAfterPolicy[label]++
		}
	}
	st := sch.Stats()
	res.Exchanges, res.Failures, res.BytesOnAir = st.Exchanges, st.Failures, lk.BytesOnAir
	return res, nil
}

func snapshot(ctx context.Context, t time.Duration, sst *sstore.Store, ingested int64, unknown map[string]bool, lk *link.Sim) sample {
	s := sample{T: t.Seconds(), Ingested: ingested, BytesOnAir: lk.BytesOnAir, LinkBps: lk.Current().Bps}
	items, _, err := sst.List(ctx, sstore.ListFilter{Limit: 1 << 20})
	if err != nil {
		return s
	}
	for _, it := range items {
		known := it.ManifestAt.UnixMilli() > 0
		var fact, thumb, full bool
		for _, f := range it.Forms {
			if f.Received > 0 {
				known = true
			}
			if f.Complete {
				switch f.Kind {
				case pb.FormKind_FORM_KIND_FACT:
					fact = true
				case pb.FormKind_FORM_KIND_THUMB:
					thumb = true
				case pb.FormKind_FORM_KIND_FULL:
					full = true
				}
			}
		}
		u := unknown[it.Manifest.ItemId]
		if known {
			s.Known++
			if u {
				s.UnknownsKnown++
			}
		}
		if fact {
			s.Fact++
			if u {
				s.UnknownsFact++
			}
		}
		if thumb {
			s.Thumb++
		}
		if full {
			s.Full++
			if u {
				s.UnknownsFull++
			}
		}
	}
	return s
}

func printTable(rs []result) {
	fmt.Printf("%-12s %-16s %8s %8s %10s %10s %10s %10s %8s %8s %10s %6s\n",
		"strategy", "trace", "ingested", "unknowns", "1stUnk any", "1stUnk fact", "1stUnk full", "med known", "known", "full", "bytes air", "fail")
	for _, r := range rs {
		fmt.Printf("%-12s %-16s %8d %8d %10s %10s %10s %10s %8d %8d %10d %6d\n",
			r.Strategy, r.Trace, r.Ingested, r.Unknowns, secs(r.FirstUnknownAnyS), secs(r.FirstUnknownFactS), secs(r.FirstUnknownFullS),
			secs(r.MedianKnownLatencyS), r.ShoreKnowsAtEnd, r.FullAtEnd, r.BytesOnAir, r.Failures)
	}
}

func secs(s float64) string {
	if s < 0 {
		return "never"
	}
	return fmt.Sprintf("%.0fs", s)
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
