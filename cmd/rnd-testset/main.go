package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/rnd"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	root := flag.String("repo", ".", "repository with the 10 source clips")
	out := flag.String("out", filepath.Join(home, "rnd-data/rnd-500-v1"), "new output directory (parent must exist)")
	sidecar := flag.String("infer", "http://localhost:9091", "real sidecar URL")
	rerank := flag.Bool("rerank", false, "update tiers in existing -out predictions without inference")
	flag.Parse()
	if *rerank {
		if err = rnd.Rerank(*out); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err = rnd.Build(context.Background(), *root, *out, inference.NewRemote(*sidecar)); err != nil {
		log.Fatal(err)
	}
}
