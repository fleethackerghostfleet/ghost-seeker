//go:build !prod

package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scenario"
)

// Demo wiring: the mock analyzer and -scenario/-frames replay. Production
// builds (-tags prod) compile this out; see replay_prod.go.

const (
	defaultInferURL = "mock"
	inferUsage      = "inference sidecar URL, or 'mock'"
)

type replayFlags struct {
	scenario, frames *string
	period           *time.Duration
}

func registerReplayFlags() *replayFlags {
	r := &replayFlags{}
	r.scenario = flag.String("scenario", env("SCENARIO", ""), "scenario JSON to replay into the store (optional)")
	r.frames = flag.String("frames", env("FRAMES_DIR", ""), "replay the built-in scenario from this frame dir (optional)")
	r.period = flag.Duration("period", 5*time.Second, "capture period for replay")
	return r
}

func newAnalyzer(url string) (inference.Analyzer, error) {
	if url == "mock" {
		return inference.NewMock(), nil
	}
	return inference.NewRemote(url), nil
}

func startReplay(ctx context.Context, r *replayFlags, ing *ingest.Ingester, queue *ingest.Queue, clk clock.Clock, log *slog.Logger) {
	var err error
	if *r.scenario != "" || *r.frames != "" {
		var sc *scenario.Scenario
		if *r.scenario != "" {
			sc, err = scenario.Load(*r.scenario)
		} else {
			sc = scenario.Default(*r.frames)
		}
		if err != nil {
			log.Error("replay", "err", err)
			os.Exit(1)
		}
		src, err := ingest.NewReplay(clk, *r.period, sc.Sets...)
		if err != nil {
			log.Error("replay", "err", err)
			os.Exit(1)
		}
		ing.OnBurst = func(source string, fps float64, d time.Duration, reason string) {
			src.Burst(source, fps, d)
			log.Info("replay burst", "source", source, "fps", fps, "duration", d, "reason", reason)
		}
		log.Info("replaying", "frames", src.Len(), "period", *r.period)
		go func() {
			if err := ingest.Pump(ctx, queue, src, "replay"); err != nil && ctx.Err() == nil {
				log.Error("replay", "err", err)
			}
			log.Info("replay finished")
		}()
	}
}
