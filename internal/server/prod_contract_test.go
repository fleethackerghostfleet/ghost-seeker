//go:build prod

package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestProdContractNoDemoReset(t *testing.T) {
	app := New(store.New(4))
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		// No handler exists: the mux answers 404, or 405 where a GET-only
		// /api/ pattern matches the path.
		if got := request(app, method, "/api/demo/reset", map[string]string{"confirm": "reset"}).Code; got != http.StatusNotFound && got != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/demo/reset = %d, want no route", method, got)
		}
	}
	var config struct {
		DemoReset bool           `json:"demoReset"`
		Features  map[string]any `json:"features"`
	}
	if err := json.Unmarshal(request(app, http.MethodGet, "/api/config", nil).Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config.DemoReset || config.Features["demoReset"] != false || config.Features["profile"] != "prod" {
		t.Fatalf("prod config advertises demo features: %+v", config)
	}
}
