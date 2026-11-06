//go:build !prod

package main

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestObserved(t *testing.T) {
	for _, tc := range []struct {
		name string
		tls  *tls.ConnectionState
		err  error
	}{
		{"tls", &tls.ConnectionState{NegotiatedProtocol: "h2"}, nil},
		{"empty TLS", nil, nil},
		{"transport error", nil, errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := observed{roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("User-Agent") != "uds-harness-sync-probe" {
					t.Fatal("missing probe user agent")
				}
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: 200, Proto: "HTTP/2.0", TLS: tc.tls,
					Body: io.NopCloser(strings.NewReader("")), Trailer: http.Header{"Grpc-Status": {"0"}}}, nil
			})}
			response, err := h.RoundTrip(httptest.NewRequest("POST", "https://shore.uds.dev", nil))
			if tc.err != nil || tc.tls == nil {
				if err == nil {
					t.Fatal("expected failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
