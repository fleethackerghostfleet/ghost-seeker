package shorebridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
)

func TestPrivateCAConsoleAndProxies(t *testing.T) {
	dir := t.TempDir()
	root := pivtest.New(t, nil, pivtest.Options{CN: "Bridge TLS CA", CA: true})
	cert := pivtest.Server(t, root)
	caFile, certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")
	pivtest.WritePEM(t, caFile, "CERTIFICATE", root.Cert.Raw)
	pivtest.WritePEM(t, certFile, "CERTIFICATE", cert.Cert.Raw)
	pivtest.WriteKey(t, keyFile, cert)
	config, err := httpx.ServerTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	newServer := func() *httptest.Server {
		s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-operator" || r.Header.Get("Cookie") != "" {
				t.Error("upstream credentials incorrect")
			}
			if r.URL.Path == consolePath+"GetStatus" {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{}`)
			} else {
				io.WriteString(w, "TLS upstream")
			}
		}))
		s.TLS = config.Clone()
		s.StartTLS()
		t.Cleanup(s.Close)
		return s
	}
	shore, edge := newServer(), newServer()
	transport, err := httpx.ClientTransport(caFile)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	bridge := New(shore.URL, edge.URL, nil).WithToken("test-operator").WithTransport(transport)
	for _, url := range []string{shore.URL, edge.URL} {
		if err := bridge.call(context.Background(), url, "GetStatus", &pb.GetStatusRequest{}, &pb.GetStatusResponse{}); err != nil {
			t.Fatal(err)
		}
		untrusted := New(shore.URL, edge.URL, nil).WithToken("test-operator")
		if err := untrusted.call(context.Background(), url, "GetStatus", &pb.GetStatusRequest{}, &pb.GetStatusResponse{}); err == nil {
			t.Fatal("console accepted untrusted server")
		}
	}
	for _, path := range []string{"/blob/item/full", "/api/v1/images"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Cookie", "gs_session=private")
		request.Header.Set("Authorization", "Bearer browser-secret")
		recorder := httptest.NewRecorder()
		bridge.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Body.String() != "TLS upstream" {
			t.Fatalf("proxy %s: %d %s", path, recorder.Code, recorder.Body.String())
		}
		untrusted := New(shore.URL, edge.URL, nil).WithToken("test-operator")
		recorder = httptest.NewRecorder()
		untrusted.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("untrusted proxy: %d", recorder.Code)
		}
	}
}
