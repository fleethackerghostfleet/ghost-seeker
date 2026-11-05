package main

import (
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

type simulationUplink struct {
	data         *store.Store
	distribution *shaper.NormalDistribution
	sample       func() float64
	untilSample  time.Duration
}

func newSimulationUplink(data *store.Store, fixedBPS int64, sample func() float64) *simulationUplink {
	u := &simulationUplink{data: data, sample: sample}
	if fixedBPS >= 0 {
		data.SetLinkBPS(fixedBPS)
		return u
	}
	u.distribution = shaper.NewNormal("").Distribution()
	u.distribution.StdDevBps *= 16_000 / u.distribution.MeanBps
	u.distribution.MeanBps = 16_000
	u.distribution.MaxBps = 100_000
	u.untilSample = time.Duration(u.distribution.IntervalMS) * time.Millisecond
	data.SetLinkBPS(u.distribution.Rate(sample()))
	return u
}

func (u *simulationUplink) advance(elapsed time.Duration) []command.Item {
	if u.distribution == nil {
		return u.data.AdvanceUploads(elapsed)
	}
	var updates []command.Item
	for elapsed > 0 {
		step := min(elapsed, u.untilSample)
		updates = append(updates, u.data.AdvanceUploads(step)...)
		elapsed -= step
		u.untilSample -= step
		if u.untilSample == 0 {
			updates = append(updates, u.data.SetLinkBPS(u.distribution.Rate(u.sample()))...)
			u.untilSample = time.Duration(u.distribution.IntervalMS) * time.Millisecond
		}
	}
	return updates
}
