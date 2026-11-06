package shorebridge

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestShoreProxyConnectionCannotStripCredentials(t *testing.T) {
	for _, route := range []string{"/api/v1/stream?since=7", "/blob/image.jpg?form=preview"} {
		for _, token := range []string{"synthetic-shore-token", ""} {
			for _, connection := range [][]string{nil, {"Authorization"}, {"keep-alive, aUtHoRiZaTiOn"}, {"keep-alive", "Authorization, Cookie, X-Drop"}} {
				name := route + "/" + token + "/" + strings.Join(connection, ";")
				t.Run(name, func(t *testing.T) {
					seen := make(chan *http.Request, 1)
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						seen <- r.Clone(r.Context())
						if r.Header.Get("Authorization") != "Bearer synthetic-shore-token" {
							w.WriteHeader(http.StatusUnauthorized)
						}
						io.WriteString(w, "upstream response")
					}))
					defer upstream.Close()
					frontend := httptest.NewServer(New(upstream.URL, "", nil).WithToken(token).Handler())
					defer frontend.Close()
					req, err := http.NewRequest(http.MethodGet, frontend.URL+route, nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer synthetic-caller-token")
					req.Header.Set("Cookie", "session=synthetic-browser-session")
					req.Header.Set("X-Drop", "hop-only")
					req.Header["Connection"] = connection
					req.Header.Set("Proxy-Connection", "Authorization")
					res, err := frontend.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer res.Body.Close()
					body, err := io.ReadAll(res.Body)
					if err != nil {
						t.Fatal(err)
					}
					wantAuth, wantStatus := "", http.StatusUnauthorized
					if token != "" {
						wantAuth, wantStatus = "Bearer "+token, http.StatusOK
					}
					if res.StatusCode != wantStatus || string(body) != "upstream response" {
						t.Fatalf("upstream response not preserved: status=%d body=%q", res.StatusCode, body)
					}
					got := <-seen
					if got.Header.Get("Authorization") != wantAuth {
						t.Fatal("upstream did not receive exactly the configured credential")
					}
					if got.Header.Get("Cookie") != "" || got.Header.Get("Proxy-Connection") != "" || got.Header.Get("Connection") != "" {
						t.Fatal("browser cookie or hop-by-hop headers reached upstream")
					}
					if len(connection) == 2 && got.Header.Get("X-Drop") != "" {
						t.Fatal("connection-nominated header reached upstream")
					}
					if res.Header.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
						t.Fatal("proxy response hardening was lost")
					}
				})
			}
		}
	}
}

func TestShoreProxyPreservesURLHostAndQuery(t *testing.T) {
	for _, tc := range []struct {
		name, base, path, want string
	}{
		{"api", "/shore%20base/?fixed=one&since=0&empty=", "/api/v1/items/a%2Fb?since=7&tag=one+two&tag=three%2Bfour&empty=", "/shore%20base/api/v1/items/a%2Fb?fixed=one&since=0&empty=&since=7&tag=one+two&tag=three%2Bfour&empty="},
		{"blob", "/shore%20base?fixed=one", "/blob/image%20one.jpg?form=preview&raw=%2f", "/shore%20base/blob/image%20one.jpg?fixed=one&form=preview&raw=%2f"},
		{"empty", "", "/api/v1/items", "/api/v1/items"},
		{"base-query-only", "/?fixed=one", "/blob/image.jpg", "/blob/image.jpg?fixed=one"},
		{"raw-query", "", "/api/v1/items?bad=%zz&semi=one;two&good=ok", "/api/v1/items?bad=%zz&semi=one;two&good=ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan *http.Request, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Clone(r.Context())
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Security-Policy", "default-src 'none'")
				io.WriteString(w, "payload")
			}))
			defer upstream.Close()
			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.Host = "watch-floor.example"
			r.Header.Set("Last-Event-ID", "8")
			originalURL := r.URL.String()
			w := httptest.NewRecorder()
			New(upstream.URL+tc.base, "", nil).WithToken("synthetic-shore-token").Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("proxy status=%d", w.Code)
			}
			got := <-seen
			if got.RequestURI != tc.want || got.Host != target.Host || got.Method != http.MethodGet || got.Header.Get("Last-Event-ID") != "8" {
				t.Fatalf("routing changed: URI=%q host=%q method=%q cursor=%q", got.RequestURI, got.Host, got.Method, got.Header.Get("Last-Event-ID"))
			}
			if r.URL.String() != originalURL || r.Host != "watch-floor.example" {
				t.Fatal("inbound URL or host was mutated")
			}
			if w.Body.String() != "payload" || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Security-Policy") != "default-src 'none'" {
				t.Fatal("upstream body, content type or CSP was changed")
			}
		})
	}
}

