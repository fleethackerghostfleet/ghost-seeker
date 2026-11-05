package main

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestSimulationUplinkUsesCurrentBandwidthAndOutages(t *testing.T) {
	data := store.New(1)
	samples := []float64{0, -2, 0}
	u := newSimulationUplink(data, -1, func() float64 { z := samples[0]; samples = samples[1:]; return z })
	data.Add(&command.Item{ID: "image", KB: 20000}, 0)
	u.advance(5 * time.Second)
	it, _ := data.GetItem("image")
	if it.Upload.Sent != 5_000_000 || it.Upload.BPS != 0 {
		t.Fatalf("outage: %+v", it.Upload)
	}
	u.advance(4 * time.Second)
	it, _ = data.GetItem("image")
	if it.Upload.Sent != 5_000_000 {
		t.Fatalf("uploaded during outage: %+v", it.Upload)
	}
	u.advance(3 * time.Second)
	it, _ = data.GetItem("image")
	if it.Upload.Sent != 7_000_000 || it.Upload.BPS != 8_000_000 {
		t.Fatalf("recovery: %+v", it.Upload)
	}
}

func TestSimulationUplinkFixedOverride(t *testing.T) {
	for _, rate := range []int64{0, 8000} {
		data := store.New(1)
		u := newSimulationUplink(data, rate, func() float64 { t.Fatal("fixed override sampled bandwidth"); return 0 })
		data.Add(&command.Item{ID: "image", KB: 100}, 0)
		u.advance(10 * time.Second)
		it, _ := data.GetItem("image")
		if it.Upload.Sent != rate*10/8 {
			t.Fatalf("fixed rate %d: %+v", rate, it.Upload)
		}
	}
}
