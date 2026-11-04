// shore is the receiving node: SyncService for edges, ConsoleService for
// analysts, and the downlink queue that carries operator intent to the ship.
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

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/console"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

func main() {
	data := flag.String("data", env("DATA_DIR", "data/shore"), "data directory")
	listen := flag.String("listen", env("LISTEN", ":8080"), "listen address")
	inferURL := flag.String("infer", env("INFER_URL", "mock"), "inference URL for text embedding, or 'mock'")
	uiDir := flag.String("ui", env("UI_DIR", "ui"), "static UI directory")
	edgeID := flag.String("edge", env("EDGE_ID", ""), "edge id to address downlink to ('' = all)")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	st, err := store.Open(*data)
	fatal(err)
	defer st.Close()
	clk := clock.Real{}
	syncSvc := ssync.New(st, clk, log)
	var emb inference.Analyzer
	if *inferURL == "mock" {
		emb = inference.NewMock()
	} else {
		emb = inference.NewRemote(*inferURL)
	}
	con := &console.Service{NodeID: "shore", St: st, Clk: clk, Embedder: emb, EdgeID: *edgeID}

	mux := http.NewServeMux()
	p, h := ghostseekerv1connect.NewSyncServiceHandler(ssync.Handler{Service: syncSvc}, link.HandlerOptions()...)
	mux.Handle(p, h)
	p, h = ghostseekerv1connect.NewConsoleServiceHandler(con)
	mux.Handle(p, h)
	mux.Handle("/blob/", con.BlobHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	httpx.StaticUI(mux, *uiDir, "shore")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: *listen, Handler: h2c.NewHandler(httpx.CORS(mux), &http2.Server{})}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("shore", "listen", *listen)
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
