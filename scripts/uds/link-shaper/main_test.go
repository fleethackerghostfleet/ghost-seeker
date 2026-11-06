//go:build !prod

package main

import (
	"net/http/httptest"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

func TestControl(t *testing.T) {
	s := shaper.New("unused", 16000000)
	h := control(s)
	for _, tc := range []struct {
		method, path string
		status       int
		bps          int64
	}{
		{"GET", "/rate", 200, 16000000},
		{"POST", "/rate?bps=32000", 200, 32000},
		{"POST", "/rate?bps=0", 200, 0},
		{"POST", "/rate", 400, 0},
		{"POST", "/rate?bps=-1", 400, 0},
		{"POST", "/rate?bps=bad", 400, 0},
		{"DELETE", "/rate", 405, 0},
		{"GET", "/missing", 404, 0},
		{"POST", "/rate?bps=16000000", 200, 16000000},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || s.Bps() != tc.bps {
			t.Fatalf("%s %s: status=%d bps=%d", tc.method, tc.path, w.Code, s.Bps())
		}
	}
}
