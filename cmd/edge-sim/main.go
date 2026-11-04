// Command edge-sim simulates one or more edge sensors uploading triaged
// frames to a remote ghost-seeker server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/edgesim"
)

func main() {
	url := flag.String("server", "http://localhost:8080", "ghost-seeker server URL")
	sources := flag.String("sources", "", "comma-separated source IDs (default: all)")
	min := flag.Duration("min", 1200*time.Millisecond, "minimum delay between captures")
	max := flag.Duration("max", 2400*time.Millisecond, "maximum delay between captures")
	backfill := flag.Int("backfill", 0, "frames to backfill over the past 45 minutes before streaming")
	token := flag.String("token", os.Getenv("GHOST_INGEST_TOKEN"), "ingest bearer token")
	flag.Parse()

	srcs := catalog.Sources
	if *sources != "" {
		srcs = nil
		for _, id := range strings.Split(*sources, ",") {
			s, ok := catalog.SourceByID(strings.TrimSpace(id))
			if !ok {
				log.Fatalf("unknown source %q", id)
			}
			srcs = append(srcs, s)
		}
	}
	if *max < *min {
		*max = *min
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if resp, err := http.Get(strings.TrimRight(*url, "/") + "/api/stats"); err != nil {
		log.Fatalf("server unreachable: %v", err)
	} else {
		var st struct{ DB int }
		json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		log.Printf("connected to %s (%d images in DB)", *url, st.DB)
	}
	edgesim.Run(ctx, &edgesim.Uploader{BaseURL: *url, Token: *token}, edgesim.Options{Sources: srcs, Interval: [2]time.Duration{*min, *max}, Backfill: *backfill})
}
