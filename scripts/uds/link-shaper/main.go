//go:build !prod

package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

func control(s *shaper.Shaper) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rate" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost:
			bps, err := strconv.ParseInt(r.URL.Query().Get("bps"), 10, 64)
			if err != nil || bps < 0 {
				http.Error(w, "bps must be a nonnegative integer", http.StatusBadRequest)
				return
			}
			s.SetBps(bps)
			log.Printf("rate set to %d bits/s", bps)
		case http.MethodGet:
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"bps": s.Bps(), "bytes": s.Bytes()})
	})
}

func main() {
	listen := flag.String("listen", "172.19.0.1:443", "ship-facing TCP listener")
	target := flag.String("target", "127.0.0.1:9443", "shore HTTPS TCP endpoint")
	socket := flag.String("socket", "", "private Unix HTTP control socket")
	bps := flag.Int64("bps", 16000000, "initial bits/second; zero is an outage")
	flag.Parse()
	if *socket == "" || *bps < 0 {
		log.Fatal("socket is required and bps must be nonnegative")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s := shaper.New(*target, *bps)
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(*socket)
	if err := os.Chmod(*socket, 0600); err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: control(s)}
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Print(err)
			cancel()
		}
	}()
	log.Printf("TCP shaper listen=%s target=%s bps=%d control=%s", *listen, *target, *bps, *socket)
	if err := s.ListenAndServe(ctx, *listen); err != nil {
		log.Fatal(err)
	}
}
