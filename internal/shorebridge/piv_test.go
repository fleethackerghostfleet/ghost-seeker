package shorebridge

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
)

func TestPIVOperatorsShareConsoleLayer(t *testing.T) {
	options := pivtest.Options{CN: "Synthetic A", UPN: "synthetic-a", EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	root, inter, a := pivtest.Chain(t, options)
	options.UPN = "synthetic-b"
	b := pivtest.New(t, inter, options)
	dir := t.TempDir()
	crls := filepath.Join(dir, "crls")
	if err := os.Mkdir(crls, 0700); err != nil {
		t.Fatal(err)
	}
	pivtest.WritePEM(t, filepath.Join(dir, "ca"), "CERTIFICATE", root.Cert.Raw, inter.Cert.Raw)
	pivtest.WriteDER(t, filepath.Join(dir, "users"), []byte("synthetic-a: operator\nsynthetic-b: operator\n"))
	now := time.Now()
	for name, ca := range map[string]*pivtest.Certificate{"root": root, "inter": inter} {
		pivtest.WriteDER(t, filepath.Join(crls, name), pivtest.CRL(t, ca, nil, now.Add(-time.Minute), now.Add(time.Hour)))
	}
	auth, err := pivauth.New(pivauth.Config{CABundle: filepath.Join(dir, "ca"), CRLDir: crls, UsersFile: filepath.Join(dir, "users")})
	if err != nil {
		t.Fatal(err)
	}
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	console := NewConsole(bridge)
	mux := http.NewServeMux()
	mux.Handle("/api/shore/", bridge.Handler())
	mux.Handle("/", console.Handler())
	handler := opauth.NewSessions("synthetic-token", false).WithCerts(auth, slog.New(slog.NewJSONHandler(io.Discard, nil))).Wrap(mux)
	request := func(cert *pivtest.Certificate, method, path, body string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert.Cert, inter.Cert}}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}
	request(a, "POST", "/api/directives", `{"text":"ignore cargo ships"}`, 201)
	stored := shore.stored()
	if len(stored.Layers) != 1 || stored.Layers[0].IssuedBy != bridge.issuer() || stored.Layers[0].Rules[0].Author != "synthetic-a" {
		t.Fatalf("bad ownership/author: %v", stored)
	}
	directives := bridge.directives(stored, now)
	if len(directives) != 1 || directives[0].Source != "" {
		t.Fatalf("not removable: %+v", directives)
	}
	request(b, "DELETE", "/api/directives/"+directives[0].ID, "", 204)
	request(a, "POST", "/api/directives", `{"text":"ignore cargo ships"}`, 201)
	request(b, "POST", "/api/shore/emcon", `{"mode":"BURST"}`, 200)
	stored = shore.stored()
	if len(stored.Layers) != 1 || stored.Layers[0].Emcon.SetBy != "synthetic-b" || stored.IssuedBy != "synthetic-b" {
		t.Fatalf("EMCON ownership: %v", stored)
	}
	request(b, "POST", "/api/directives", `{"text":"ignore cargo ships"}`, 201)
	stored = shore.stored()
	if len(stored.Layers) != 1 || len(stored.Layers[0].Rules) != 1 || len(stored.Layers[0].Rules[0].SuppressLabels) == 0 || stored.Layers[0].Rules[0].Author != "synthetic-b" {
		t.Fatalf("suppression lost: %v", stored)
	}
	request(b, "DELETE", "/api/directives/"+stored.Layers[0].Rules[0].Id, "", 204)
}
