// Package link abstracts the ship-to-shore transport. The scheduler only
// needs two things: an estimate of what the link can carry right now and a
// way to perform one Exchange. Real links (HTTP/Connect over whatever the
// ship has) and simulated links (traces for testing) both satisfy Link.
package link

import (
	"context"
	"math"
	"sync"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type Link interface {
	// Exchange performs one request/response with shore. An error means
	// nothing in the request may be assumed delivered.
	Exchange(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error)
	// State returns the current capacity estimate.
	State() *pb.LinkState
}

// Estimator keeps an EWMA of observed goodput and tracks failures. On
// failure it halves the estimate once and marks the link down; on recovery
// it starts from half the last good rate and ramps back up as exchanges
// succeed. Small exchanges are latency-dominated, so they may raise the
// estimate (slow-start style) but never lower it.
type Estimator struct {
	mu       sync.Mutex
	bps      float64
	lastGood float64
	floorBps float64
	ceilBps  float64
	lastOK   time.Time
	failures int64
	up       bool
	alpha    float64
	observed int64 // samples
}

func NewEstimator(initialBps, floorBps, ceilBps float64) *Estimator {
	return &Estimator{bps: initialBps, lastGood: initialBps, floorBps: floorBps, ceilBps: ceilBps, alpha: 0.5, up: true}
}

// Success records bytes moved (both directions) in elapsed time.
func (e *Estimator) Success(bytes int64, elapsed time.Duration, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failures > 0 {
		// Recovery: resume from half the last known-good rate, then ramp.
		e.bps = math.Max(e.bps, e.lastGood/2)
	}
	e.lastOK, e.failures, e.up = at, 0, true
	e.observed++
	if elapsed <= 0 || bytes <= 0 {
		return
	}
	obs := float64(bytes) * 8 / elapsed.Seconds()
	switch {
	case bytes >= 2048:
		e.bps = (1-e.alpha)*e.bps + e.alpha*obs
	case obs > e.bps:
		e.bps = math.Min(obs, e.bps*2)
	default:
		return // tiny exchange slower than estimate: inconclusive
	}
	e.bps = math.Max(e.floorBps, math.Min(e.ceilBps, e.bps))
	e.lastGood = e.bps
}

func (e *Estimator) Failure() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures++
	if e.failures == 1 {
		e.bps = math.Max(e.floorBps, e.bps/2)
	}
	if e.failures >= 2 {
		// Keep decaying while the link stays dead so the planner's exchange
		// (and its timeout, both sized from bps) eventually fits a link that
		// shrank by orders of magnitude; otherwise a 2 Mbps -> 9.6 kbps drop
		// pins the estimate at lastGood/4 and every exchange times out.
		e.up = false
		e.bps = math.Max(e.floorBps, math.Min(e.bps/2, e.lastGood/4))
	}
}

func (e *Estimator) State() *pb.LinkState {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := &pb.LinkState{Up: e.up, EstimateBps: e.bps, ConsecutiveFailures: e.failures}
	if !e.lastOK.IsZero() {
		s.LastSuccessUnixMs = e.lastOK.UnixMilli()
	}
	return s
}

func (e *Estimator) Bps() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bps
}
