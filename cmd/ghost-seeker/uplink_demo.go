//go:build !prod

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
	fixedBPS     int64
}

func newSimulationUplink(data *store.Store, fixedBPS int64, sample func() float64) *simulationUplink {
	u := &simulationUplink{data: data, sample: sample, fixedBPS: fixedBPS}
	if fixedBPS >= 0 {
		data.SetLinkBPS(fixedBPS)
		data.SetLinkFixed(true)
		if fixedBPS == 0 {
			return u
		}
		u.distribution = &shaper.NormalDistribution{
			MinBps:  fixedBPS - fixedBPS*15/100,
			MaxBps:  fixedBPS + fixedBPS*15/100,
			MeanBps: float64(fixedBPS), StdDevBps: float64(fixedBPS) * .05,
			IntervalMS: 5000,
		}
		u.untilSample = time.Duration(u.distribution.IntervalMS) * time.Millisecond
		return u
	}
	u.distribution = shaper.NewNormal("").Distribution()
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
			if u.fixedBPS > 0 {
				stats, _ := u.data.Stats()
				u.distribution.MeanBps = float64(u.fixedBPS) + .8*(float64(stats.LinkBPS)-float64(u.fixedBPS))
			}
			updates = append(updates, u.data.SetLinkBPS(u.distribution.Rate(u.sample()))...)
			u.untilSample = time.Duration(u.distribution.IntervalMS) * time.Millisecond
		}
	}
	return updates
}
