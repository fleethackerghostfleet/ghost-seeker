//go:build !prod

package main

import (
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func seedReviewWhale(data *store.Store, generator *sensor.Generator, now time.Time, model string, directives []command.Directive) (command.Item, bool) {
	item := generator.WhaleFrame(now, model, directives)
	if item == nil {
		return command.Item{}, false
	}
	item.ReviewFirst = true
	stored, _ := data.Add(item, 0)
	return stored, true
}

func seedReviewAfterReset(data *store.Store, generator *sensor.Generator, epoch uint64, now time.Time, model string, directives []command.Directive) (command.Item, uint64, bool) {
	next := data.Epoch()
	if next == epoch {
		return command.Item{}, epoch, false
	}
	item, ok := seedReviewWhale(data, generator, now, model, directives)
	return item, next, ok
}
