package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsReq(h http.Handler, method, origin, site string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://shore.local:8080/ghostseeker.v1.ConsoleService/SetPolicy", nil)
	return corsReqWithRequest(h, r, origin, site)
}

func corsReqWithRequest(h http.Handler, r *http.Request, origin, site string) *httptest.ResponseRecorder {
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
	h := CORSAllow([]string{"http://localhost:8090"}, NewHostAllowlist("", []string{"shore.local"}), ok)

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
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/x", nil)
	if w := corsReqWithRequest(CORS(ok), r, "http://localhost:8090", ""); w.Code != http.StatusForbidden {
		t.Fatalf("default CORS must not allow other origins: %d", w.Code)
	}
	if got := SplitList("a, b c,,"); len(got) != 3 {
		t.Fatalf("SplitList: %v", got)
	}
}

func TestCORSAllowRejectsRebindingHost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := CORSAllow(nil, NewHostAllowlist(":8080", nil), ok)

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodOptions} {
		r := httptest.NewRequest(method, "/ghostseeker.v1.SyncService/Exchange", nil)
		r.Host = "rebind.example:8080"
		r.Header.Set("Origin", "http://rebind.example:8080")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s rebinding host: %d %v", method, w.Code, w.Header())
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/ghostseeker.v1.SyncService/Exchange", nil)
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8080" {
		t.Fatalf("localhost same-origin: %d %v", w.Code, w.Header())
	}

	for _, host := range []string{"127.0.0.1:8080", "[::1]:8080", "10.1.2.3:8080"} {
		r := httptest.NewRequest(http.MethodPost, "/ghostseeker.v1.SyncService/Exchange", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("IP host %q: %d %v", host, w.Code, w.Header())
		}
	}

	r = httptest.NewRequest(http.MethodPost, "/ghostseeker.v1.SyncService/Exchange", nil)
	r.Host = "shore:8080"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("shore host should be rejected by default: %d", w.Code)
	}

	shore := CORSAllow(nil, NewHostAllowlist(":8080", []string{"shore"}), ok)
	w = httptest.NewRecorder()
	shore.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("extra shore host: %d %v", w.Code, w.Header())
	}

	normalized := CORSAllow(nil, NewHostAllowlist(":8080", []string{"SHORE:9999"}), ok)
	r.Host = "shore.:8080"
	w = httptest.NewRecorder()
	normalized.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("normalized host: %d %v", w.Code, w.Header())
	}

	listenHost := CORSAllow(nil, NewHostAllowlist("shore.example:8080", nil), ok)
	r.Host = "shore.example"
	w = httptest.NewRecorder()
	listenHost.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("listen host: %d %v", w.Code, w.Header())
	}

	cors := CORSAllow([]string{"http://cors-client.example:8090"}, NewHostAllowlist(":8080", nil), ok)
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://cors-client.example:8090")
	w = httptest.NewRecorder()
	cors.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "http://cors-client.example:8090" {
		t.Fatalf("allowlisted CORS origin: %d %v", w.Code, w.Header())
	}

	r.Header.Set("Origin", "http://rebind.example:8080")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	cors.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unallowlisted Origin on allowed Host: %d %v", w.Code, w.Header())
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
