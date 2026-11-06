//go:build !prod

package main

import (
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

// whaleEvery is how many simulated captures pass between demo whale frames.
const whaleEvery = 20

// whaleCadence makes the demo show a whale at startup and right after Reset demo (pinned first in P1, like the other seeded demo items), then every whaleEvery captures.
type whaleCadence struct {
	epoch uint64
	since int
}

func newWhaleCadence(data *store.Store) *whaleCadence {
	return &whaleCadence{epoch: data.Epoch()}
}

func (w *whaleCadence) tick(data *store.Store, generator *sensor.Generator, now time.Time, model string, directives []command.Directive) (*command.Item, int) {
	if epoch := data.Epoch(); epoch != w.epoch {
		w.epoch, w.since = epoch, 0
		if item := generator.WhaleFrame(now, model, directives); item != nil {
			item.FeedFirst, item.Manual = true, 1
			return item, 0
		}
	}
	if w.since++; w.since >= whaleEvery {
		w.since = 0
		if item := generator.WhaleFrame(now, model, directives); item != nil {
			return item, 0
		}
	}
	return generator.Tick(nil, now, model, directives)
}

func seedWhale(data *store.Store, generator *sensor.Generator) {
	if item := generator.WhaleFrame(time.Now(), "maritime", nil); item != nil {
		item.FeedFirst, item.Manual = true, 1
		data.Add(item, 0)
	}
}
