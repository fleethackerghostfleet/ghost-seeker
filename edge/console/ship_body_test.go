package console

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShipPolicyRejectsOversizedAndTrailingBodies(t *testing.T) {
	h := (&Service{}).ShipPolicyHandler()
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"oversized value", `{"by":"` + strings.Repeat("x", 64<<10) + `"}`, 413},
		{"oversized whitespace", `{}` + strings.Repeat(" ", 64<<10), 413},
		{"second object", `{"rules":[]} {}`, 400},
		{"trailing junk", `{"rules":[]} junk`, 400},
		{"empty", ``, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/ship/policy", strings.NewReader(tc.body)))
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
