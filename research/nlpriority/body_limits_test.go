package nlpriority

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResearchJSONBodyLimits(t *testing.T) {
	h := (&Service{}).Handler("")
	padding := strings.Repeat(" ", 50<<20)
	for _, path := range []string{"/api/compile", "/api/preview", "/api/apply", "/api/undo", "/api/items"} {
		for _, tc := range []struct {
			body string
			want int
		}{{`{}` + padding, 413}, {`{} {}`, 400}, {`{`, 400}, {``, 400}} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s: status=%d want=%d: %s", path, w.Code, tc.want, w.Body.String())
			}
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/compile", strings.NewReader(`{"text":"`+strings.Repeat("x", 50<<20)+`"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized value: %d %s", w.Code, w.Body.String())
	}
}
