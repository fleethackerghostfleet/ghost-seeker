package lowband

import (
	"testing"
	"time"
)

func TestTrackerHysteresis(t *testing.T) {
	n := time.Unix(0, 0)
	tr := &Tracker{LowThreshold: 8000, HighThreshold: 16000, Hold: 2 * time.Minute}
	tr.Observe(0, n)
	if tr.Observe(0, n.Add(119*time.Second)) {
		t.Fatal("entered early")
	}
	if !tr.Observe(0, n.Add(120*time.Second)) {
		t.Fatal("did not enter")
	}
	if !tr.Observe(16000, n.Add(3*time.Minute)) {
		t.Fatal("equal high threshold exited")
	}
	if !tr.Observe(16001, n.Add(4*time.Minute)) {
		t.Fatal("exited early")
	}
	if tr.Observe(16001, n.Add(6*time.Minute)) {
		t.Fatal("did not exit")
	}
}
func TestTrackerDipResets(t *testing.T) {
	n := time.Unix(0, 0)
	tr := New()
	tr.Observe(1, n)
	tr.Observe(9000, n.Add(time.Minute))
	if tr.Observe(1, n.Add(2*time.Minute+time.Second)) {
		t.Fatal("dip did not reset")
	}
}
