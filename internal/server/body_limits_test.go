package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestRequestBodyLimits(t *testing.T) {
	app := New(store.New(4))
	for _, route := range []struct {
		path  string
		field string
	}{
		{"/api/directives/compile", "text"},
		{"/api/items/missing/full-res", "by"},
		{"/api/policy/emcon", "mode"},
	} {
		t.Run(route.path, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				body string
				want int
			}{
				{"oversized value", `{"` + route.field + `":"` + strings.Repeat("x", 3<<20) + `"}`, http.StatusRequestEntityTooLarge},
				{"oversized trailing whitespace", `{}` + strings.Repeat(" ", 64<<10), http.StatusRequestEntityTooLarge},
				{"empty", "", http.StatusBadRequest},
				{"invalid JSON", "{", http.StatusBadRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := httptest.NewRecorder()
					app.ServeHTTP(w, httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(tc.body)))
					if w.Code != tc.want {
						t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
					}
				})
			}
		})
	}
}
