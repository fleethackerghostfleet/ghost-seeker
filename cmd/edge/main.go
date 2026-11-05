// edge is the shipboard node: ingest, store, score, schedule, and serve the
// ship-side console. Talks to shore over the Link; everything else is local.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/console"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scenario"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

func main() {
	id := flag.String("id", env("EDGE_ID", "edge-1"), "edge id")
	data := flag.String("data", env("DATA_DIR", "data/edge"), "data directory")
	listen := flag.String("listen", env("LISTEN", ":8081"), "console listen address")
	shoreURL := flag.String("shore", env("SHORE_URL", "http://localhost:8080"), "shore base URL (put Toxiproxy here to degrade)")
	inferURL := flag.String("infer", env("INFER_URL", "mock"), "inference sidecar URL, or 'mock'")
	sceneEvery := flag.Duration("scene-every", 60*time.Second, "sightings mode: store at most one object-free 'scene' frame (FACT+THUMB) per source per interval; 0 = one item per frame")
	dedup := flag.String("dedup", env("DEDUP", "hash"), "naive (item per frame) | hash (exact-duplicate frames fold) | sightings (one item per object; needs a sidecar that returns sightings)")
	uiDir := flag.String("ui", env("UI_DIR", "ui"), "static UI directory")
	scen := flag.String("scenario", env("SCENARIO", ""), "scenario JSON to replay into the store (optional)")
	frames := flag.String("frames", env("FRAMES_DIR", ""), "replay the built-in scenario from this frame dir (optional)")
	period := flag.Duration("period", 5*time.Second, "capture period for replay")
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
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	token, err := unitauth.Credential(*tokenFlag, *tokenFile)
	fatal(err)

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
	sch, err := scheduler.New(cfg, st, lk, scoring.Default(), clk, log)
	fatal(err)

	var an inference.Analyzer
	if *inferURL == "mock" {
		an = inference.NewMock()
	} else {
		an = inference.NewRemote(*inferURL)
	}
	icfg.EdgeID = *id
	icfg.WaitForSpace = true
	icfg.Dedup, err = ingest.ParseDedup(*dedup)
	fatal(err)
	icfg.SceneEvery = *sceneEvery
	ing := ingest.New(icfg, st, an, sch, clk, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() { _ = sch.Run(ctx) }()

	if *scen != "" || *frames != "" {
		var sc *scenario.Scenario
		if *scen != "" {
			sc, err = scenario.Load(*scen)
		} else {
			sc = scenario.Default(*frames)
		}
		fatal(err)
		src, err := ingest.NewReplay(clk, *period, sc.Sets...)
		fatal(err)
		ing.OnBurst = func(source string, fps float64, d time.Duration, reason string) {
			src.Burst(source, fps, d)
			log.Info("replay burst", "source", source, "fps", fps, "duration", d, "reason", reason)
		}
		log.Info("replaying", "frames", src.Len(), "period", *period)
		go func() {
			if err := ing.Run(ctx, src); err != nil && ctx.Err() == nil {
				log.Error("replay", "err", err)
			}
			log.Info("replay finished", "items", ing.Count, "dups", ing.Dups)
		}()
	}

	svc := &console.Service{NodeID: *id, St: st, Sch: sch, Clk: clk, Deduped: ing.Deduped}
	mux := http.NewServeMux()
	path, h := ghostseekerv1connect.NewConsoleServiceHandler(svc)
	mux.Handle(path, h)
	mux.Handle("/blob/", svc.BlobHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	httpx.StaticUI(mux, *uiDir, "edge "+*id)
	srv := &http.Server{Addr: *listen, Handler: h2c.NewHandler(httpx.CORS(mux), &http2.Server{})}
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
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
