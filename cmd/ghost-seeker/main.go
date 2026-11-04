package main

import (
	"flag"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	retain := flag.Int("retain", 260, "maximum stored items")
	backfill := flag.Int("backfill", 84, "number of historical items to generate at startup")
	flag.Parse()

	data := store.New(*retain)
	generator := sensor.NewGenerator()
	fillBacklog(data, generator, *backfill)
	app := server.New(data)
	go runSimulation(data, generator, app)
	go refreshTimeScopes(app)

	log.Printf("Ghost Seeker listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, app))
}

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
		if item != nil {
			data.Add(item, extra)
			kept++
		} else {
			data.Add(nil, extra)
		}
	}
}

func runSimulation(data *store.Store, generator *sensor.Generator, app *server.Server) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		time.Sleep(time.Duration(1200+rng.Intn(1201)) * time.Millisecond)
		_, _, _, state := data.Snapshot()
		item, extra := generator.Tick(nil, time.Now(), state.Model, state.Directives)
		stored, evicted := data.Add(item, extra)
		app.PublishEvict(evicted)
		if item != nil {
			stats, _ := data.Stats()
			app.PublishItem(stored, stats)
		} else {
			stats, _ := data.Stats()
			app.PublishStats(stats)
		}
	}
}
