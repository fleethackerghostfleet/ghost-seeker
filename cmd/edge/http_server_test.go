package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
)

func TestHTTPUpgradeRejectsIncompleteBody(t *testing.T) {
	for _, tc := range []struct {
		name, host, body string
		length           int
		status           int
	}{
		{"unauthorized", "localhost", "partial body", 1 << 20, http.StatusUnauthorized},
		{"empty", "localhost", "", 0, http.StatusUnauthorized},
		{"blocked host", "untrusted.invalid", "partial body", 1 << 20, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := httpx.CORSAllow(nil, httpx.NewHostAllowlist("127.0.0.1:0", nil),
				opauth.RequireBearer("test-operator-token", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("unauthorized request reached the application")
				})))
			srv := httptest.NewUnstartedServer(handler)
			srv.Config = newHTTPServer(srv.Listener.Addr().String(), handler)
			srv.Start()
			defer srv.Close()
			conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABk\r\nContent-Length: %d\r\n\r\n%s", tc.host, tc.length, tc.body); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
			if err != nil {
				t.Fatalf("no rejection after sending only %d of %d body bytes: %v", len(tc.body), tc.length, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
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
