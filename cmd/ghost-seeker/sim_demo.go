//go:build !prod

package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	reviewdataset "github.com/fleethackerghostfleet/ghost-seeker/internal/dataset"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

// Demo/simulation wiring. Production builds (-tags prod) compile this file out
// and use sim_prod.go, which only allows live mode (-shore).

type demoFlags struct {
	negativesRate      *float64
	datasetManifest    *string
	datasetDir         *string
	splitsPath         *string
	retain             *int
	backfill           *int
	captureEvery       *time.Duration
	autoCDR            *time.Duration
	demoLinkControl    *bool
	simLinkBps         *int64
	lowbandHold        *time.Duration
	admin              *bool
	policyState        *string
	devToken           *string
	devTrustPeers      *bool
	demoAcceptAnyToken *bool
}

func registerDemoFlags() *demoFlags {
	d := &demoFlags{}
	d.negativesRate = flag.Float64("negatives-rate", 0.15, "dataset mode: fraction of detector negatives to review")
	d.datasetManifest = flag.String("dataset", "", "simulation mode: replay a clean-v2 review manifest")
	d.datasetDir = flag.String("dataset-dir", "data/review_set/images", "simulation mode: dataset image directory")
	d.splitsPath = flag.String("splits", "edgeml/configs/splits.yaml", "dataset mode: locked test and quarantine source policy")
	d.retain = flag.Int("retain", 260, "maximum stored items")
	d.backfill = flag.Int("backfill", 84, "number of historical items to generate at startup")
	d.captureEvery = flag.Duration("capture-every", 700*time.Millisecond, "simulation: mean edge capture interval (>0), uniformly jittered from 2/3 to 4/3; independent of upload bandwidth")
	d.autoCDR = flag.Duration("auto-cdr", 15*time.Second, "average interval at which a simulated watch officer escalates the top P1/P2 image to the commander (0 = off)")
	d.demoLinkControl = flag.Bool("demo-link-control", false, "live mode: allow POST /api/shore/link to pin the shaper rate ({\"bps\":N} or {\"auto\":true}) so a presenter can build a backlog")
	d.simLinkBps = flag.Int64("sim-link-bps", -1, "simulation: fixed upload rate override in bits/s (-1 = automatic ship bandwidth, 0 = outage)")
	d.lowbandHold = flag.Duration("lowband-hold", 2*time.Minute, "simulation: sustained low/high link duration before metadata-only mode changes")
	adminDefault := strings.EqualFold(os.Getenv("GS_ADMIN"), "true") || os.Getenv("GS_ADMIN") == "1"
	d.admin = flag.Bool("admin", adminDefault, "enable super-admin policy controls")
	d.policyState = flag.String("policy-state", "", "persist simulation policy state at this path")
	d.devToken = flag.String("dev-token-file", envOr("GS_DEV_TOKEN_FILE", opauth.DefaultDevFile), "local demo only: auto-generate the operator token here when none is set and log loopback browsers in automatically ('none' = require an explicit token)")
	d.devTrustPeers = flag.Bool("dev-trust-peers", envBool("GS_DEV_TRUST_PEERS"), "local demo in containers only: with -dev-token-file, also sign in browsers that address localhost from a non-loopback peer (a port published on the host's 127.0.0.1 arrives from the bridge gateway); publish the port on 127.0.0.1 only")
	d.demoAcceptAnyToken = flag.Bool("demo-accept-any-token", false, "INSECURE demo only: accept any non-empty login token as operator (incompatible with PIV)")
	return d
}

func simulationAvailable() error { return nil }

func (d *demoFlags) validate(authMode, shoreURL string) error {
	if *d.simLinkBps < -1 {
		return errors.New("-sim-link-bps must be >= -1")
	}
	if *d.captureEvery <= 0 {
		return errors.New("-capture-every must be >0")
	}
	if *d.demoAcceptAnyToken && authMode != "token" {
		return errors.New("-demo-accept-any-token requires -auth-mode token; incompatible with PIV")
	}
	if math.IsNaN(*d.negativesRate) || *d.negativesRate < 0 || *d.negativesRate > 1 {
		return errors.New("-negatives-rate must be in [0,1]")
	}
	if shoreURL != "" && *d.datasetManifest != "" {
		return errors.New("-dataset cannot be used with -shore")
	}
	return nil
}

func (d *demoFlags) tokenFile() string { return *d.devToken }

func (d *demoFlags) linkControl() bool { return *d.demoLinkControl }

func (d *demoFlags) configureSessions(s *opauth.Sessions) *opauth.Sessions {
	s = s.TrustPeers(*d.devTrustPeers).DemoAcceptAnyToken(*d.demoAcceptAnyToken)
	if *d.demoAcceptAnyToken {
		log.Print("WARNING: INSECURE DEMO MODE (-demo-accept-any-token): ANY non-empty login token grants OPERATOR access. Never enable in production or on an untrusted network.")
	}
	return s
}

