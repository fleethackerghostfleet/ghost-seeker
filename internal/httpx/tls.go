package httpx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// ServerTLS loads an optional certificate pair; neither file preserves HTTP.
func ServerTLS(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("-tls-cert and -tls-key must be set together")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("server TLS certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}, nil
}

// ListenAndServe keeps HTTP as the default and enables HTTPS when configured.
func ListenAndServe(srv *http.Server, config *tls.Config) error {
	if config == nil {
		return srv.ListenAndServe()
	}
	srv.TLSConfig = config
	return srv.ListenAndServeTLS("", "")
}

// ClientTransport adds an optional PEM CA bundle to the system trust roots.
func ClientTransport(caFile string) (*http.Transport, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS13}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read shore CA bundle: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("system trust roots: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("shore CA bundle contains no certificates")
		}
		config.RootCAs = roots
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = config
	return transport, nil
}
