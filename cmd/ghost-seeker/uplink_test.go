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

func TestSimulationUplinkResampleWithoutMovementHasNoUpdates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		add      bool
		complete bool
		hold     bool
		outage   bool
		subByte  bool
	}{
		{name: "empty"},
		{name: "completed", add: true, complete: true},
		{name: "held", add: true, hold: true},
		{name: "outage recovery", add: true, outage: true},
		{name: "sub-byte budget", add: true, subByte: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := store.New(1)
			if tc.add {
				data.Add(&command.Item{ID: "image", KB: 100}, 0)
				if tc.complete {
					data.SetLinkBPS(8_000_000)
					data.AdvanceUploads(time.Second)
				}
				if tc.hold {
					if _, _, err := data.SetUploadOverride("image", "hold", "operator"); err != nil {
						t.Fatal(err)
					}
				}
			}
			samples := []float64{0, 1}
			if tc.outage {
				samples[0] = -2
			}
			u := newSimulationUplink(data, -1, func() float64 { z := samples[0]; samples = samples[1:]; return z })
			if tc.subByte {
				u.untilSample = time.Nanosecond
			}
			if updates := u.advance(u.untilSample); len(updates) != 0 {
				t.Fatalf("resample without byte movement published %d item updates: %+v", len(updates), updates)
			}
			if len(samples) != 0 {
				t.Fatal("bandwidth was not resampled")
			}
		})
	}
}

func TestSimulationUplinkResampleUpdatesOnlyTransferringItems(t *testing.T) {
	data := store.New(10)
	data.SetLinkBPS(8_000_000)
	data.Add(&command.Item{ID: "completed", KB: 1}, 0)
	data.AdvanceUploads(time.Second)
	data.Add(&command.Item{ID: "active", KB: 20000, Manual: 1}, 0)
	data.Add(&command.Item{ID: "queued", KB: 100, Manual: 4}, 0)
	data.Add(&command.Item{ID: "held", KB: 100, Tier: 1}, 0)
	if _, _, err := data.SetUploadOverride("held", "hold", "operator"); err != nil {
		t.Fatal(err)
	}
	u := newSimulationUplink(data, -1, func() float64 { return 0 })
	updates := u.advance(5 * time.Second)
	if len(updates) != 1 || updates[0].ID != "active" || updates[0].Upload.Sent != 5_000_000 {
		t.Fatalf("expected only moving upload, got %+v", updates)
	}
}

func TestSimulationUplinkRateChangesPublishOnlyPartialUploads(t *testing.T) {
	data := store.New(10)
	data.SetLinkBPS(8_000_000)
	data.Add(&command.Item{ID: "completed", KB: 1}, 0)
	data.AdvanceUploads(time.Second)
	data.SetLinkBPS(8_000)
	data.Add(&command.Item{ID: "held", KB: 100}, 0)
	data.AdvanceUploads(time.Second)
	if _, _, err := data.SetUploadOverride("held", "hold", "operator"); err != nil {
		t.Fatal(err)
	}
	data.Add(&command.Item{ID: "partial", KB: 100, Manual: 1}, 0)
	data.AdvanceUploads(time.Second)
	data.Add(&command.Item{ID: "queued", KB: 100, Manual: 4}, 0)
	samples := []float64{0, -2, -2, 0}
	u := newSimulationUplink(data, -1, func() float64 { z := samples[0]; samples = samples[1:]; return z })
	u.untilSample = time.Nanosecond
	for _, tc := range []struct {
		name string
		rate int64
		want int
	}{
		{"outage", 0, 1},
		{"unchanged outage", 0, 0},
		{"recovery", 8_000_000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updates := u.advance(u.untilSample)
			if len(updates) != tc.want {
				t.Fatalf("got %d updates, want %d: %+v", len(updates), tc.want, updates)
			}
			for _, item := range updates {
				if item.ID != "partial" || item.Upload.Sent != 1000 || item.Upload.BPS != tc.rate {
					t.Fatalf("unexpected rate update: %+v", item)
				}
			}
		})
	}
}
