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
	if d.MinBps != 0 || d.MaxBps != 16_000_000 || d.MeanBps != 4_000_000 || d.IntervalMS != 5000 {
		t.Fatalf("unexpected distribution: %+v", d)
	}
	if p := math.Erfc(d.MeanBps/(d.StdDevBps*math.Sqrt2)) / 2; math.Abs(p-0.05) > 1e-12 {
		t.Fatalf("outage probability = %v, want 0.05", p)
	}
	if s.Bps() < d.MinBps || s.Bps() > d.MaxBps {
		t.Fatalf("initial rate outside bounds: %d", s.Bps())
	}
	for _, tc := range []struct {
		z    float64
		want int64
	}{{-20, 0}, {-3, 0}, {0, 4_000_000}, {20, 16_000_000}} {
		if got := d.rate(tc.z); got != tc.want {
			t.Errorf("rate(%v) = %d, want %d", tc.z, got, tc.want)
		}
	}
	d.MaxBps = 1
	if s.Distribution().MaxBps != 16_000_000 {
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
	var low, center, high, outages int
	for i := 0; i < n; i++ {
		rate := d.rate(rng.NormFloat64())
		if rate < d.MinBps || rate > d.MaxBps {
			t.Fatalf("sample outside bounds: %d", rate)
		}
		if rate == 0 {
			outages++
		}
		x := float64(rate)
		sum += x
		squared += x * x
		switch {
		case rate >= 500_000 && rate < 1_500_000:
			low++
		case rate >= 3_000_000 && rate <= 5_000_000:
			center++
		case rate > 6_500_000 && rate <= 7_500_000:
			high++
		}
	}
	mean := sum / n
	stddev := math.Sqrt(squared/n - mean*mean)
	frequency := float64(outages) / n
	t.Logf("zero-rate samples: %d/%d (%.2f%%)", outages, n, frequency*100)
	if math.Abs(frequency-0.05) > 0.003 {
		t.Fatalf("observed outage frequency = %.2f%%, want approximately 5%%", frequency*100)
	}
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
	if math.Abs(mean-wantMean) > 50_000 || math.Abs(stddev-wantStddev) > 75_000 || center <= low+high {
		t.Fatalf("not the expected bell curve: mean %.0f, stddev %.0f, bins %d/%d/%d", mean, stddev, low, center, high)
	}
}

func TestNormalRateUpdatesAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewNormal("unused")
		s.SetBps(1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		zs := []float64{-4, 0, 20}
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
		for _, want := range []int64{4_000_000, 16_000_000} {
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
		if n != 3 || s.Bps() != 16_000_000 {
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

// TestShaperThroughputMatchesSimulatedBandwidth pushes two seconds' worth of
// image bytes through the real TCP shaper at the simulated mean (0.5 MB/s)
// and cap (2 MB/s) and checks the measured goodput follows the set rate.
func TestShaperThroughputMatchesSimulatedBandwidth(t *testing.T) {
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
			go func() {
				defer c.Close()
				var size [8]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				n := int64(0)
				for i := range size {
					n = n<<8 | int64(size[i])
				}
				if _, err := io.CopyN(io.Discard, c, n); err == nil {
					c.Write([]byte{1})
				}
			}()
		}
	}()
	for _, tc := range []struct {
		name string
		bps  int64
	}{{"mean 0.5 MB/s", MeanBps}, {"cap 2 MB/s", MaxBps}} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(up.Addr().String(), tc.bps)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			addr := ln.Addr().String()
			ln.Close()
			go s.ListenAndServe(ctx, addr)
			var c net.Conn
			for range 50 {
				if c, err = net.Dial("tcp", addr); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			n := tc.bps / 8 * 2 // two seconds of airtime
			header := make([]byte, 8)
			for i := range header {
				header[i] = byte(n >> (8 * (7 - i)))
			}
			start := time.Now()
			go func() { c.Write(header); c.Write(make([]byte, n)) }()
			if _, err := io.ReadFull(c, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(start)
			got := float64(n) / elapsed.Seconds() / 1e6
			want := float64(tc.bps) / 8 / 1e6
			t.Logf("sent %.1f MB in %v: measured %.3f MB/s, simulated %.3f MB/s", float64(n)/1e6, elapsed.Round(time.Millisecond), got, want)
			// The pacer releases the final ~100 ms chunk without waiting, so
			// expect ~1.9 s; anything far off means the link is not governed.
			if elapsed < 1700*time.Millisecond || elapsed > 2600*time.Millisecond {
				t.Fatalf("2 s of data at %.2f MB/s took %v", want, elapsed)
			}
		})
	}
}
