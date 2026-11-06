//go:build prod

package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

// Production builds have no built-in analyzer stand-in and no scenario/frame
// replay: frames come from the live inputs in main.go (POST
// /api/v1/ingest/frames, -watch-dir, -mjpeg-url) and are scored by the
// inference sidecar.

const (
	defaultInferURL = ""
	inferUsage      = "inference sidecar URL (required)"
)

type replayFlags struct{}

func registerReplayFlags() *replayFlags { return &replayFlags{} }

func newAnalyzer(url string) (inference.Analyzer, error) {
	if url == "" || url == "mock" {
		return nil, errors.New("edge: production build (-tags prod) needs -infer / INFER_URL set to the inference sidecar URL")
	}
	return inference.NewRemote(url), nil
}

func startReplay(context.Context, *replayFlags, *ingest.Ingester, *ingest.Queue, clock.Clock, *slog.Logger) {
}
