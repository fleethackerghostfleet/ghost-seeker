//go:build !prod

// sim (demo builds only) runs edge + shore in one process over a simulated link with a virtual
// clock, for both strategies, and prints what reached shore when. This is
// the metrics harness behind the FIFO-vs-prioritized chart.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	// Demo comparison (set when -dedup is used and the clips carry MOT gt).
	ObjectsWithImage int64 `json:"objects_with_image"`
	ItemsWithImage   int64 `json:"items_with_image"`
}

type result struct {
	Strategy            string           `json:"strategy"`
	Dedup               string           `json:"dedup,omitempty"`
	Trace               string           `json:"trace"`
	LinkBps             float64          `json:"link_bps,omitempty"`
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
	Skipped             int64            `json:"skipped_frames"`
	ObjectsTotal        int64            `json:"objects_total"`
	ObjectsWithImage    int64            `json:"objects_with_image"`
	ItemsWithImage      int64            `json:"items_with_image"`
	BytesPerObject      float64          `json:"bytes_per_delivered_object"`
	AllObjectsAtS       float64          `json:"all_objects_with_image_at_s"` // -1 = never
	Samples             []sample         `json:"samples"`
}

type runOpts struct {
	dedup       string
	gt          *gtIndex
	updateEvery time.Duration
	inferURL    string // sidecar for the tomer row (ImagePriority); mock otherwise
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// tomerAnalyzer keeps the mock's ground-truth detections (so object
// bookkeeping is identical across rows) and adds the real sidecar's
// ImagePriority. Each run gets its own source prefix so the sidecar's
// per-source duplicate window starts empty.
type tomerAnalyzer struct {
	mock   inference.Analyzer
	remote inference.Analyzer
	prefix string
}

func (t *tomerAnalyzer) Analyze(ctx context.Context, f inference.Frame, known []string, hints map[string]string) (*pb.AnalyzeResponse, error) {
	resp, err := t.mock.Analyze(ctx, f, known, hints)
	if err != nil {
		return nil, err
	}
	rf := f
	rf.Source = t.prefix + f.Source
	rr, err := t.remote.Analyze(ctx, rf, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("tomer sidecar: %w", err)
	}
	if rr.ImagePriority == nil {
		return nil, fmt.Errorf("tomer sidecar returned no image_priority; start it with --prioritizer tomer|both")
	}
	resp.ImagePriority = rr.ImagePriority
	return resp, nil
}

func (t *tomerAnalyzer) EmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	return t.mock.EmbedText(ctx, texts)
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
	dedup := flag.String("dedup", "", "demo comparison: comma list of naive|hash|sightings|tomer (or 'all'); runs each mode under each -bps rate")
	inferURL := flag.String("infer", envOr("INFER_URL", "http://127.0.0.1:9090"), "sidecar URL for the tomer row (must run --prioritizer tomer|both)")
	bpsList := flag.String("bps", "", "comma list of fixed link rates in bit/s that replace -trace (e.g. 9600,64000,256000,2000000; 0 = link down)")
	md := flag.String("md", "", "write a markdown summary table here")
	updateEvery := flag.Duration("sighting-update", 30*time.Second, "sightings mode: refresh an object's image at most this often")
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
	traces := []link.Trace{tr}
	if *bpsList != "" {
		traces = nil
		for _, b := range splitComma(*bpsList) {
			bps, err := strconv.ParseFloat(b, 64)
			if err != nil {
				fmt.Fprintln(os.Stderr, "bad -bps", b)
				os.Exit(2)
			}
			traces = append(traces, fixedTrace(bps))
		}
	}
	modes := []string{""}
	var gt *gtIndex
	if *dedup != "" {
		modes = splitComma(*dedup)
		if *dedup == "all" {
			modes = dedupModes
		}
		gt = loadGT(sc.Sets)
	}
	var results []result
	for _, tr := range traces {
		for _, mode := range modes {
			for _, s := range splitComma(*strategies) {
				r, err := run(scheduler.Strategy(s), tr, sc, *dur, *seed, *sampleEvery, runOpts{dedup: mode, gt: gt, updateEvery: *updateEvery, inferURL: *inferURL})
				if err != nil {
					fmt.Fprintln(os.Stderr, s, err)
					os.Exit(1)
				}
				results = append(results, *r)
			}
		}
	}
	if *dedup != "" {
		printCompare(os.Stdout, results)
		if *md != "" {
			var buf strings.Builder
			printCompare(&buf, results)
			if err := os.WriteFile(*md, []byte(buf.String()), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	} else {
		printTable(results)
	}
	if *out != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func run(strat scheduler.Strategy, tr link.Trace, sc *scenario.Scenario, dur time.Duration, seed int64, sampleEvery time.Duration, opts runOpts) (*result, error) {
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
	eng := scoring.Default()
	var an inference.Analyzer = inference.NewMock()
	icfg := ingest.DefaultConfig()
	icfg.EdgeID = "sim" // same ids across strategies so results can be joined by item
	if opts.dedup == "tomer" {
		eng = scoring.Tomer()
		icfg.Prioritizer, icfg.Dedup = scoring.PrioritizerTomer, ingest.DedupHash
		an = &tomerAnalyzer{mock: an, remote: inference.NewRemote(opts.inferURL), prefix: fmt.Sprintf("sim-%s-%d-%d/", strat, seed, time.Now().UnixNano())}
	}
	sch, err := scheduler.New(cfg, est, lk, eng, clk, nil)
	if err != nil {
		return nil, err
	}
	ing := ingest.New(icfg, est, an, sch, clk, nil)
	replay, err := ingest.NewReplay(clk, 0, sc.Sets...)
	if err != nil {
		return nil, err
	}
	src := newDedup(replay, opts.dedup, opts.gt, clk, opts.updateEvery)
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
	res := &result{Strategy: string(strat), Dedup: opts.dedup, Trace: tr.Name, DurationS: dur.Seconds(), AllObjectsAtS: -1}
	if len(tr.Windows) == 1 {
		res.LinkBps = tr.Windows[0].Bps
	}
	if opts.gt != nil {
		res.ObjectsTotal = int64(len(opts.gt.objects))
	}
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
			if c == nil { // dropped by the dedup strategy; the clock still moves
				nextCapture = nextCapture.Add(period)
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
	res.Skipped = src.Skipped + ing.Deduped() // tomer: near-duplicates fold inside the ingester
	if last := res.Samples[len(res.Samples)-1]; true {
		res.ObjectsWithImage, res.ItemsWithImage = last.ObjectsWithImage, last.ItemsWithImage
	}
	if res.ObjectsWithImage > 0 {
		res.BytesPerObject = float64(res.BytesOnAir) / float64(res.ObjectsWithImage)
	}
	for _, smp := range res.Samples {
		if res.ObjectsTotal > 0 && smp.ObjectsWithImage >= res.ObjectsTotal {
			res.AllObjectsAtS = smp.T
			break
		}
	}
	return res, nil
}

func snapshot(ctx context.Context, t time.Duration, sst *sstore.Store, ingested int64, unknown map[string]bool, lk *link.Sim) sample {
	s := sample{T: t.Seconds(), Ingested: ingested, BytesOnAir: lk.BytesOnAir, LinkBps: lk.Current().Bps}
	items, _, err := sst.List(ctx, sstore.ListFilter{Limit: 1 << 20})
	if err == nil {
		objs, withImage := deliveredObjects(items)
		s.ObjectsWithImage, s.ItemsWithImage = int64(len(objs)), withImage
	}
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

// fixedTrace is a flat link at bps bits/s (0 = down) for the preset sweep.
func fixedTrace(bps float64) link.Trace {
	rtt := 120 * time.Millisecond
	if bps > 0 && bps <= 64_000 {
		rtt = 600 * time.Millisecond
	}
	name := fmt.Sprintf("fixed_%gk", bps/1000)
	if bps == 0 {
		name = "fixed_down"
	}
	return link.Trace{Name: name, Windows: []link.Window{{Duration: 1000 * time.Hour, Bps: bps, RTT: rtt}}}
}

// printCompare writes the three-way table: one row per link rate × dedup
// mode, with unique objects delivered with an image over time.
func printCompare(w io.Writer, rs []result) {
	var ts []float64
	if len(rs) > 0 {
		for _, smp := range rs[0].Samples {
			ts = append(ts, smp.T)
		}
	}
	fmt.Fprintf(w, "| link | dedup | strategy | items sent | frames dropped | objects w/ image | all objects at | bytes on air | bytes / delivered object |")
	for _, t := range ts {
		fmt.Fprintf(w, " t=%s |", secs(t))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "|---|---|---|---:|---:|---:|---:|---:|---:|")
	for range ts {
		fmt.Fprintf(w, "---:|")
	}
	fmt.Fprintln(w)
	for _, r := range rs {
		linkName := r.Trace
		if r.LinkBps > 0 {
			linkName = fmt.Sprintf("%g kbps", r.LinkBps/1000)
		} else if len(r.Trace) > 0 && r.Trace == "fixed_down" {
			linkName = "0 (down)"
		}
		all := "never"
		if r.AllObjectsAtS >= 0 {
			all = secs(r.AllObjectsAtS)
		}
		bpo := "-"
		if r.BytesPerObject > 0 {
			bpo = fmt.Sprintf("%.0f KB", r.BytesPerObject/1024)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %d | %d | %d/%d | %s | %.0f KB | %s |", linkName, r.Dedup, r.Strategy, r.Ingested, r.Skipped, r.ObjectsWithImage, r.ObjectsTotal, all, float64(r.BytesOnAir)/1024, bpo)
		for _, smp := range r.Samples {
			fmt.Fprintf(w, " %d |", smp.ObjectsWithImage)
		}
		fmt.Fprintln(w)
	}
}
