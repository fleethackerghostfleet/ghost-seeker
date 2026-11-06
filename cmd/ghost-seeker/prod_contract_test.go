//go:build prod

package main

import (
	"flag"
	"strings"
	"testing"
)

func TestProdContractGhostSeekerLiveOnly(t *testing.T) {
	err := simulationAvailable()
	if err == nil || !strings.Contains(err.Error(), "-shore") {
		t.Fatalf("prod build must require live mode (-shore): %v", err)
	}
	d := registerDemoFlags()
	for _, name := range []string{"backfill", "capture-every", "sim-link-bps", "lowband-hold", "auto-cdr", "dataset", "dataset-dir", "negatives-rate", "splits", "retain", "demo-link-control", "demo-accept-any-token", "dev-token-file", "dev-trust-peers", "policy-state", "admin"} {
		if flag.Lookup(name) != nil {
			t.Errorf("prod build registers demo flag -%s", name)
		}
	}
	if d.tokenFile() != "none" || d.linkControl() {
		t.Fatal("prod build must not auto-generate tokens or allow demo link control")
	}
}
