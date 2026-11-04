package shaper

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

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
