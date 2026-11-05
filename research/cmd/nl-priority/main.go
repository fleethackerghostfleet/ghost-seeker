// nl-priority runs the standalone natural-language prioritization research page.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/research/nlpriority"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8097", "research HTTP address")
	data := flag.String("data", "work/nl-priority", "isolated research state directory")
	web := flag.String("web-dir", "research/nl-priority/web", "standalone page assets")
	llm := flag.String("llm-url", "", "local IBM Granite llama.cpp URL")
	model := flag.String("llm-model", "granite-4.0-350m", "Granite model server alias")
	vision := flag.String("vision-url", "", "matching MobileCLIP research service URL")
	bandwidth := flag.Float64("bandwidth-bps", 128000, "simulated ship-to-shore bits per second")
	flag.Parse()
	service, err := nlpriority.Open(nlpriority.Config{DataDir: *data, Compiler: &nlpriority.Compiler{URL: *llm, Model: *model}, Vision: &nlpriority.Vision{URL: *vision}, BandwidthBPS: *bandwidth})
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *listen, Handler: service.Handler(*web), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("research-only prioritization demo: http://%s", *listen)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
