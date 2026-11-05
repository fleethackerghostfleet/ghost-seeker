package main

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestSimulationUplinkSlowDistribution(t *testing.T) {
	u := newSimulationUplink(store.New(1), -1, func() float64 { return 0 })
	d := u.distribution
	if d.MeanBps != 16_000 || d.MaxBps != 100_000 || d.MinBps != 0 || d.IntervalMS != 5000 {
		t.Fatalf("unexpected simulation distribution: %+v", d)
	}
	if p := math.Erfc(d.MeanBps/(d.StdDevBps*math.Sqrt2)) / 2; math.Abs(p-0.05) > 1e-12 {
		t.Fatalf("outage probability = %v, want 0.05", p)
	}
	for _, kb := range []int{18, 32, 45} {
		data := store.New(1)
		u := newSimulationUplink(data, -1, func() float64 { return 0 })
		data.Add(&command.Item{ID: "image", KB: kb}, 0)
		seconds := 0
		for ; seconds <= 30; seconds++ {
			item, _ := data.GetItem("image")
			if item.Upload.Done {
				break
			}
			u.advance(time.Second)
		}
		if seconds < 10 || seconds > 30 {
			t.Fatalf("%d KB image took %d seconds", kb, seconds)
		}
	}
}

func TestSimulationUplinkBackpressure(t *testing.T) {
	data := store.New(260)
	u := newSimulationUplink(data, -1, func() float64 { return 0 })
	maxPending, completed := 0, 0
	for tick := 0; tick < 300; tick++ {
		for _, item := range u.advance(2 * time.Second) {
			if item.Upload.Done {
				completed++
			}
		}
		items, _, _, _ := data.Snapshot()
		if u.canGenerate(items) {
			data.Add(&command.Item{ID: fmt.Sprint(tick), KB: 32}, 0)
		}
		items, _, _, _ = data.Snapshot()
		pending := 0
		for _, item := range items {
			if !item.Upload.Done {
				pending++
			}
		}
		maxPending = max(maxPending, pending)
		if pending > 6 {
			t.Fatalf("queue grew to %d", pending)
		}
	}
	if maxPending != 6 || completed < 30 {
		t.Fatalf("max pending=%d, completed=%d", maxPending, completed)
	}
	for _, rate := range []int64{0, 8000} {
		fixed := newSimulationUplink(data, rate, func() float64 { t.Fatal("fixed link sampled"); return 0 })
		items, _, _, _ := data.Snapshot()
		if !fixed.canGenerate(items) || !fixed.canGenerate(nil) {
			t.Fatalf("fixed override %d was backpressured", rate)
		}
	}
}

func TestSimulationUplinkSmallRetainBackpressure(t *testing.T) {
	for _, retain := range []int{0, 1, 2, 3, 5, 6, 7} {
		t.Run(fmt.Sprint(retain), func(t *testing.T) {
			data := store.New(retain)
			u := newSimulationUplink(data, -1, func() float64 { return 0 })
			limit := max(1, min(6, max(1, retain)-1))
			for i := 0; i < limit; i++ {
				items, _, _, _ := data.Snapshot()
				if !u.canGenerate(items) {
					t.Fatalf("blocked before pending cap: %d", i)
				}
				data.Add(&command.Item{ID: fmt.Sprint(i), KB: 32}, 0)
			}
			items, _, _, _ := data.Snapshot()
			if u.canGenerate(items) {
				t.Fatalf("admitted at pending cap %d", limit)
			}
			u.advance(20 * time.Second)
			items, _, _, _ = data.Snapshot()
			if !u.canGenerate(items) {
				t.Fatal("did not resume after upload completion")
			}
			wantEvicted := ""
			if retain <= 1 {
				completed, _ := data.GetItem("0")
				if !completed.Upload.Done {
					t.Fatal("replacement would evict an unfinished upload")
				}
				wantEvicted = "0"
			}
			if _, evicted := data.Add(&command.Item{ID: "new", KB: 32}, 0); evicted != wantEvicted {
				t.Fatalf("unexpected eviction: %s", evicted)
			}
			for i := 1; i < limit; i++ {
				if _, ok := data.GetItem(fmt.Sprint(i)); !ok {
					t.Fatalf("pending upload %d was evicted", i)
				}
			}
		})
	}
}

