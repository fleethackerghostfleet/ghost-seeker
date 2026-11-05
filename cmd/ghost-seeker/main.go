package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	reviewdataset "github.com/fleethackerghostfleet/ghost-seeker/internal/dataset"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"github.com/fleethackerghostfleet/ghost-seeker/web"
)

func main() {
	llmTimeoutDefault := 8 * time.Second
	if value := os.Getenv("GS_LLM_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			log.Printf("invalid GS_LLM_TIMEOUT %q; using 8s", value)
		} else {
			llmTimeoutDefault = timeout
		}
	}
	addr := flag.String("addr", ":8080", "HTTP listen address")
	reviewPath := flag.String("review-log", "data/reviews.jsonl", "append-only review log (empty disables)")
	negativesRate := flag.Float64("negatives-rate", 0.15, "dataset mode: fraction of detector negatives to review")
	datasetManifest := flag.String("dataset", "", "simulation mode: replay a clean-v2 review manifest")
	datasetDir := flag.String("dataset-dir", "data/review_set/images", "simulation mode: dataset image directory")
	splitsPath := flag.String("splits", "edgeml/configs/splits.yaml", "dataset mode: locked test and quarantine source policy")
	retain := flag.Int("retain", 260, "maximum stored items")
	backfill := flag.Int("backfill", 84, "number of historical items to generate at startup")
	captureEvery := flag.Duration("capture-every", 700*time.Millisecond, "simulation: mean edge capture interval (>0), uniformly jittered from 2/3 to 4/3; independent of upload bandwidth")
	autoCDR := flag.Duration("auto-cdr", 15*time.Second, "average interval at which a simulated watch officer escalates the top P1/P2 image to the commander (0 = off)")
	shoreURL := flag.String("shore", "", "live mode: back the UI with this shore console (e.g. http://localhost:8080) instead of the internal simulation")
	operatorID := flag.String("operator-id", envOr("GS_OPERATOR_ID", shorebridge.DefaultOperator), "live mode: identity recorded as the issuer of policies edited from this console (never taken from the browser)")
	edgeURL := flag.String("edge", "http://localhost:8081", "live mode: edge console, polled for the ship-side item count (optional)")
	linkListen := flag.String("link-listen", ":18080", "live mode: automatic 0–2 MB/s ship-to-shore link; point the edge's -shore at it (empty = externally shaped link)")
	demoLinkControl := flag.Bool("demo-link-control", false, "live mode: allow POST /api/shore/link to pin the shaper rate ({\"bps\":N} or {\"auto\":true}) so a presenter can build a backlog")
	simLinkBps := flag.Int64("sim-link-bps", -1, "simulation: fixed upload rate override in bits/s (-1 = automatic ship bandwidth, 0 = outage)")
	linkBps := flag.Int64("link-bps", -1, "legacy fixed link rate in bits/s (live shaper, or simulation alias for -sim-link-bps)")
	poll := flag.Duration("poll", time.Second, "live mode: shore poll interval")
	adminDefault := strings.EqualFold(os.Getenv("GS_ADMIN"), "true") || os.Getenv("GS_ADMIN") == "1"
	admin := flag.Bool("admin", adminDefault, "enable super-admin policy controls")
	policyState := flag.String("policy-state", "", "persist simulation policy state at this path")
	llmURL := flag.String("llm-url", os.Getenv("GS_LLM_URL"), "OpenAI-compatible local LLM base URL")
	researchCompilerURL := flag.String("research-compiler-url", os.Getenv("GS_RESEARCH_COMPILER_URL"), "opt-in local research language compiler origin (e.g. http://localhost:8097)")
	llmModel := flag.String("llm-model", os.Getenv("GS_LLM_MODEL"), "local LLM model name")
	llmTimeout := flag.Duration("llm-timeout", llmTimeoutDefault, "local LLM request timeout")
	opToken := flag.String("operator-token", os.Getenv("GS_OPERATOR_TOKEN"), "operator bearer token shared with shore/edge; the browser exchanges it for a session cookie at /login (prefer env or file)")
	opTokenFile := flag.String("operator-token-file", os.Getenv("GS_OPERATOR_TOKEN_FILE"), "operator token file")
	devTokenFile := flag.String("dev-token-file", envOr("GS_DEV_TOKEN_FILE", opauth.DefaultDevFile), "local demo only: auto-generate the operator token here when none is set and log loopback browsers in automatically ('none' = require an explicit token)")
	devTrustPeers := flag.Bool("dev-trust-peers", envBool("GS_DEV_TRUST_PEERS"), "local demo in containers only: with -dev-token-file, also sign in browsers that address localhost from a non-loopback peer (a port published on the host's 127.0.0.1 arrives from the bridge gateway); publish the port on 127.0.0.1 only")
	authMode := flag.String("auth-mode", envOr("GS_AUTH_MODE", "token"), "web authentication: token or piv")
	reviewLinkKeyFile := flag.String("review-link-key-file", os.Getenv("GS_REVIEW_LINK_KEY_FILE"), "enable review-only bearer link using this private key file (empty = disabled)")
	demoAcceptAnyToken := flag.Bool("demo-accept-any-token", false, "INSECURE demo only: accept any non-empty login token as operator (incompatible with PIV)")
	tlsCert := flag.String("tls-cert", os.Getenv("GS_TLS_CERT"), "HTTPS server certificate")
	tlsKey := flag.String("tls-key", os.Getenv("GS_TLS_KEY"), "HTTPS server private key")
	ca := flag.String("piv-ca-bundle", os.Getenv("GS_PIV_CA_BUNDLE"), "local PIV CA bundle")
	crls := flag.String("piv-crl-dir", os.Getenv("GS_PIV_CRL_DIR"), "local PIV CRL directory")
	grace := flag.Duration("piv-crl-grace", envDuration("GS_PIV_CRL_GRACE", 0), "CRL grace period")
	reload := flag.Duration("piv-reload", envDuration("GS_PIV_RELOAD", time.Minute), "PIV reload interval")
	ocsp := flag.String("piv-ocsp-url", os.Getenv("GS_PIV_OCSP_URL"), "optional on-prem OCSP responder")
	oids := flag.String("piv-policy-oids", os.Getenv("GS_PIV_POLICY_OIDS"), "comma-separated policy OIDs")
	users := flag.String("piv-users", os.Getenv("GS_PIV_USERS"), "PIV user role mapping")
	flag.Parse()
	if *linkBps < -1 || *simLinkBps < -1 {
		log.Fatal("-link-bps and -sim-link-bps must be >= -1")
	}
	*simLinkBps = simulationLinkRate(*simLinkBps, *linkBps)
	if *captureEvery <= 0 {
		log.Fatal("-capture-every must be >0")
	}
	if *demoAcceptAnyToken && *authMode != "token" {
		log.Fatal("-demo-accept-any-token requires -auth-mode token; incompatible with PIV")
	}
	if math.IsNaN(*negativesRate) || *negativesRate < 0 || *negativesRate > 1 {
		log.Fatal("-negatives-rate must be in [0,1]")
	}
	if err := validateReviewLinkAuthMode(*authMode, *reviewLinkKeyFile, *shoreURL); err != nil {
		log.Fatal(err)
	}
	reviewLogSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "review-log" {
			reviewLogSet = true
		}
	})
	if !reviewLogSet && *datasetManifest != "" {
		*reviewPath = "data/reviews-real.jsonl"
	}
	if err := validateAuthMode(*authMode, *tlsCert, *tlsKey, *ca, *crls, *users); err != nil {
		log.Fatal(err)
	}
	var transport *tls.Config

	researchCompiler, err := command.NewResearchClient(*researchCompilerURL)
	if err != nil {
		log.Fatal(err)
	}
	compiler := &command.Compiler{LLM: command.NewLLMClient(*llmURL, *llmModel, *llmTimeout), Research: researchCompiler}
	if researchCompiler != nil {
		log.Printf("NL compiler: research interpreter at %s (existing demo classes; failures block Apply)", *researchCompilerURL)
	} else if *llmURL == "" {
		log.Printf("NL compiler: rules parser (set GS_LLM_URL to enable LLM)")
	} else {
		log.Printf("NL compiler: local LLM at %s", *llmURL)
	}

	operator, devMode, err := opauth.Load(*opToken, *opTokenFile, *devTokenFile)
	if err != nil {
		log.Fatal(err)
	}
	sessions := opauth.NewSessions(operator, devMode).TrustPeers(*devTrustPeers).DemoAcceptAnyToken(*demoAcceptAnyToken)
	if *reviewLinkKeyFile != "" {
		key, err := opauth.LoadOrCreateReviewLinkKey(*reviewLinkKeyFile)
		if err != nil {
			log.Fatalf("review link key: %v", err)
		}
		sessions.WithReviewJoinKey(key)
		log.Printf("review link enabled; key file: %s", *reviewLinkKeyFile)
	}
	if *demoAcceptAnyToken {
		log.Print("WARNING: INSECURE DEMO MODE (-demo-accept-any-token): ANY non-empty login token grants OPERATOR access. Never enable in production or on an untrusted network.")
	}
	if *authMode == "piv" {
		var policies []string
		for _, oid := range strings.Split(*oids, ",") {
			if oid = strings.TrimSpace(oid); oid != "" {
				policies = append(policies, oid)
			}
		}
		auth, err := pivauth.New(pivauth.Config{CABundle: *ca, CRLDir: *crls, CRLGrace: *grace, ReloadInterval: *reload, OCSPURL: *ocsp, PolicyOIDs: policies, UsersFile: *users})
		if err != nil {
			log.Fatalf("PIV authentication: %v", err)
		}
		transport, err = auth.TLSConfig(*tlsCert, *tlsKey)
		if err != nil {
			log.Fatalf("PIV HTTPS: %v", err)
		}
		auth.Start(context.Background())
		sessions.WithCerts(auth, nil)
		log.Print("PIV mode: dev loopback auto-login disabled; bridge bearer token unchanged")
	}
	if devMode && *authMode == "token" {
		peers := "loopback browsers"
		if *devTrustPeers {
			peers = "browsers addressing localhost from any peer (-dev-trust-peers)"
		}
		log.Printf("operator token auto-generated for a local demo (%s); %s are signed in automatically", *devTokenFile, peers)
	}

	if *shoreURL != "" {
		if *datasetManifest != "" {
			log.Fatal("-dataset cannot be used with -shore")
		}
		runShoreMode(*addr, *shoreURL, *edgeURL, *linkListen, *poll, compiler, operator, sessions, *operatorID, transport, *reviewPath, *demoLinkControl, *linkBps)
		return
	}

	data := store.New(*retain)
	var generator *sensor.Generator
	var replay *reviewdataset.Replay
	options := []server.Option{server.WithAdmin(*admin), server.WithAdminToken(operator), server.WithNodeID("DDG-51")}
	if *datasetManifest != "" {
		images, err := reviewdataset.LoadManifest(*datasetManifest)
		if err != nil {
			log.Fatalf("load dataset manifest: %v", err)
		}
		rejected, err := reviewdataset.ValidateSplits(images, *splitsPath)
		if err != nil {
			log.Fatalf("validate dataset splits: %v", err)
		}
		log.Printf("dataset split guard: %d locked rows rejected", rejected)
		replayStarted := time.Now()
		replay, err = reviewdataset.NewReplay(images, *datasetDir, reviewdataset.Seed)
		if err != nil {
			log.Fatalf("load dataset images: %v", err)
		}
		log.Printf("dataset replay prepared and SHA-verified %d images in %s", len(images), time.Since(replayStarted))
		sources := reviewdataset.Sources(images)
		options = append(options, server.WithDataset(*datasetDir, sources))
		summary := reviewdataset.Summarize(images)
		distribution, _ := json.Marshal(summary.DetectionsByClass)
		log.Printf("real clean-v2 dev dataset: %d images; detections=%s; images with person=%d; person-driven P1=%d",
			len(images), distribution, summary.ImagesWithPerson, summary.PersonDrivenP1)
		fillDatasetBacklog(data, replay, *backfill, *negativesRate)
	} else {
		options = append(options, server.WithDemoReset())
		generator = sensor.NewGenerator()
		fillBacklog(data, generator, *backfill)
	}
	if *simLinkBps < 0 {
		data.AdvanceUploads(24 * time.Hour)
	}
	uplink := newSimulationUplink(data, *simLinkBps, rand.NormFloat64)
	if *policyState != "" {
		backend, err := policy.NewFileBackend(*policyState)
		if err != nil {
			log.Fatal(err)
		}
		options = append(options, server.WithPolicyBackend(backend))
	}
	app := server.New(data, options...)
	if *reviewPath != "" {
		l, err := reviews.Open(*reviewPath)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		app.SetReviewLog(l)
	}
	compiler.Locks = app.PolicyLocks()
	app.SetCompiler(compiler)
	if replay != nil {
		go runDatasetSimulation(data, replay, app, uplink, *negativesRate, *captureEvery)
	} else {
		if *autoCDR > 0 {
			for i := 0; i < startSent; i++ {
				data.AutoEscalate()
			}
		}
		data.SeedUploaded(startSent)
		go runSimulation(data, generator, app, uplink, *captureEvery)
	}
	go refreshTimeScopes(app)
	if replay == nil && *autoCDR > 0 {
		go runWatchOfficer(data, app, *autoCDR)
	}

	log.Printf("Ghost Seeker listening on %s", *addr)
	log.Fatal(serve(*addr, httpx.Secure(httpx.SameOrigin(sessions.Wrap(app))), transport))
}

