// Package shaper is a TCP proxy with a token-bucket bandwidth cap: the
// ship-to-shore "link" for laptop demos. The edge points its -shore at the
// shaper and the operator UI changes the rate live (0 = link down).
//
// It shapes at the byte level, like Toxiproxy's bandwidth toxic, so it is
// protocol-agnostic (gRPC/h2c, Connect, plain HTTP all pass through).
package shaper

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Preset rates in bits per second, matching the link traces and degrade.sh.
var Presets = []int64{0, 9_600, 64_000, 256_000, 2_000_000}

type Shaper struct {
	target string
	bps    atomic.Int64
	bytes  atomic.Int64 // bytes forwarded in both directions
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	ln     net.Listener
	pacers [2]pacer // 0 = ship->shore, 1 = shore->ship; shared by all connections
}

// pacer is a per-direction leaky bucket: every chunk books its airtime at
// the current rate, so concurrent connections share one link, not one each.
type pacer struct {
	mu   sync.Mutex
	next time.Time
}

// reserve books n bytes and returns how long the caller must wait before
// putting them on the wire.
func (p *pacer) reserve(n int, bps int64) time.Duration {
	air := time.Duration(float64(n*8) / float64(bps) * float64(time.Second))
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	wait := p.next.Sub(now)
	p.next = p.next.Add(air)
	return wait
}

// New creates a shaper forwarding to target (host:port) at bps bits/second.
func New(target string, bps int64) *Shaper {
	s := &Shaper{target: target, conns: map[net.Conn]struct{}{}}
	s.bps.Store(bps)
	return s
}

func (s *Shaper) Bps() int64   { return s.bps.Load() }
func (s *Shaper) Bytes() int64 { return s.bytes.Load() }

// SetBps changes the rate. Setting 0 drops every open connection and refuses
// new ones until the rate is raised again.
func (s *Shaper) SetBps(bps int64) {
	if bps < 0 {
		bps = 0
	}
	s.bps.Store(bps)
	if bps == 0 {
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
	}
}

// ListenAndServe accepts on addr until ctx is cancelled.
func (s *Shaper) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(ctx, c)
	}
}

func (s *Shaper) handle(ctx context.Context, down net.Conn) {
	if s.Bps() == 0 {
		down.Close()
		return
	}
	up, err := net.DialTimeout("tcp", s.target, 5*time.Second)
	if err != nil {
		log.Printf("shaper: dial %s: %v", s.target, err)
		down.Close()
		return
	}
	s.track(down, true)
	s.track(up, true)
	defer func() {
		s.track(down, false)
		s.track(up, false)
		down.Close()
		up.Close()
	}()
	done := make(chan struct{}, 2)
	go func() { s.pipe(ctx, up, down, &s.pacers[0]); done <- struct{}{} }()
	go func() { s.pipe(ctx, down, up, &s.pacers[1]); done <- struct{}{} }()
	<-done
}

func (s *Shaper) track(c net.Conn, add bool) {
	s.mu.Lock()
	if add {
		s.conns[c] = struct{}{}
	} else {
		delete(s.conns, c)
	}
	s.mu.Unlock()
}

// pipe copies src -> dst, booking each chunk on the direction's shared pacer
// (the trace semantics in edge/link are per-direction too). Chunks are sized
// to ~100 ms of airtime so rate changes take effect quickly.
func (s *Shaper) pipe(ctx context.Context, dst, src net.Conn, p *pacer) {
	buf := make([]byte, 64<<10)
	for {
		bps := s.Bps()
		if bps == 0 {
			return
		}
		chunk := int(bps / 8 / 10)
		if chunk < 64 {
			chunk = 64
		}
		if chunk > len(buf) {
			chunk = len(buf)
		}
		n, err := src.Read(buf[:chunk])
		if n > 0 {
			if wait := p.reserve(n, bps); wait > 0 {
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return
				}
			}
			if s.Bps() == 0 {
				return
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			s.bytes.Add(int64(n))
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			if cw, ok := dst.(interface{ CloseWrite() error }); ok {
				cw.CloseWrite()
			}
			return
		}
	}
}
