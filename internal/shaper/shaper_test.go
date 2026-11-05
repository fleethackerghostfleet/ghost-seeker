package shaper

import (
	"context"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestNormalDistributionBounds(t *testing.T) {
	s := NewNormal("unused")
	d := s.Distribution()
	if d.MinBps != 0 || d.MaxBps != 50_000_000 || d.MeanBps != 8_000_000 || d.StdDevBps != 50_000_000.0/6 || d.IntervalMS != 5000 {
		t.Fatalf("unexpected distribution: %+v", d)
	}
	if s.Bps() < d.MinBps || s.Bps() > d.MaxBps {
		t.Fatalf("initial rate outside bounds: %d", s.Bps())
	}
	for _, tc := range []struct {
		z    float64
		want int64
	}{{-20, 0}, {-3, 0}, {0, 8_000_000}, {3, 33_000_000}, {20, 50_000_000}} {
		if got := d.rate(tc.z); got != tc.want {
			t.Errorf("rate(%v) = %d, want %d", tc.z, got, tc.want)
		}
	}
	d.MaxBps = 1
	if s.Distribution().MaxBps != 50_000_000 {
		t.Fatal("caller mutated the active distribution")
	}
	if New("unused", 64_000).Distribution() != nil {
		t.Fatal("fixed shaper must not advertise an automatic distribution")
	}
}

func TestNormalDistributionShape(t *testing.T) {
	d := NewNormal("unused").Distribution()
	rng := rand.New(rand.NewPCG(42, 7))
	const n = 100_000
	var sum, squared float64
	var low, center, high int
	for i := 0; i < n; i++ {
		rate := d.rate(rng.NormFloat64())
		if rate < d.MinBps || rate > d.MaxBps {
			t.Fatalf("sample outside bounds: %d", rate)
		}
		x := float64(rate)
		sum += x
		squared += x * x
		switch {
		case rate >= 1_000_000 && rate < 3_000_000:
			low++
		case rate >= 6_000_000 && rate <= 10_000_000:
			center++
		case rate > 13_000_000 && rate <= 15_000_000:
			high++
		}
	}
	mean := sum / n
	stddev := math.Sqrt(squared/n - mean*mean)
	// Clipping changes the observed moments, especially near the lower bound.
	cdf := func(z float64) float64 { return (1 + math.Erf(z/math.Sqrt2)) / 2 }
	pdf := func(z float64) float64 { return math.Exp(-z*z/2) / math.Sqrt(2*math.Pi) }
	lo, hi := float64(d.MinBps), float64(d.MaxBps)
	a, b := (lo-d.MeanBps)/d.StdDevBps, (hi-d.MeanBps)/d.StdDevBps
	p := cdf(b) - cdf(a)
	wantMean := lo*cdf(a) + d.MeanBps*p + d.StdDevBps*(pdf(a)-pdf(b)) + hi*(1-cdf(b))
	wantSquared := lo*lo*cdf(a) + (d.MeanBps*d.MeanBps+d.StdDevBps*d.StdDevBps)*p +
		2*d.MeanBps*d.StdDevBps*(pdf(a)-pdf(b)) + d.StdDevBps*d.StdDevBps*(a*pdf(a)-b*pdf(b)) + hi*hi*(1-cdf(b))
	wantStddev := math.Sqrt(wantSquared - wantMean*wantMean)
	if math.Abs(mean-wantMean) > 100_000 || math.Abs(stddev-wantStddev) > 150_000 || center <= low+high {
		t.Fatalf("not the expected bell curve: mean %.0f, stddev %.0f, bins %d/%d/%d", mean, stddev, low, center, high)
	}
}

func TestNormalRateUpdatesAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewNormal("unused")
		s.SetBps(1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		zs := []float64{-4, 0, 6}
		n := 0
		go s.varyRate(ctx, func() float64 {
			z := zs[n]
			n++
			return z
		})
		synctest.Wait()
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if s.Bps() != 1 {
			t.Fatal("rate changed before the five-second interval")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if s.Bps() != 0 {
			t.Fatalf("expected clipped outage, got %d", s.Bps())
		}
		for _, want := range []int64{8_000_000, 50_000_000} {
			time.Sleep(5 * time.Second)
			synctest.Wait()
			if s.Bps() != want {
				t.Fatalf("rate = %d, want %d", s.Bps(), want)
			}
		}
		cancel()
		synctest.Wait()
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if n != 3 || s.Bps() != 50_000_000 {
			t.Fatal("automatic updates continued after cancellation")
		}
	})
}

func TestShaperRateAndDown(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	s := New(up.Addr().String(), 80_000) // 10 KB/s
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ListenAndServe(ctx, addr)
	time.Sleep(50 * time.Millisecond)

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	// 5000 B is 0.5 s of airtime per direction at 10 KB/s; the echo overlaps
	// the two directions and the pacer lets the last 100 ms chunk go early,
	// so expect roughly 0.4 s (an unshaped loopback echo takes ~1 ms).
	payload := make([]byte, 5000)
	start := time.Now()
	go c.Write(payload)
	if _, err := io.ReadFull(c, make([]byte, len(payload))); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 300*time.Millisecond {
		t.Fatalf("echo took %v, expected >= ~0.4 s at 80 kbit/s", el)
	}
	// The counter for the upstream->client direction is bumped after the
	// last chunk is written, which can land just after ReadFull returns on
	// a loaded CI runner; give it a moment.
	deadline := time.Now().Add(time.Second)
	for s.Bytes() < int64(2*len(payload)) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Bytes() < int64(2*len(payload)) {
		t.Fatalf("bytes = %d", s.Bytes())
	}
	c.Close()

	// Two connections share the link: the same payload on each takes about
	// twice as long, not the same time.
	start = time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cc, err := net.Dial("tcp", addr)
			if err != nil {
				t.Error(err)
				return
			}
			defer cc.Close()
			go cc.Write(payload)
			if _, err := io.ReadFull(cc, make([]byte, len(payload))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if el := time.Since(start); el < 700*time.Millisecond {
		t.Fatalf("two connections echoed in %v; the cap must be shared", el)
	}

	s.SetBps(0)
	c2, err := net.Dial("tcp", addr)
	if err == nil {
		c2.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c2.Read(make([]byte, 1)); err == nil {
			t.Fatal("expected closed connection while link is down")
		}
		c2.Close()
	}
}
