//go:build !prod

package main

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestCaptureDelay(t *testing.T) {
	for _, mean := range []time.Duration{700 * time.Millisecond, 1800 * time.Millisecond} {
		t.Run(mean.String(), func(t *testing.T) {
			rng := rand.New(rand.NewSource(42))
			lower, upper := mean-mean/3, mean+mean/3
			var sum float64
			var bins [10]int
			const samples = 100000
			for range samples {
				delay := captureDelay(rng, mean)
				if delay < lower || delay > upper {
					t.Fatalf("delay %v outside [%v, %v]", delay, lower, upper)
				}
				sum += float64(delay)
				bin := min(9, int((delay-lower)*10/(upper-lower)))
				bins[bin]++
			}
			if got := sum / samples; math.Abs(got-float64(mean)) > float64(mean)*0.002 {
				t.Fatalf("mean = %v, want %v", time.Duration(got), mean)
			}
			for _, count := range bins {
				if count < 9500 || count > 10500 {
					t.Fatalf("nonuniform distribution: %v", bins)
				}
			}
		})
	}
}

func TestCaptureDelayDurationExtremes(t *testing.T) {
	for _, mean := range []time.Duration{1, 2, time.Duration(math.MaxInt64)} {
		rng := rand.New(rand.NewSource(1))
		for range 100 {
			if delay := captureDelay(rng, mean); delay <= 0 {
				t.Fatalf("mean %v produced %v", mean, delay)
			}
		}
	}
}