func (d *demoFlags) logDevMode(devMode bool, authMode string) {
	if devMode && authMode == "token" {
		peers := "loopback browsers"
		if *d.devTrustPeers {
			peers = "browsers addressing localhost from any peer (-dev-trust-peers)"
		}
		log.Printf("operator token auto-generated for a local demo (%s); %s are signed in automatically", *d.devToken, peers)
	}
}

func runSimulationMode(addr string, d *demoFlags, linkBps int64, reviewPath string, reviewLogSet bool, compiler *command.Compiler, operator string, sessions *opauth.Sessions, transport *tls.Config) {
	*d.simLinkBps = simulationLinkRate(*d.simLinkBps, linkBps)
	if !reviewLogSet && *d.datasetManifest != "" {
		reviewPath = "data/reviews-real.jsonl"
	}
	data := store.New(*d.retain)
	data.SetLowbandHold(*d.lowbandHold)
	var generator *sensor.Generator
	var replay *reviewdataset.Replay
	options := []server.Option{server.WithAdmin(*d.admin), server.WithAdminToken(operator), server.WithNodeID("DDG-51")}
	if *d.datasetManifest != "" {
		images, err := reviewdataset.LoadManifest(*d.datasetManifest)
		if err != nil {
			log.Fatalf("load dataset manifest: %v", err)
		}
		rejected, err := reviewdataset.ValidateSplits(images, *d.splitsPath)
		if err != nil {
			log.Fatalf("validate dataset splits: %v", err)
		}
		log.Printf("dataset split guard: %d locked rows rejected", rejected)
		replayStarted := time.Now()
		replay, err = reviewdataset.NewReplay(images, *d.datasetDir, reviewdataset.Seed)
		if err != nil {
			log.Fatalf("load dataset images: %v", err)
		}
		log.Printf("dataset replay prepared and SHA-verified %d images in %s", len(images), time.Since(replayStarted))
		sources := reviewdataset.Sources(images)
		options = append(options, server.WithDataset(*d.datasetDir, sources))
		summary := reviewdataset.Summarize(images)
		distribution, _ := json.Marshal(summary.DetectionsByClass)
		log.Printf("real clean-v2 dev dataset: %d images; detections=%s; images with person=%d; person-driven P1=%d",
			len(images), distribution, summary.ImagesWithPerson, summary.PersonDrivenP1)
		fillDatasetBacklog(data, replay, *d.backfill, *d.negativesRate)
	} else {
		options = append(options, server.WithDemoReset())
		generator = sensor.NewGenerator()
		fillBacklog(data, generator, *d.backfill)
	}
	if *d.simLinkBps < 0 {
		data.AdvanceUploads(24 * time.Hour)
	}
	uplink := newSimulationUplink(data, *d.simLinkBps, rand.NormFloat64)
	if *d.policyState != "" {
		backend, err := policy.NewFileBackend(*d.policyState)
		if err != nil {
			log.Fatal(err)
		}
		options = append(options, server.WithPolicyBackend(backend))
	}
	app := server.New(data, options...)
	if reviewPath != "" {
		l, err := reviews.Open(reviewPath)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		app.SetReviewLog(l)
	}
	compiler.Locks = app.PolicyLocks()
	app.SetCompiler(compiler)
	if replay != nil {
		go runDatasetSimulation(data, replay, app, uplink, *d.negativesRate, *d.captureEvery)
	} else {
		if *d.autoCDR > 0 {
			for i := 0; i < startSent; i++ {
				data.AutoEscalate()
			}
		}
		data.SeedUploaded(startSent)
		go runSimulation(data, generator, app, uplink, *d.captureEvery)
	}
	go refreshTimeScopes(app)
	if replay == nil && *d.autoCDR > 0 {
		go runWatchOfficer(data, app, *d.autoCDR)
	}

	log.Printf("Ghost Seeker listening on %s", addr)
	log.Fatal(serve(addr, httpx.Secure(httpx.SameOrigin(sessions.Wrap(app))), transport))
}

// startSent is how many top P1 backlog images open already uploaded and
// auto-sent to the Commander; everything else starts queued.
const startSent = 2

func refreshTimeScopes(app *server.Server) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		app.RescoreTimeScoped(now)
	}
}

func fillBacklog(data *store.Store, generator *sensor.Generator, count int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ts := time.Now().Add(-45 * time.Minute)
	for kept := 0; kept < count; {
		ts = ts.Add(time.Duration(rng.Intn(14)+8) * time.Second)
		source := catalog.Sources[rng.Intn(len(catalog.Sources))]
		item, extra := generator.Tick(&source, ts, "maritime", nil)
		addCapture(data, generator, rng, item, extra, "maritime", nil)
		if item != nil && item.Filtered == "" {
			kept++
		}
	}
	seedWhale(data, generator)
}

