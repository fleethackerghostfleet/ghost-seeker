//go:build prod

package main

import (
	"errors"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
)

const (
	defaultInferURL = ""
	inferUsage      = "inference URL for text embedding (empty = text search disabled)"
)

// Production builds have no built-in embedding stand-in.
func newEmbedder(url string) (inference.Analyzer, error) {
	switch url {
	case "":
		return nil, nil
	case "mock":
		return nil, errors.New("shore: production build (-tags prod) has no built-in embedder; set -infer / INFER_URL to the inference sidecar or leave it empty")
	}
	return inference.NewRemote(url), nil
}
