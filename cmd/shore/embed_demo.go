//go:build !prod

package main

import "github.com/fleethackerghostfleet/ghost-seeker/edge/inference"

const (
	defaultInferURL = "mock"
	inferUsage      = "inference URL for text embedding, or 'mock'"
)

func newEmbedder(url string) (inference.Analyzer, error) {
	if url == "mock" {
		return inference.NewMock(), nil
	}
	return inference.NewRemote(url), nil
}