func serve(addr string, handler http.Handler, transport ...*tls.Config) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if len(transport) > 0 && transport[0] != nil {
		srv.TLSConfig = transport[0]
		return srv.ListenAndServeTLS("", "")
	}
	return srv.ListenAndServe()
}

func refreshTimeScopes(app *server.Server) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		app.RescoreTimeScoped(now)
	}
}

// startSent is how many top P1 backlog images open already uploaded and
// auto-sent to the Commander; everything else starts queued.
const startSent = 2

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

func simulationLinkRate(rate, legacy int64) int64 {
	if rate == -1 && legacy >= 0 {
		return legacy
	}
	return rate
}

func liveLinkShaper(target string, rate int64) *shaper.Shaper {
	if rate >= 0 {
		return shaper.New(target, rate)
	}
	return shaper.NewNormal(target)
}

// runShoreMode serves the operator UI on top of a live shore node: the
// bridge polls the shore ConsoleService and the shaper is the ship-to-shore
// link the edge talks through, with an automatically varying normal rate.
func runShoreMode(addr, shoreURL, edgeURL, linkListen string, poll time.Duration, compiler *command.Compiler, operator string, sessions *opauth.Sessions, operatorID string, transport *tls.Config, reviewPath string, demoLinkControl bool, linkBps int64) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var link shorebridge.LinkControl
	if linkListen != "" {
		target, err := shaperTarget(shoreURL)
		if err != nil {
			log.Fatalf("-shore: %v", err)
		}
		sh := liveLinkShaper(target, linkBps)
		go func() {
			if err := sh.ListenAndServe(ctx, linkListen); err != nil {
				log.Fatalf("shaper: %v", err)
			}
		}()
		if linkBps >= 0 {
			log.Printf("link shaper on %s -> %s: fixed %d bits/s", linkListen, target, linkBps)
		} else {
			log.Printf("link shaper on %s -> %s: automatic normal distribution, 0–2 MB/s, mean 0.5 MB/s, stddev 0.30 MB/s, ~5%% outages, every 5s (start the edge with -shore http://localhost%s)", linkListen, target, linkListen)
		}
		link = sh
	}
	bridge := shorebridge.New(shoreURL, edgeURL, link).WithToken(operator).WithOperator(operatorID).WithLinkOverride(demoLinkControl)
	compiler.Locks = bridge.PolicyLocks()
	bridge.Compiler = compiler
	console := shorebridge.NewConsole(bridge)
	if reviewPath != "" {
		l, err := reviews.Open(reviewPath)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		console.SetReviewLog(l)
	}
	go bridge.Run(ctx, poll)

	// One site: Patrick's operator console (index.html / cdr.html) on live
	// shore items. The console speaks the simulation's API (/api/config,
	// /api/stream, /api/items, /api/directives); shore-only actions (pulls,
	// overrides) and link status stay under /api/shore/ and blobs are proxied.
	mux := http.NewServeMux()
	mux.Handle("/api/shore/", bridge.Handler())
	mux.Handle("/blob/", bridge.Handler())
	mux.Handle("/api/", console.Handler())
	mux.Handle("GET /cdr", http.RedirectHandler("/cdr.html", http.StatusFound))
	mux.Handle("GET /shore.html", http.RedirectHandler("/", http.StatusFound)) // retired F2 UI; same console at /
	mux.Handle("/", web.Handler())
	log.Printf("Ghost Seeker (live shore mode, shore=%s) listening on %s", shoreURL, addr)
	log.Fatal(serve(addr, httpx.Secure(httpx.SameOrigin(sessions.Wrap(mux))), transport))
}

func envBool(k string) bool {
	v, _ := strconv.ParseBool(os.Getenv(k))
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func shaperTarget(shoreURL string) (string, error) {
	u, err := url.Parse(shoreURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	return host, nil
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
		item, extra := generator.Tick(nil, time.Now(), state.Model, directives)
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
