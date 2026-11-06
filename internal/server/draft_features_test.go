package server

import (
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/buildprofile"
)

func TestProdContractDraftDemoCapabilities(t *testing.T) {
	features := demoDraftFeatures(map[string]any{})
	for _, key := range []string{"demoDraftSend", "demoReviewFirst"} {
		value, present := features[key]
		if buildprofile.Name == "prod" {
			if present {
				t.Fatalf("production exposes %s", key)
			}
		} else if value != true {
			t.Fatalf("demo missing %s", key)
		}
	}
}
