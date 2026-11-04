package link

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"google.golang.org/protobuf/proto"
)

// Window is one segment of a link trace.
type Window struct {
	Duration time.Duration
	Bps      float64 // 0 = down
	Loss     float64 // probability an exchange fails after consuming airtime
	RTT      time.Duration
}

// Trace is a repeating sequence of windows.
type Trace struct {
	Name    string
	Windows []Window
}

func (t Trace) total() time.Duration {
	var d time.Duration
	for _, w := range t.Windows {
		d += w.Duration
	}
	return d
}

// At returns the window active at elapsed time since trace start.
func (t Trace) At(elapsed time.Duration) Window {
	tot := t.total()
	if tot == 0 {
		return Window{}
	}
	e := elapsed % tot
	for _, w := range t.Windows {
		if e < w.Duration {
			return w
		}
		e -= w.Duration
	}
	return t.Windows[len(t.Windows)-1]
}

// Handler is what the simulated link delivers to: normally the shore
// SyncService implementation called in-process.
type Handler func(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error)

var ErrLinkDown = errors.New("link down")
var ErrLost = errors.New("exchange lost")

// Sim replays a trace over a clock. It charges airtime for request and
// response bytes, fails when the window is down, and randomly loses
// exchanges. If the request was delivered but the response is lost, shore
// state still changes; the edge must tolerate that (acks are idempotent and
// replayed on the next exchange).
type Sim struct {
	Trace   Trace
	Clock   clock.Clock
	Handler Handler
	Start   time.Time
	Rand    *rand.Rand
	est     *Estimator

	mu           sync.Mutex
	BytesOnAir   int64
	Exchanges    int64
	Failed       int64
	AirtimeTotal time.Duration
}

func NewSim(tr Trace, clk clock.Clock, h Handler, seed int64) *Sim {
	s := &Sim{Trace: tr, Clock: clk, Handler: h, Start: clk.Now(), Rand: rand.New(rand.NewSource(seed))}
	w := tr.At(0)
	init := w.Bps
	if init == 0 {
		init = 9600
	}
	s.est = NewEstimator(init, 300, 1e9)
	return s
}

// Current returns the window active now.
func (s *Sim) Current() Window { return s.Trace.At(s.Clock.Now().Sub(s.Start)) }

func (s *Sim) Exchange(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	s.mu.Lock()
	s.Exchanges++
	s.mu.Unlock()
	w := s.Current()
	start := s.Clock.Now()
	if w.Bps <= 0 {
		// Probing a dead link costs a timeout's worth of time.
		_ = s.Clock.Sleep(ctx, 2*time.Second)
		s.est.Failure()
		s.count(0, true)
		return nil, ErrLinkDown
	}
	reqBytes := int64(proto.Size(req)) + 64 // framing overhead
	air := time.Duration(float64(reqBytes*8)/w.Bps*float64(time.Second)) + w.RTT/2
	if err := s.Clock.Sleep(ctx, air); err != nil {
		return nil, err
	}
	// Window may have closed mid-transfer: the whole exchange is lost.
	if nw := s.Current(); nw.Bps <= 0 {
		s.est.Failure()
		s.count(reqBytes, true)
		return nil, fmt.Errorf("%w: window closed during uplink", ErrLost)
	}
	if w.Loss > 0 && s.Rand.Float64() < w.Loss {
		s.est.Failure()
		s.count(reqBytes, true)
		return nil, fmt.Errorf("%w: uplink", ErrLost)
	}
	resp, err := s.Handler(ctx, req)
	if err != nil {
		s.est.Failure()
		s.count(reqBytes, true)
		return nil, err
	}
	respBytes := int64(proto.Size(resp)) + 64
	air = time.Duration(float64(respBytes*8)/w.Bps*float64(time.Second)) + w.RTT/2
	if err := s.Clock.Sleep(ctx, air); err != nil {
		return nil, err
	}
	if nw := s.Current(); nw.Bps <= 0 || (w.Loss > 0 && s.Rand.Float64() < w.Loss/2) {
		// Delivered, but the response never came back.
		s.est.Failure()
		s.count(reqBytes+respBytes, true)
		return nil, fmt.Errorf("%w: downlink", ErrLost)
	}
	s.count(reqBytes+respBytes, false)
	s.est.Success(reqBytes+respBytes, s.Clock.Now().Sub(start), s.Clock.Now())
	return resp, nil
}

func (s *Sim) count(b int64, failed bool) {
	s.mu.Lock()
	s.BytesOnAir += b
	if failed {
		s.Failed++
	}
	s.mu.Unlock()
}

func (s *Sim) State() *pb.LinkState { return s.est.State() }

// Built-in traces. bps are bits per second.
var Traces = map[string]Trace{
	"steady_64k":  {Name: "steady_64k", Windows: []Window{{Duration: time.Hour, Bps: 64_000, RTT: 600 * time.Millisecond}}},
	"trickle_2k4": {Name: "trickle_2k4", Windows: []Window{{Duration: time.Hour, Bps: 2_400, RTT: 1500 * time.Millisecond}}},
	// Starlink-class link that drops out during turns / blockage.
	"starlink_turns": {Name: "starlink_turns", Windows: []Window{
		{Duration: 4 * time.Minute, Bps: 20_000_000, RTT: 60 * time.Millisecond},
		{Duration: 90 * time.Second, Bps: 0},
		{Duration: 2 * time.Minute, Bps: 500_000, RTT: 120 * time.Millisecond, Loss: 0.1},
		{Duration: 3 * time.Minute, Bps: 20_000_000, RTT: 60 * time.Millisecond},
		{Duration: 5 * time.Minute, Bps: 0},
	}},
	// Narrowband SATCOM with outages and loss.
	"satcom_contested": {Name: "satcom_contested", Windows: []Window{
		{Duration: 3 * time.Minute, Bps: 64_000, RTT: 700 * time.Millisecond, Loss: 0.05},
		{Duration: 4 * time.Minute, Bps: 0},
		{Duration: 2 * time.Minute, Bps: 9_600, RTT: 900 * time.Millisecond, Loss: 0.15},
		{Duration: 6 * time.Minute, Bps: 0},
		{Duration: 5 * time.Minute, Bps: 128_000, RTT: 600 * time.Millisecond, Loss: 0.02},
	}},
	// Short line-of-sight contact windows.
	"los_bursty": {Name: "los_bursty", Windows: []Window{
		{Duration: 45 * time.Second, Bps: 2_000_000, RTT: 30 * time.Millisecond},
		{Duration: 10 * time.Minute, Bps: 0},
	}},
}
