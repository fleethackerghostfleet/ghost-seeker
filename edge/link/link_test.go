package link

import (
	"testing"
	"time"
)

func TestEstimatorRecoversAfterOutage(t *testing.T) {
	e := NewEstimator(64000, 300, 1e9)
	for i := 0; i < 20; i++ {
		e.Failure()
	}
	if e.State().Up {
		t.Fatal("should be down")
	}
	if e.Bps() < 300 {
		t.Fatal("below floor")
	}
	// Tiny heartbeat succeeds: should jump back toward last good, not stay at floor.
	e.Success(200, 500*time.Millisecond, time.Now())
	if e.Bps() < 16000 {
		t.Fatalf("estimate did not recover: %v", e.Bps())
	}
	// Big exchanges converge to observed rate.
	for i := 0; i < 10; i++ {
		e.Success(100_000, time.Second, time.Now()) // 800 kbps
	}
	if b := e.Bps(); b < 700_000 || b > 900_000 {
		t.Fatalf("did not converge: %v", b)
	}
}

func TestTraceAt(t *testing.T) {
	tr := Traces["satcom_contested"]
	if w := tr.At(0); w.Bps != 64000 {
		t.Fatal(w)
	}
	if w := tr.At(3*time.Minute + time.Second); w.Bps != 0 {
		t.Fatal(w)
	}
	if w := tr.At(20*time.Minute + 10*time.Second); w.Bps != 64000 {
		t.Fatalf("should wrap: %+v", w)
	}
}

func TestEstimatorKeepsDecayingWhileDown(t *testing.T) {
	e := NewEstimator(2_000_000, 300, 1e9)
	e.Failure()
	e.Failure()
	if b := e.Bps(); b != 250_000 {
		t.Fatalf("after two failures want min(bps/4, lastGood/4), got %v", b)
	}
	for i := 0; i < 2; i++ {
		e.Failure()
	}
	if b := e.Bps(); b > 32_000 {
		t.Fatalf("estimate should quarter per failure toward a 9.6 kbps link, got %v", b)
	}
	e.Failure()
	if b := e.Bps(); b > 10_000 {
		t.Fatalf("estimate should be under a 9.6 kbps link after 5 failures, got %v", b)
	}
	if b := e.Bps(); b < 300 {
		t.Fatal("below floor")
	}
}
