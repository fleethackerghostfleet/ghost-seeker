//go:build !prod

package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestDemoResetBodyLimits(t *testing.T) {
	app := New(store.New(4), WithDemoReset())
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("POST", "/api/demo/reset", strings.NewReader(`{"confirm":"reset"}`+strings.Repeat(" ", 64<<10))))
	if w.Code != 413 {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
}
