//go:build !prod

package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

func TestAutomaticLinkStatus(t *testing.T) {
	link := shaper.NewNormal("unused")
	bridge := New("", "", link)
	rec := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/shore/link", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var status LinkStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Controllable || status.Bps != link.Bps() || status.Distribution == nil || *status.Distribution != *link.Distribution() {
		t.Fatalf("missing applied rate or distribution: %+v", status)
	}
	if strings.Contains(rec.Body.String(), "presets") {
		t.Fatal("manual speed presets still advertised")
	}
	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/link", strings.NewReader(`{"bps":1}`)))
	if rec.Code != http.StatusMethodNotAllowed || link.Bps() != status.Bps {
		t.Fatalf("manual rate changes must be rejected: status %d, bps %d", rec.Code, link.Bps())
	}
	demo := New("", "", link).WithLinkOverride(true)
	rec = httptest.NewRecorder()
	demo.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/link", strings.NewReader(`{"bps":32000}`)))
	if rec.Code != http.StatusOK || link.Bps() != 32000 || !demo.linkStatus().Fixed {
		t.Fatalf("demo override: status %d bps %d: %s", rec.Code, link.Bps(), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	demo.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/link", strings.NewReader(`{"auto":true}`)))
	if rec.Code != http.StatusOK || demo.linkStatus().Fixed {
		t.Fatalf("demo auto: status %d: %s", rec.Code, rec.Body.String())
	}
	external := New("", "", nil).linkStatus()
	if external.Controllable || external.Distribution != nil {
		t.Fatalf("externally shaped link advertised automation: %+v", external)
	}
}
