package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsReq(h http.Handler, method, origin, site string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://shore.local:8080/ghostseeker.v1.ConsoleService/SetPolicy", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if site != "" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCORSAllowlist(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := CORSAllow([]string{"http://localhost:8090"}, ok)

	if w := corsReq(h, http.MethodPost, "", ""); w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("no Origin (curl): %d %v", w.Code, w.Header())
	}
	if w := corsReq(h, http.MethodPost, "http://shore.local:8080", "same-origin"); w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "http://shore.local:8080" {
		t.Fatalf("same origin: %d %v", w.Code, w.Header())
	}
	w := corsReq(h, http.MethodPost, "http://localhost:8090", "cross-site")
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8090" || w.Header().Get("Vary") != "Origin" {
		t.Fatalf("allowlisted: %d %v", w.Code, w.Header())
	}
	if w := corsReq(h, http.MethodPost, "https://evil.example", "cross-site"); w.Code != http.StatusForbidden {
		t.Fatalf("wrong Origin POST must be 403: %d", w.Code)
	}
	if w := corsReq(h, http.MethodOptions, "https://evil.example", ""); w.Code != http.StatusForbidden {
		t.Fatalf("wrong Origin preflight must be 403: %d", w.Code)
	}
	if w := corsReq(h, http.MethodGet, "https://evil.example", ""); w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("wrong Origin GET: no CORS headers expected: %d %v", w.Code, w.Header())
	}
	if w := corsReq(h, http.MethodPost, "", "cross-site"); w.Code != http.StatusForbidden {
		t.Fatalf("Sec-Fetch-Site cross-site without Origin must be 403: %d", w.Code)
	}
	if w := corsReq(h, http.MethodOptions, "http://localhost:8090", ""); w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("allowed preflight: %d %v", w.Code, w.Header())
	}
	// Same host but a different scheme is a different origin.
	if w := corsReq(h, http.MethodPost, "https://shore.local:8080", "cross-site"); w.Code != http.StatusForbidden {
		t.Fatalf("different-scheme origin must be 403: %d", w.Code)
	}
	tlsReq := httptest.NewRequest(http.MethodPost, "http://shore.local:8080/x", nil)
	tlsReq.Header.Set("Origin", "https://shore.local:8080")
	tlsReq.Header.Set("X-Forwarded-Proto", "https")
	tw := httptest.NewRecorder()
	h.ServeHTTP(tw, tlsReq)
	if tw.Code != http.StatusNoContent {
		t.Fatalf("https origin behind TLS termination: %d", tw.Code)
	}
	// Default: same-origin only, never a wildcard.
	if w := corsReq(CORS(ok), http.MethodPost, "http://localhost:8090", ""); w.Code != http.StatusForbidden {
		t.Fatalf("default CORS must not allow other origins: %d", w.Code)
	}
	if got := SplitList("a, b c,,"); len(got) != 3 {
		t.Fatalf("SplitList: %v", got)
	}
}

func TestSameOriginNullOriginFormPost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := SameOrigin(ok)
	post := func(origin, site string) int {
		r := httptest.NewRequest(http.MethodPost, "http://app.local:8090/login", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := post("null", "same-origin"); got != http.StatusNoContent {
		t.Fatalf("null origin + same-origin fetch site: %d", got)
	}
	if got := post("null", "cross-site"); got != http.StatusForbidden {
		t.Fatalf("null origin cross-site: %d", got)
	}
	if got := post("null", ""); got != http.StatusForbidden {
		t.Fatalf("null origin without Sec-Fetch-Site: %d", got)
	}
	if got := post("http://app.local:8090", "same-origin"); got != http.StatusNoContent {
		t.Fatalf("same origin: %d", got)
	}
	if got := post("https://evil.example", "same-origin"); got != http.StatusForbidden {
		t.Fatalf("wrong origin: %d", got)
	}
}