func TestShoreProxyPreservesForwardingSemantics(t *testing.T) {
	chain := http.Header{
		"X-Forwarded-For":   {"198.51.100.1", "198.51.100.2"},
		"X-Forwarded-Host":  {"public.example", "gateway.example"},
		"X-Forwarded-Proto": {"https"},
		"Forwarded":         {"for=198.51.100.1;proto=https", "for=198.51.100.2"},
	}
	for _, route := range []string{"/api/v1/items", "/blob/image.jpg"} {
		for _, tc := range []struct {
			name, remote string
			tls          bool
			headers      http.Header
			want         http.Header
		}{
			{"empty", "203.0.113.7:4321", false, nil, http.Header{"X-Forwarded-For": {"203.0.113.7"}}},
			{"tls-no-invented-proto", "203.0.113.7:4321", true, nil, http.Header{"X-Forwarded-For": {"203.0.113.7"}}},
			{"existing-chain", "203.0.113.7:4321", false, chain, http.Header{
				"X-Forwarded-For": {"198.51.100.1, 198.51.100.2, 203.0.113.7"}, "X-Forwarded-Host": chain["X-Forwarded-Host"], "X-Forwarded-Proto": chain["X-Forwarded-Proto"], "Forwarded": chain["Forwarded"],
			}},
			{"ipv6", "[2001:db8::7]:4321", false, nil, http.Header{"X-Forwarded-For": {"2001:db8::7"}}},
			{"nil-suppresses-for", "203.0.113.7:4321", false, http.Header{"X-Forwarded-For": nil}, nil},
			{"invalid-remote-keeps-chain", "not-a-host-port", false, chain, chain},
			{"connection-nominated", "203.0.113.7:4321", false, http.Header{
				"Connection":      {"x-forwarded-for, Forwarded", "X-Forwarded-Host, x-forwarded-proto"},
				"X-Forwarded-For": chain["X-Forwarded-For"], "X-Forwarded-Host": chain["X-Forwarded-Host"], "X-Forwarded-Proto": chain["X-Forwarded-Proto"], "Forwarded": chain["Forwarded"],
			}, http.Header{"X-Forwarded-For": {"203.0.113.7"}}},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				seen := make(chan *http.Request, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen <- r.Clone(r.Context())
					w.WriteHeader(http.StatusNoContent)
				}))
				defer upstream.Close()
				r := httptest.NewRequest(http.MethodGet, route, nil)
				r.Host, r.RemoteAddr = "watch-floor.example", tc.remote
				if tc.tls {
					r.TLS = &tls.ConnectionState{}
				}
				for key, values := range tc.headers {
					r.Header[key] = append([]string(nil), values...)
				}
				originalHeaders := r.Header.Clone()
				w := httptest.NewRecorder()
				New(upstream.URL, "", nil).Handler().ServeHTTP(w, r)
				if w.Code != http.StatusNoContent {
					t.Fatalf("proxy status=%d", w.Code)
				}
				got := <-seen
				for _, key := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
					if !reflect.DeepEqual(got.Header.Values(key), tc.want.Values(key)) {
						t.Errorf("%s=%q, want %q", key, got.Header.Values(key), tc.want.Values(key))
					}
				}
				if !reflect.DeepEqual(r.Header, originalHeaders) {
					t.Fatal("inbound forwarding headers were mutated")
				}
			})
		}
	}
}
