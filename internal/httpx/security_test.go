package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecureHeaders(t *testing.T) {
	handler := Secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/x" {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	for _, tc := range []struct {
		path        string
		wantNoStore bool
	}{
		{path: "/api/x", wantNoStore: true},
		{path: "/", wantNoStore: false},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := response.Header().Get("Content-Security-Policy"); got != "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'" {
			t.Errorf("CSP = %q", got)
		}
		if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := response.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q, want DENY", got)
		}
		if got := response.Header().Get("Cache-Control"); (got == "no-store") != tc.wantNoStore {
			t.Errorf("Cache-Control = %q, want no-store=%t", got, tc.wantNoStore)
		}
	}
}

func TestSecurePreservesExistingCSP(t *testing.T) {
	response := httptest.NewRecorder()
	response.Header().Set("Content-Security-Policy", "default-src 'none'")
	handler := Secure(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := response.Header().Get("Content-Security-Policy"); got != "default-src 'none'" {
		t.Fatalf("CSP = %q, want existing policy", got)
	}
}

func TestSameOrigin(t *testing.T) {
	handler := SameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		name       string
		method     string
		origin     string
		fetchSite  string
		wantStatus int
	}{
		{name: "no origin", method: http.MethodPost, wantStatus: http.StatusNoContent},
		{name: "foreign origin", method: http.MethodPost, origin: "http://evil.example", wantStatus: http.StatusForbidden},
		{name: "same origin", method: http.MethodPost, origin: "http://operator.example:8090", wantStatus: http.StatusNoContent},
		{name: "cross-site fetch", method: http.MethodPost, fetchSite: "cross-site", wantStatus: http.StatusForbidden},
		{name: "same-origin fetch", method: http.MethodPost, fetchSite: "same-origin", wantStatus: http.StatusNoContent},
		{name: "GET foreign origin", method: http.MethodGet, origin: "http://evil.example", wantStatus: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://operator.example:8090/api/directives", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
			if response.Code == http.StatusForbidden && response.Body.String() != "cross-origin request rejected\n" {
				t.Fatalf("body = %q", response.Body.String())
			}
		})
	}
}

func TestSafeBlobMime(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "text/html", want: "application/octet-stream"},
		{input: "image/png", want: "image/png"},
		{input: "text/plain", want: "text/plain; charset=utf-8"},
	} {
		if got := SafeBlobMime(tc.input); got != tc.want {
			t.Errorf("SafeBlobMime(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
