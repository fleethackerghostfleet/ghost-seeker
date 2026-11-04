// Command ghost-seeker runs the command-level server: it ingests triaged
// imagery from edge sensors, stores and scores it, and streams it live to the
// operator UI. By default it also runs a built-in edge simulator.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/edgesim"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"github.com/fleethackerghostfleet/ghost-seeker/web"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	data := flag.String("data", "data", "directory for the imagery database")
	sim := flag.Bool("sim", true, "run the built-in edge sensor simulator")
	backfill := flag.Int("backfill", 90, "frames the simulator backfills when the database is empty")
	token := flag.String("ingest-token", os.Getenv("GHOST_INGEST_TOKEN"), "bearer token required on /api/ingest (empty = open)")
	flag.Parse()

	st, err := store.Open(*data)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	srv := server.New(st, web.FS, *token)
	hs := &http.Server{Addr: *addr, Handler: server.LogRequests(srv.Handler()), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	log.Printf("ghost-seeker listening on %s (data: %s)", ln.Addr(), *data)

	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := st.Flush(); err != nil {
					log.Printf("flush: %v", err)
				}
			}
		}
	}()

	if *sim {
		host, port, _ := net.SplitHostPort(ln.Addr().String())
		if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
			host = "127.0.0.1"
		}
		bf := *backfill
		if st.Stats().DB > 0 {
			bf = 0
		}
		up := &edgesim.Uploader{BaseURL: "http://" + net.JoinHostPort(host, port), Token: *token,
			Client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{}}}
		go edgesim.Run(ctx, up, edgesim.Options{Sources: catalog.Sources, Interval: [2]time.Duration{1200 * time.Millisecond, 2400 * time.Millisecond}, Backfill: bf})
		log.Printf("edge simulator: %d sensors uploading to %s", len(catalog.Sources), up.BaseURL)
	}

	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hs.Shutdown(shut)
	if err := st.Flush(); err != nil {
		log.Printf("flush: %v", err)
	}
}
