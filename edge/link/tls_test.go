package link

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
)

func TestConnectPrivateCA(t *testing.T) {
	dir := t.TempDir()
	root := pivtest.New(t, nil, pivtest.Options{CN: "Sync TLS CA", CA: true})
	cert := pivtest.Server(t, root)
	caFile, certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")
	pivtest.WritePEM(t, caFile, "CERTIFICATE", root.Cert.Raw)
	pivtest.WritePEM(t, certFile, "CERTIFICATE", cert.Cert.Raw)
	pivtest.WriteKey(t, keyFile, cert)
	config, err := httpx.ServerTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	path, handler := ghostseekerv1connect.NewSyncServiceHandler(echoShore{}, HandlerOptions()...)
	mux := http.NewServeMux()
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("sync protocol = %s, want HTTP/2", r.Proto)
		}
		if r.Header.Get("Authorization") != "Bearer test-unit" {
			t.Error("unit token lost")
		}
		handler.ServeHTTP(w, r)
	}))
	server := httptest.NewUnstartedServer(mux)
	server.TLS, server.EnableHTTP2 = config, true
	server.StartTLS()
	defer server.Close()
	request := &pb.ExchangeRequest{EdgeId: "edge", Manifest: []*pb.ManifestEntry{{ItemId: "tls-item"}}}
	transport, err := httpx.ClientTransport(caFile)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	client := NewConnectWithClient(server.URL, &http.Client{Transport: transport, Timeout: time.Second}, NewEstimator(64000, 300, 1e9), "test-unit")
	response, err := client.Exchange(context.Background(), request)
	if err != nil || len(response.GetAcks()) != 1 || !response.Acks[0].Manifest {
		t.Fatalf("HTTPS exchange: %v %v", response, err)
	}
	untrusted := NewConnect(server.URL, time.Second, NewEstimator(64000, 300, 1e9), "test-unit")
	if _, err := untrusted.Exchange(context.Background(), request); err == nil {
		t.Fatal("exchange accepted untrusted shore")
	}
}
