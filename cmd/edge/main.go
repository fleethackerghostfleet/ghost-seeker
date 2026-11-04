// edge is the shipboard node: ingest, store, score, schedule, and serve the
// ship-side console. Talks to shore over the Link; everything else is local.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
)

func main() {
	id := flag.String("id", env("EDGE_ID", "edge-1"), "edge id")
	data := flag.String("data", env("DATA_DIR", "data/edge"), "data directory")
	listen := flag.String("listen", env("LISTEN", ":8081"), "console listen address")
	shoreURL := flag.String("shore", env("SHORE_URL", "http://localhost:8080"), "shore base URL (put Toxiproxy here to degrade)")
	inferURL := flag.String("infer", env("INFER_URL", "mock"), "inference sidecar URL, or 'mock'")
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
	flag.IntVar(&icfg.FullQual, "full-quality", icfg.FullQual, "JPEG quality for re-encoding non-JPEG FULL captures (0 = send original bytes)")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	st, err := store.Open(*data)
	fatal(err)
	defer st.Close()
	clk := clock.Real{}
	lk := link.NewConnect(*shoreURL, 5*time.Minute, link.NewEstimator(*initBps, 300, 1e9))
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
		log.Info("replaying", "frames", src.Len(), "period", *period)
		go func() {
			if err := ing.Run(ctx, src); err != nil && ctx.Err() == nil {
				log.Error("replay", "err", err)
			}
			log.Info("replay finished", "items", ing.Count, "dups", ing.Dups)
		}()
	}

	svc := &console.Service{NodeID: *id, St: st, Sch: sch, Clk: clk}
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
