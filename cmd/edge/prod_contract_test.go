//go:build prod

package main

import (
	"flag"
	"testing"
)

func TestProdContractEdgeLiveInputOnly(t *testing.T) {
	for _, url := range []string{"", "mock"} {
		if _, err := newAnalyzer(url); err == nil {
			t.Fatalf("prod edge accepted -infer %q", url)
		}
	}
	if an, err := newAnalyzer("http://edgeml-sidecar:9090"); err != nil || an == nil {
		t.Fatalf("prod edge must accept an inference sidecar: %v", err)
	}
	registerReplayFlags()
	for _, name := range []string{"scenario", "frames", "period"} {
		if flag.Lookup(name) != nil {
			t.Errorf("prod edge registers replay flag -%s", name)
		}
	}
	if defaultInferURL != "" {
		t.Fatalf("prod edge default -infer = %q", defaultInferURL)
	}
}
