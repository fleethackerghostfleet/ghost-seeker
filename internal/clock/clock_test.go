package clock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestVirtualSleepAndCancellation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewVirtual(start)
	if err := c.Sleep(context.Background(), 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	c.Advance(500 * time.Millisecond)
	if got := c.Now(); !got.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("time = %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sleep = %v", err)
	}
	if !c.Now().Equal(start.Add(2 * time.Second)) {
		t.Fatal("canceled sleep advanced time")
	}
	if err := c.Sleep(context.Background(), 0); err != nil || !c.Now().Equal(start.Add(2*time.Second)) {
		t.Fatalf("zero sleep changed time or failed: %v", err)
	}
}

func TestVirtualConcurrentAdvance(t *testing.T) {
	start := time.Unix(0, 0)
	c := NewVirtual(start)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 100 {
				c.Advance(time.Millisecond)
				c.Now()
			}
		})
	}
	wg.Wait()
	if !c.Now().Equal(start.Add(2 * time.Second)) {
		t.Fatalf("concurrent advances lost time: %v", c.Now())
	}
}

func TestRealSleep(t *testing.T) {
	c := Real{}
	before := time.Now()
	if err := c.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if c.Now().Before(before.Add(time.Millisecond)) {
		t.Fatal("sleep returned before timer elapsed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sleep = %v", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired sleep = %v", err)
	}
}
