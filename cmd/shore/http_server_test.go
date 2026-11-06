package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
)

type countingReader struct {
	io.Reader
	reads int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func TestHTTPUpgradeDoesNotReadBodyBeforeGuards(t *testing.T) {
	for _, tc := range []struct {
		name, host, body string
		status           int
	}{
		{"unauthorized", "localhost", "untrusted body", http.StatusUnauthorized},
		{"empty", "localhost", "", http.StatusUnauthorized},
		{"blocked host", "untrusted.invalid", "untrusted body", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := httpx.CORSAllow(nil, httpx.NewHostAllowlist("127.0.0.1:0", nil),
				opauth.RequireBearer("test-operator-token", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("unauthorized request reached the application")
				})))
			srv := newHTTPServer("127.0.0.1:0", handler)
			body := &countingReader{Reader: strings.NewReader(tc.body)}
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/", body)
			req.Header.Set("Connection", "Upgrade, HTTP2-Settings")
			req.Header.Set("Upgrade", "h2c")
			req.Header.Set("HTTP2-Settings", "AAMAAABk")
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if body.reads != 0 {
				t.Fatalf("read untrusted body %d times before rejecting request", body.reads)
			}
		})
	}
}

func TestHTTPServerProtocols(t *testing.T) {
	handler := opauth.RequireBearer("test-operator-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Error(err)
		}
	}))
	srv := httptest.NewUnstartedServer(handler)
	srv.Config = newHTTPServer(srv.Listener.Addr().String(), handler)
	srv.Start()
	defer srv.Close()
	for name, proto := range map[string]int{"HTTP1": 1, "h2c": 2} {
		t.Run(name, func(t *testing.T) {
			protocols := new(http.Protocols)
			protocols.SetHTTP1(proto == 1)
			protocols.SetUnencryptedHTTP2(proto == 2)
			transport := &http.Transport{Protocols: protocols}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			for _, authorized := range []bool{false, true} {
				req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("sensor payload"))
				if err != nil {
					t.Fatal(err)
				}
				if authorized {
					req.Header.Set("Authorization", "Bearer test-operator-token")
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if resp.ProtoMajor != proto {
					t.Fatalf("protocol = %s, want HTTP/%d", resp.Proto, proto)
				}
				wantStatus := http.StatusUnauthorized
				if authorized {
					wantStatus = http.StatusOK
					if string(body) != "sensor payload" {
						t.Fatalf("body = %q", body)
					}
				}
				if resp.StatusCode != wantStatus {
					t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
				}
			}
		})
	}
}
