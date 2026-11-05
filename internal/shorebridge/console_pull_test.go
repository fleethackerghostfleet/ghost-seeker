package shorebridge

import "testing"

// A scene FULL the edge merely holds (pending, nothing acked) is pull-only, so the
// console must offer the pull instead of reporting it as queued backlog.
func TestPullStateScenePendingIsPullable(t *testing.T) {
	held := FormStatus{State: "pending", Bytes: 120000}
	if p := pullState(held, true); !p.Enabled || p.Label != "Request full image" {
		t.Fatalf("scene held FULL: got %+v, want enabled pull", p)
	}
	if p := pullState(held, false); p.Enabled {
		t.Fatalf("object pending FULL must stay queued: %+v", p)
	}
	streaming := FormStatus{State: "partial", Bytes: 120000, Received: 4096}
	if p := pullState(streaming, true); p.Enabled {
		t.Fatalf("scene FULL already streaming must not be re-pullable: %+v", p)
	}
	if p := pullState(FormStatus{State: "received", Bytes: 120000}, true); p.Enabled {
		t.Fatalf("received scene FULL must not be pullable: %+v", p)
	}
}
