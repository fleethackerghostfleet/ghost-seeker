// edge is the shipboard node: ingest, store, score, schedule, and serve the
// ship-side console. Talks to shore over the Link; everything else is local.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/console"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

func main() {
	id := flag.String("id", env("EDGE_ID", "edge-1"), "edge id")
	data := flag.String("data", env("DATA_DIR", "data/edge"), "data directory")
	listen := flag.String("listen", env("LISTEN", ":8081"), "console listen address")
	shoreURL := flag.String("shore", env("SHORE_URL", "http://localhost:8080"), "shore base URL (put Toxiproxy here to degrade)")
	inferURL := flag.String("infer", env("INFER_URL", defaultInferURL), inferUsage)
	sceneEvery := flag.Duration("scene-every", 60*time.Second, "sightings mode: store at most one object-free 'scene' frame (FACT+THUMB) per source per interval; 0 = one item per frame")
	prioritizer := flag.String("prioritizer", env("PRIORITIZER", "edgeml"), "edgeml (object-level: unknownness, labels, confidence) | tomer (algorithm/ghost_priority image-level score; sidecar must run --prioritizer tomer|both)")
	dedup := flag.String("dedup", env("DEDUP", "hash"), "naive (item per frame) | hash (exact-duplicate frames fold) | sightings (one item per object; needs a sidecar that returns sightings)")
	uiDir := flag.String("ui", env("UI_DIR", "ui"), "static UI directory")
	watchDir := flag.String("watch-dir", env("GS_WATCH_DIR", ""), "live image spool directory (writable; optional <image>.json sidecar)")
	liveSource := flag.String("live-source", env("GS_LIVE_SOURCE", "camera-1"), "default camera id for spool/MJPEG")
	liveBytes := flag.Int64("ingest-max-bytes", ingest.DefaultMaxFrameBytes, "maximum live frame request/file bytes")
	liveQueue := flag.Int("ingest-queue", 4, "maximum live frames being read, queued or processed")
	watchPoll := flag.Duration("watch-poll", 500*time.Millisecond, "spool poll/stability interval")
	mjpegURL := flag.String("mjpeg-url", env("GS_MJPEG_URL", ""), "optional HTTP multipart MJPEG camera URL")
	mjpegFPS := flag.Float64("mjpeg-fps", 2, "maximum MJPEG frames per second (0 < fps <= 60)")
	tick := flag.Duration("tick", 3*time.Second, "scheduler tick")
	initBps := flag.Float64("init-bps", 64000, "initial link estimate (bits/s)")
	icfg := ingest.DefaultConfig()
	flag.IntVar(&icfg.ThumbWidth, "thumb-width", icfg.ThumbWidth, "THUMB max width in px (0 = unbounded)")
	flag.IntVar(&icfg.ThumbHeight, "thumb-height", icfg.ThumbHeight, "THUMB max height in px (0 = unbounded)")
	flag.IntVar(&icfg.ThumbQual, "thumb-quality", icfg.ThumbQual, "THUMB JPEG quality 1-100")
	flag.IntVar(&icfg.CropQual, "crop-quality", icfg.CropQual, "CROP JPEG quality 1-100")
	flag.Float64Var(&icfg.CropPad, "crop-pad", icfg.CropPad, "CROP padding per side, as a fraction of the detection box size")
	flag.BoolVar(&icfg.CropUnknowns, "crop-unknowns", icfg.CropUnknowns, "also send CROP for unknown items (default: unknowns skip CROP so the whole frame is kept for training)")
	flag.IntVar(&icfg.FullQual, "full-quality", icfg.FullQual, "JPEG quality for re-encoding non-JPEG FULL captures (0 = send original bytes)")
	tokenFlag := flag.String("unit-token", env("GS_UNIT_TOKEN", ""), "unit JWT (prefer env or file)")
	tokenFile := flag.String("unit-token-file", env("GS_UNIT_TOKEN_FILE", ""), "unit JWT file")
	maxBytes := flag.Int64("max-bytes", envInt64("GS_EDGE_MAX_BYTES"), "edge payload byte cap, incl. originals kept for full-size overrides (0 = unlimited)")
	maxItems := flag.Int64("max-items", envInt64("GS_EDGE_MAX_ITEMS"), "edge item cap (0 = unlimited)")
	requireSignedPolicy := flag.Bool("require-signed-policy", envBool("GS_REQUIRE_SIGNED_POLICY"), "require signed downlinked policies")
	policyKeysPath := flag.String("policy-keys", env("GS_POLICY_KEYS", ""), "JSON public keyring for signed policies")
	opToken := flag.String("operator-token", env("GS_OPERATOR_TOKEN", ""), "operator bearer token for the ship-side console and /blob/ (prefer env or file)")
	opTokenFile := flag.String("operator-token-file", env("GS_OPERATOR_TOKEN_FILE", ""), "operator token file")
	devTokenFile := opauth.DevTokenFileFlag(flag.CommandLine)
	corsOrigins := flag.String("cors-origins", env("GS_CORS_ORIGINS", ""), "comma-separated browser origins allowed to call the API cross-origin (default: same-origin only)")
	publicHosts := flag.String("public-hosts", env("GS_PUBLIC_HOSTS", ""), "comma-separated extra Host names browsers and clients use to reach this node (always allowed: localhost, loopback and IP literals, this machine's hostname, the -listen host); requests with any other Host get 403 (DNS-rebinding guard)")
	replay := registerReplayFlags()
	flag.Parse()
	if *liveBytes <= 0 || *liveQueue < 1 || *liveQueue > 256 || *watchPoll <= 0 || *mjpegFPS <= 0 || *mjpegFPS > 60 || math.IsNaN(*mjpegFPS) {
		fatal(fmt.Errorf("invalid live ingest limits"))
	}
	if (*watchDir != "" || *mjpegURL != "") && !ids.ValidName(*liveSource) {
		fatal(fmt.Errorf("invalid live source id"))
	}
	if *mjpegURL != "" {
		u, err := url.Parse(*mjpegURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			fatal(fmt.Errorf("mjpeg-url must be HTTP(S)"))
		}
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	token, err := unitauth.Credential(*tokenFlag, *tokenFile)
	fatal(err)
	if token == "" {
		log.Warn("no unit token; shore rejects unauthenticated edges unless it runs with -require-auth=false (make demo-edge mints one from data/unit.key)")
	}
	operator, devMode, err := opauth.Load(*opToken, *opTokenFile, *devTokenFile)
	fatal(err)
	if devMode {
		log.Warn("operator token auto-generated for a local demo; set GS_OPERATOR_TOKEN in production", "file", *devTokenFile)
	}

	st, err := store.Open(*data)
	fatal(err)
	defer st.Close()
	if err := st.SetLimits(context.Background(), store.Limits{MaxBytes: *maxBytes, MaxItems: *maxItems}); err != nil && !errors.Is(err, store.ErrFull) {
		fatal(err)
	}
	clk := clock.Real{}
	lk := link.NewConnect(*shoreURL, 5*time.Minute, link.NewEstimator(*initBps, 300, 1e9), token)
	cfg := scheduler.DefaultConfig(*id)
	cfg.Tick = *tick
	cfg.RequireSignedPolicy = *requireSignedPolicy
	if *policyKeysPath != "" {
		cfg.PolicyKeys, err = policy.LoadKeyring(*policyKeysPath)
		fatal(err)
	}
	if cfg.RequireSignedPolicy && *policyKeysPath == "" {
		fatal(fmt.Errorf("-policy-keys or GS_POLICY_KEYS is required with -require-signed-policy"))
	}
	eng, err := scoring.ForPrioritizer(*prioritizer)
	if err != nil {
		log.Error("prioritizer", "err", err)
		os.Exit(2)
	}
	sch, err := scheduler.New(cfg, st, lk, eng, clk, log)
	fatal(err)

	an, err := newAnalyzer(*inferURL)
	fatal(err)
	icfg.EdgeID = *id
	icfg.WaitForSpace = true
	icfg.Dedup, err = ingest.ParseDedup(*dedup)
	fatal(err)
	icfg.SceneEvery = *sceneEvery
	icfg.Prioritizer = *prioritizer
	ing := ingest.New(icfg, st, an, sch, clk, log)
	ing.Frames = ingest.NewFrameLog(300)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() { _ = sch.Run(ctx) }()
	queue := ingest.NewQueue(*liveQueue)
	go func() { _ = ing.Run(ctx, queue) }()
	if *watchDir != "" {
		go func() {
			if err := ingest.Watch(ctx, queue, *watchDir, ingest.Metadata{Source: *liveSource}, *liveBytes, *watchPoll); err != nil && ctx.Err() == nil {
				log.Error("spool stopped", "err", err)
			}
		}()
	}
	if *mjpegURL != "" {
		go func() {
			if err := ingest.PullMJPEG(ctx, queue, *mjpegURL, ingest.Metadata{Source: *liveSource}, *liveBytes, *mjpegFPS); err != nil && ctx.Err() == nil {
				log.Error("MJPEG stopped", "err", err)
			}
		}()
	}

	startReplay(ctx, replay, ing, queue, clk, log)

	svc := &console.Service{NodeID: *id, St: st, Sch: sch, Clk: clk, Deduped: ing.Deduped, Prioritizer: *prioritizer, Frames: ing.Frames}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/ingest/frames", queue.Handler(operator, token, *liveBytes))
	mux.Handle("GET /api/v1/health", opauth.RequireBearer(operator, http.HandlerFunc(queue.Health)))
	path, h := ghostseekerv1connect.NewConsoleServiceHandler(svc, connect.WithReadMaxBytes(1<<20))
	mux.Handle(path, opauth.RequireBearer(operator, h))
	mux.Handle("/blob/", opauth.RequireBearer(operator, svc.BlobHandler()))
	mux.Handle("GET /policy/audit", opauth.RequireBearer(operator, svc.PolicyAuditHandler()))
	mux.Handle("/ship/policy", opauth.RequireBearer(operator, svc.ShipPolicyHandler()))
	mux.Handle("GET /ship/items", opauth.RequireBearer(operator, svc.ShipItemsHandler()))
	mux.Handle("GET /ship/frames", opauth.RequireBearer(operator, svc.ShipFramesHandler()))
	mux.Handle("GET /ship/frames/{seq}", opauth.RequireBearer(operator, svc.ShipFramesHandler()))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	httpx.StaticUI(mux, *uiDir, "edge "+*id)
	srv := newHTTPServer(*listen, httpx.CORSAllow(httpx.SplitList(*corsOrigins), httpx.NewHostAllowlist(*listen, httpx.SplitList(*publicHosts)), mux))
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("edge console", "listen", *listen, "shore", *shoreURL, "infer", *inferURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	srv := httpx.Server(addr, handler)
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetHTTP2(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	return srv
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string) bool {
	switch strings.ToLower(os.Getenv(k)) {
	case "1", "true":
		return true
	default:
		return false
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envInt64(k string) int64 {
	v, err := strconv.ParseInt(env(k, "0"), 10, 64)
	fatal(err)
	return v
}
