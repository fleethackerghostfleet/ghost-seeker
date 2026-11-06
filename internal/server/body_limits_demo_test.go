//go:build !prod

package server

import (
	"encoding/json"
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
	for _, input := range []string{``, `{`, `{} {}`, `{"confirm":"wrong"}`} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("POST", "/api/demo/reset", strings.NewReader(input)))
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 400 || body.Error != "confirm must be reset" {
			t.Fatalf("input=%q status=%d: %s", input, w.Code, w.Body.String())
		}
	}
}
