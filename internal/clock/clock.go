// Package clock abstracts time so the simulator can fast-forward days of
// link time in seconds while production code uses the wall clock.
package clock

import (
	"context"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	// Sleep blocks for d or until ctx is done.
	Sleep(ctx context.Context, d time.Duration) error
}

type Real struct{}

func (Real) Now() time.Time { return time.Now() }
func (Real) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Virtual is a manually advanced clock. Sleep advances it immediately, so a
// single-goroutine simulation runs as fast as the CPU allows while all
// timestamps stay consistent.
type Virtual struct {
	mu  sync.Mutex
	now time.Time
}

func NewVirtual(start time.Time) *Virtual { return &Virtual{now: start} }

func (v *Virtual) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.now
}

func (v *Virtual) Advance(d time.Duration) {
	v.mu.Lock()
	v.now = v.now.Add(d)
	v.mu.Unlock()
}

func (v *Virtual) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.Advance(d)
	return nil
}
