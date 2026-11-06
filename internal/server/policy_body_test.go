package server

import (
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestAdminPolicyRejectsOversizeRatherThanAcceptingTruncatedPrefix(t *testing.T) {
	app := New(store.New(4), WithAdmin(true), WithAdminToken("test-token"))
	const limit = 2 << 20
	boundary := `{}` + strings.Repeat(" ", limit-2)
	if w := rawPolicyRequest(app, "POST", "/api/policy/validate", boundary); w.Code != 200 {
		t.Fatalf("exact boundary: %d %s", w.Code, w.Body.String())
	}
	for _, route := range []struct{ method, path string }{{"POST", "/api/policy/validate"}, {"PUT", "/api/policy"}} {
		for _, tc := range []struct {
			body string
			want int
		}{
			{boundary + `garbage`, 413},
			{`{"text":"` + strings.Repeat("x", limit) + `"}`, 413},
			{`{} {}`, 400}, {`{`, 400}, {``, 400},
		} {
			w := rawPolicyRequest(app, route.method, route.path, tc.body)
			if w.Code != tc.want {
				t.Fatalf("%s %s: status=%d want=%d: %s", route.method, route.path, w.Code, tc.want, w.Body.String())
			}
		}
	}
}
