package httpx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
)

func tlsFiles(t *testing.T) (ca, cert, key string) {
	t.Helper()
	dir := t.TempDir()
	root := pivtest.New(t, nil, pivtest.Options{CN: "Listener test CA", CA: true})
	server := pivtest.Server(t, root)
	ca, cert, key = filepath.Join(dir, "ca.pem"), filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")
	pivtest.WritePEM(t, ca, "CERTIFICATE", root.Cert.Raw)
	pivtest.WritePEM(t, cert, "CERTIFICATE", server.Cert.Raw)
	pivtest.WriteKey(t, key, server)
	return
}

func TestServerTLSConfiguration(t *testing.T) {
	_, cert, key := tlsFiles(t)
	if config, err := ServerTLS("", ""); err != nil || config != nil {
		t.Fatalf("HTTP default changed: %v %v", config, err)
	}
	for _, pair := range [][2]string{{cert, ""}, {"", key}} {
		if _, err := ServerTLS(pair[0], pair[1]); err == nil || !strings.Contains(err.Error(), "must be set together") {
			t.Fatalf("unpaired TLS configuration: %v", err)
		}
	}
	if _, err := ServerTLS(cert, cert); err == nil {
		t.Fatal("accepted certificate as private key")
	}
	if _, err := ServerTLS("missing-cert", "missing-key"); err == nil {
		t.Fatal("accepted missing certificate files")
	}
	config, err := ServerTLS(cert, key)
	if err != nil || config.MinVersion != tls.VersionTLS13 || len(config.Certificates) != 1 {
		t.Fatalf("TLS configuration: %v %v", config, err)
	}
}

func TestClientTransportValidation(t *testing.T) {
	for _, content := range []string{"", "not a PEM certificate"} {
		path := filepath.Join(t.TempDir(), "invalid-ca.pem")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ClientTransport(path); err == nil {
			t.Fatal("accepted invalid CA bundle")
		}
	}
	if _, err := ClientTransport(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("accepted missing CA file")
	}
	transport, err := ClientTransport("")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	if transport.TLSClientConfig.RootCAs != nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("default client does not use verified system roots")
	}
}

func TestPrivateCATLS(t *testing.T) {
	ca, cert, key := tlsFiles(t)
	config, err := ServerTLS(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.Version != tls.VersionTLS13 {
			t.Error("not TLS 1.3")
		}
		io.WriteString(w, "verified HTTPS")
	}))
	server.TLS = config
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport, err := ClientTransport(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	res, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) != "verified HTTPS" || res.ProtoMajor != 2 {
		t.Fatalf("HTTPS response: %q protocol=%s err=%v", body, res.Proto, err)
	}
	for _, tc := range []struct {
		name, ca  string
		configure func(*tls.Config)
	}{
		{"system-roots-only", "", func(*tls.Config) {}},
		{"wrong-hostname", ca, func(c *tls.Config) { c.ServerName = "wrong.invalid" }},
		{"TLS-1.2", ca, func(c *tls.Config) { c.MinVersion = tls.VersionTLS12; c.MaxVersion = tls.VersionTLS12 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := ClientTransport(tc.ca)
			if err != nil {
				t.Fatal(err)
			}
			defer tr.CloseIdleConnections()
			tc.configure(tr.TLSClientConfig)
			_, err = (&http.Client{Transport: tr, Timeout: time.Second}).Get(server.URL)
			if err == nil {
				t.Fatal("untrusted/incompatible connection succeeded")
			}
			if tc.name == "system-roots-only" {
				var unknown x509.UnknownAuthorityError
				if !errors.As(err, &unknown) {
					t.Fatalf("expected unknown CA: %v", err)
				}
			}
		})
	}
}