func TestSimulationUplinkUsesCurrentBandwidthAndOutages(t *testing.T) {
	data := store.New(1)
	samples := []float64{0, -2, 0}
	u := newSimulationUplink(data, -1, func() float64 { z := samples[0]; samples = samples[1:]; return z })
	data.Add(&command.Item{ID: "image", KB: 20000}, 0)
	u.advance(5 * time.Second)
	it, _ := data.GetItem("image")
	if it.Upload.Sent != 2_500_000 || it.Upload.BPS != 0 {
		t.Fatalf("outage: %+v", it.Upload)
	}
	u.advance(4 * time.Second)
	it, _ = data.GetItem("image")
	if it.Upload.Sent != 2_500_000 {
		t.Fatalf("uploaded during outage: %+v", it.Upload)
	}
	u.advance(3 * time.Second)
	it, _ = data.GetItem("image")
	if it.Upload.Sent != 3_500_000 || it.Upload.BPS != 4_000_000 {
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
		if _, stats, _, _ := data.Snapshot(); !stats.LinkFixed || stats.LinkBPS != rate {
			t.Fatalf("fixed rate %d: stats %+v", rate, stats)
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
	if len(updates) != 1 || updates[0].ID != "active" || updates[0].Upload.Sent != 2_500_000 {
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
		{"recovery", 4_000_000, 1},
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

// TestSimulatedBandwidthGovernsSendTime: the console's simulated uplink sends
// an image in exactly bytes ÷ current rate, and nothing during an outage.
func TestSimulatedBandwidthGovernsSendTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		z    float64
		mbps float64 // MB/s
	}{{"mean", 0, 0.5}, {"cap", 100, 2}, {"outage", -100, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			data := store.New(1)
			u := newSimulationUplink(data, -1, func() float64 { return tc.z })
			data.Add(&command.Item{ID: "image", KB: 1000}, 0) // 1,024,512 bytes on the wire
			if stats, _ := data.Stats(); stats.LinkBPS != int64(tc.mbps*8e6) {
				t.Fatalf("link rate %d", stats.LinkBPS)
			}
			var elapsed time.Duration
			for step := 100 * time.Millisecond; elapsed < time.Minute; elapsed += step {
				if it, _ := data.GetItem("image"); it.Upload.Done {
					break
				}
				u.advance(step)
			}
			it, _ := data.GetItem("image")
			if tc.mbps == 0 {
				if it.Upload.Sent != 0 || it.Upload.Active {
					t.Fatalf("sent during outage: %+v", it.Upload)
				}
				return
			}
			want := time.Duration(float64(it.Upload.Bytes) / (tc.mbps * 1e6) * float64(time.Second))
			t.Logf("%.2f MB image at %.1f MB/s: done after %v (bytes/rate = %v)", float64(it.Upload.Bytes)/1e6, tc.mbps, elapsed, want.Round(time.Millisecond))
			if !it.Upload.Done || elapsed < want || elapsed > want+100*time.Millisecond {
				t.Fatalf("done=%v after %v, want %v", it.Upload.Done, elapsed, want)
			}
		})
	}
}

func TestUploadActiveFlagTracksTheItemOnTheWire(t *testing.T) {
	data := store.New(10)
	data.SetLinkBPS(8_000) // 1000 B/s
	data.Add(&command.Item{ID: "low", KB: 1, Tier: 4}, 0)
	data.AdvanceUploads(time.Second)
	if it, _ := data.GetItem("low"); !it.Upload.Active {
		t.Fatal("moving upload is not marked active")
	}
	data.Add(&command.Item{ID: "high", KB: 1, Tier: 1}, 0)
	updates := data.AdvanceUploads(time.Second)
	low, _ := data.GetItem("low")
	high, _ := data.GetItem("high")
	if low.Upload.Active || !high.Upload.Active {
		t.Fatalf("pre-empted upload should stop being active: low=%+v high=%+v", low.Upload, high.Upload)
	}
	if len(updates) != 2 {
		t.Fatalf("want updates for the new and the paused upload, got %d", len(updates))
	}
	data.SetLinkBPS(0)
	if it, _ := data.GetItem("high"); it.Upload.Active {
		t.Fatal("outage must clear active")
	}
}
