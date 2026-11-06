// Package lowband tracks sustained low/high link rates with hysteresis.
package lowband

import "time"

type Tracker struct {
	LowThreshold, HighThreshold int64
	Hold                        time.Duration
	lowSince, highSince         time.Time
	mode                        bool
	since                       time.Time
}

func New() *Tracker {
	return &Tracker{LowThreshold: 8_000, HighThreshold: 16_000, Hold: 2 * time.Minute}
}

// Observe records a link sample. Outage (zero) is below the low threshold.
func (t *Tracker) Observe(bps int64, now time.Time) bool {
	if t.LowThreshold == 0 {
		t.LowThreshold = 8_000
	}
	if t.HighThreshold == 0 {
		t.HighThreshold = 16_000
	}
	if t.Hold == 0 {
		t.Hold = 2 * time.Minute
	}
	if t.mode {
		if bps > t.HighThreshold {
			if t.highSince.IsZero() {
				t.highSince = now
			}
			if now.Sub(t.highSince) >= t.Hold {
				t.mode = false
				t.since = now
				t.highSince = time.Time{}
			}
		} else {
			t.highSince = time.Time{}
		}
	} else if bps < t.LowThreshold {
		if t.lowSince.IsZero() {
			t.lowSince = now
		}
		if now.Sub(t.lowSince) >= t.Hold {
			t.mode = true
			t.since = now
			t.lowSince = time.Time{}
		}
	} else {
		t.lowSince = time.Time{}
	}
	return t.mode
}

func (t *Tracker) Mode() bool       { return t.mode }
func (t *Tracker) Since() time.Time { return t.since }