func fillDatasetBacklog(data *store.Store, replay *reviewdataset.Replay, count int, negativesRate float64) {
	start := time.Now().Add(-45 * time.Minute)
	for index := 0; index < count; index++ {
		item := replay.Next(start.Add(time.Duration(index) * 18 * time.Second))
		item.DetectorNegative = item.Edge < sensor.EdgeKeep
		if item.DetectorNegative && !reviewdataset.SampleNegative(item.ImageID, negativesRate) {
			data.Add(nil, 0)
			continue
		}
		data.Add(&item, 0)
	}
}

func runDatasetSimulation(data *store.Store, replay *reviewdataset.Replay, app *server.Server, uplink *simulationUplink, negativesRate float64, captureEvery time.Duration) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	last := time.Now()
	for {
		time.Sleep(captureDelay(rng, captureEvery))
		now := time.Now()
		tickDatasetSimulation(data, replay, app, uplink, negativesRate, now, now.Sub(last))
		last = now
	}
}

func tickDatasetSimulation(data *store.Store, replay *reviewdataset.Replay, app *server.Server, uplink *simulationUplink, negativesRate float64, now time.Time, elapsed time.Duration) {
	updates := uplink.advance(elapsed)
	if len(updates) > 0 {
		stats, cdr := data.Stats()
		for _, updated := range updates {
			app.PublishUpdate(updated, cdr)
		}
		app.PublishStats(stats)
	}
	_, _, _, state := data.Snapshot()
	item := replay.Next(now)
	item.DetectorNegative = item.Edge < sensor.EdgeKeep
	if item.DetectorNegative && !reviewdataset.SampleNegative(item.ImageID, negativesRate) {
		data.Add(nil, 0)
		stats, _ := data.Stats()
		app.PublishStats(stats)
		return
	}
	command.Rescore(&item, state.Model, data.Directives())
	stored, evicted := data.Add(&item, 0)
	app.PublishEvict(evicted)
	stats, _ := data.Stats()
	app.PublishItem(stored, stats)
}

// addCapture stores one capture and, for about half of the bursts with
// repeated frames, one near-duplicate view so the feed can show it at P4.
func addCapture(data *store.Store, generator *sensor.Generator, rng *rand.Rand, item *command.Item, extra int, model string, directives []command.Directive) (stored []command.Item, evicted []string) {
	if item == nil {
		data.Add(nil, extra)
		return nil, nil
	}
	captures := []*command.Item{item}
	if extra > 0 && item.Filtered == "" && rng.Intn(2) == 0 {
		captures = append(captures, generator.Repeat(item, model, directives))
		extra--
	}
	for _, capture := range captures {
		kept, gone := data.Add(capture, extra)
		extra = 0
		stored = append(stored, kept)
		if gone != "" {
			evicted = append(evicted, gone)
		}
	}
	return stored, evicted
}

func runWatchOfficer(data *store.Store, app *server.Server, every time.Duration) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		time.Sleep(every/2 + time.Duration(rng.Int63n(int64(every))))
		if item, cdr, ok := data.AutoEscalate(); ok {
			app.PublishUpdate(item, cdr)
		}
	}
}

func captureDelay(rng *rand.Rand, mean time.Duration) time.Duration {
	spread := mean / 3
	upper := mean + min(spread, time.Duration(math.MaxInt64)-mean)
	lower := mean - spread
	return lower + time.Duration(rng.Int63n(int64(upper-lower)+1))
}

func runSimulation(data *store.Store, generator *sensor.Generator, app *server.Server, uplink *simulationUplink, captureEvery time.Duration) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	last := time.Now()
	whales := newWhaleCadence(data)
	for {
		time.Sleep(captureDelay(rng, captureEvery))
		now := time.Now()
		updates := uplink.advance(now.Sub(last))
		last = now
		_, cdr := data.Stats()
		for _, updated := range updates {
			app.PublishUpdate(updated, cdr)
		}
		_, _, _, state := data.Snapshot()
		directives := data.Directives()
		item, extra := whales.tick(data, generator, time.Now(), state.Model, directives)
		stored, evicted := addCapture(data, generator, rng, item, extra, state.Model, directives)
		for _, id := range evicted {
			app.PublishEvict(id)
		}
		stats, _ := data.Stats()
		for _, capture := range stored {
			app.PublishItem(capture, stats)
		}
		if len(stored) == 0 {
			app.PublishStats(stats)
		}
	}
}

func enableDemoLinkControl(b *shorebridge.Bridge, on bool) *shorebridge.Bridge {
	return b.WithLinkOverride(on)
}

func simulationLinkRate(rate, legacy int64) int64 {
	if rate == -1 && legacy >= 0 {
		return legacy
	}
	return rate
}

const linkBpsUsage = "legacy fixed link rate in bits/s (live shaper, or simulation alias for -sim-link-bps)"
