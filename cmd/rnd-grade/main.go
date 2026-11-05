package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/rnd"
)

func main() {
	testset := flag.String("testset", "", "directory with images, manifest.jsonl and predictions.jsonl")
	logs := flag.String("log-dir", "rnd/logs", "durable grading session directory")
	reviewer := flag.String("reviewer", "", "reviewer name (required)")
	addr := flag.String("addr", "127.0.0.1:8095", "HTTP listen address")
	flag.Parse()
	s, err := rnd.OpenSession(*testset, *logs, *reviewer)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	log.Printf("grader http://%s journal=%s", *addr, s.Path())
	server := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
