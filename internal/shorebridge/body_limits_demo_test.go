//go:build !prod

package shorebridge

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

func TestDemoLinkBodyLimits(t *testing.T) {
	b := New("", "", shaper.NewNormal("unused")).WithLinkOverride(true)
	for _, body := range []string{`{}` + strings.Repeat(" ", 4<<10), `{"bps":1}` + strings.Repeat(" ", 4<<10)} {
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/shore/link", strings.NewReader(body)))
		if w.Code != 413 {
			t.Fatalf("status=%d: %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{``, `{`, `{} {}`, `{}`} {
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/shore/link", strings.NewReader(body)))
		if w.Code != 400 || w.Body.String() != "body must be {\"bps\": N>=0} or {\"auto\": true}\n" {
			t.Fatalf("input=%q status=%d: %s", body, w.Code, w.Body.String())
		}
	}
}
