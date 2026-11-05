package pivauth

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
	"golang.org/x/crypto/ocsp"
)

type fixture struct {
	cfg               Config
	root, inter, leaf *pivtest.Certificate
	now               time.Time
}

func setup(t *testing.T, o pivtest.Options) *fixture {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	root, inter, leaf := pivtest.Chain(t, o)
	dir := t.TempDir()
	crls := filepath.Join(dir, "crls")
	if err := os.Mkdir(crls, 0700); err != nil {
		t.Fatal(err)
	}
	f := &fixture{Config{CABundle: filepath.Join(dir, "ca.pem"), CRLDir: crls, UsersFile: filepath.Join(dir, "users.yaml"), Now: func() time.Time { return now }}, root, inter, leaf, now}
	pivtest.WritePEM(t, f.cfg.CABundle, "CERTIFICATE", root.Cert.Raw, inter.Cert.Raw)
	f.crl(t, "root", root, nil, now.Add(time.Hour))
	f.crl(t, "inter", inter, nil, now.Add(time.Hour))
	f.users(t, "operator")
	return f
}
func (f *fixture) crl(t *testing.T, name string, issuer *pivtest.Certificate, serials []*big.Int, next time.Time) {
	t.Helper()
	pivtest.WriteDER(t, filepath.Join(f.cfg.CRLDir, name), pivtest.CRL(t, issuer, serials, f.now.Add(-time.Minute), next))
}
func (f *fixture) users(t *testing.T, role string) {
	t.Helper()
	pivtest.WriteDER(t, f.cfg.UsersFile, []byte("\""+identity(f.leaf.Cert).ID+"\": "+role+"\n"))
}
func normal() pivtest.Options {
	return pivtest.Options{CN: "DOE.JANE.Q.1234567890", UPN: "1234567890@mil", KeyUsage: x509.KeyUsageDigitalSignature, EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
}
func (f *fixture) auth(t *testing.T) *Authenticator {
	t.Helper()
	a, err := New(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func (f *fixture) state() *tls.ConnectionState {
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{f.leaf.Cert, f.inter.Cert}}
}
func wantKind(t *testing.T, err error, k Kind) {
	t.Helper()
	if k == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var r *Rejection
	if !errors.As(err, &r) || r.Kind != k {
		t.Fatalf("want %s, got %v", k, err)
	}
}
func TestAuthentication(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*testing.T, *fixture)
		options func(*pivtest.Options)
		kind    Kind
		role    string
	}{
		{name: "operator", role: "operator"},
		{name: "viewer", change: func(t *testing.T, f *fixture) { f.users(t, "viewer") }, role: "viewer"},
		{name: "not authorized", change: func(t *testing.T, f *fixture) { pivtest.WriteDER(t, f.cfg.UsersFile, []byte("{}")) }, kind: NotAuthorized},
		{name: "revoked leaf", change: func(t *testing.T, f *fixture) {
			f.crl(t, "inter", f.inter, []*big.Int{f.leaf.Cert.SerialNumber}, f.now.Add(time.Hour))
		}, kind: Revoked},
		{name: "revoked intermediate", change: func(t *testing.T, f *fixture) {
			f.crl(t, "root", f.root, []*big.Int{f.inter.Cert.SerialNumber}, f.now.Add(time.Hour))
		}, kind: Revoked},
		{name: "expired", options: func(o *pivtest.Options) {
			o.NotBefore = time.Now().Add(-2 * time.Hour)
			o.NotAfter = time.Now().Add(-time.Hour)
		}, kind: Expired},
		{name: "wrong EKU", options: func(o *pivtest.Options) { o.EKU = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} }, kind: KeyUsage},
		{name: "any EKU", options: func(o *pivtest.Options) { o.EKU = []x509.ExtKeyUsage{x509.ExtKeyUsageAny} }, kind: KeyUsage},
		{name: "no digital signature", options: func(o *pivtest.Options) { o.KeyUsage = x509.KeyUsageKeyEncipherment }, kind: KeyUsage},
		{name: "smartcard only", options: func(o *pivtest.Options) {
			o.EKU = nil
			o.UnknownEKU = []asn1.ObjectIdentifier{{1, 3, 6, 1, 4, 1, 311, 20, 2, 2}}
		}, role: "operator"},
		{name: "untrusted", change: func(t *testing.T, f *fixture) { _, f.inter, f.leaf = pivtest.Chain(t, normal()) }, kind: Untrusted},
		{name: "missing CRL", change: func(t *testing.T, f *fixture) {
			if err := os.Remove(filepath.Join(f.cfg.CRLDir, "inter")); err != nil {
				t.Fatal(err)
			}
		}, kind: CRLMissing},
		{name: "stale CRL", change: func(t *testing.T, f *fixture) { f.crl(t, "inter", f.inter, nil, f.now.Add(-time.Second)) }, kind: CRLStale},
		{name: "grace", change: func(t *testing.T, f *fixture) {
			f.crl(t, "inter", f.inter, nil, f.now.Add(-time.Second))
			f.cfg.CRLGrace = time.Minute
		}, role: "operator"},
		{name: "bad CRL signature", change: func(t *testing.T, f *fixture) {
			fake := pivtest.New(t, nil, pivtest.Options{CN: f.inter.Cert.Subject.CommonName, CA: true})
			f.crl(t, "inter", fake, nil, f.now.Add(time.Hour))
		}, kind: CRLMissing},
		{name: "policy accepted", options: func(o *pivtest.Options) { o.Policies = []asn1.ObjectIdentifier{{1, 2, 3, 4}} }, change: func(t *testing.T, f *fixture) { f.cfg.PolicyOIDs = []string{"1.2.3.4"} }, role: "operator"},
		{name: "policy rejected", options: func(o *pivtest.Options) { o.Policies = []asn1.ObjectIdentifier{{1, 2, 3, 4}} }, change: func(t *testing.T, f *fixture) { f.cfg.PolicyOIDs = []string{"1.2.3.5"} }, kind: Policy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := normal()
			if tc.options != nil {
				tc.options(&o)
			}
			f := setup(t, o)
			if tc.change != nil {
				tc.change(t, f)
			}
			p, err := f.auth(t).Authenticate(f.state())
			wantKind(t, err, tc.kind)
			if err == nil && (p.Role != tc.role || p.Fingerprint == "" || p.Name != "Jane Q. Doe") {
				t.Fatalf("unexpected principal: %+v", p)
			}
		})
	}
}
func TestIdentity(t *testing.T) {
	for _, tc := range []struct{ name, upn, cn, id string }{{"UPN", "test@mil", "DOE.JANE.1234567890", "test@mil"}, {"CN", "", "DOE.JANE.1234567890", "1234567890"}, {"fingerprint", "", "Synthetic Person", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			c := pivtest.New(t, nil, pivtest.Options{CN: tc.cn, UPN: tc.upn})
			p := identity(c.Cert)
			want := tc.id
			if want == "" {
				want = "sha256:" + p.Fingerprint
			}
			if p.ID != want {
				t.Fatalf("%s != %s", p.ID, want)
			}
		})
	}
}
func TestReload(t *testing.T) {
	f := setup(t, normal())
	a := f.auth(t)
	f.users(t, "viewer")
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	p, err := a.Authenticate(f.state())
	wantKind(t, err, "")
	if p.Role != "viewer" {
		t.Fatal(p)
	}
	f.users(t, "admin")
	if err := a.Reload(); err == nil {
		t.Fatal("invalid role accepted")
	}
	p, err = a.Authenticate(f.state())
	wantKind(t, err, "")
	if p.Role != "viewer" {
		t.Fatal("lost previous users")
	}
	f.users(t, "operator")
	f.crl(t, "inter", f.inter, []*big.Int{f.leaf.Cert.SerialNumber}, f.now.Add(time.Hour))
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	_, err = a.Authenticate(f.state())
	wantKind(t, err, Revoked)
}
func TestTLSHandshake(t *testing.T) {
	f := setup(t, normal())
	a := f.auth(t)
	server := pivtest.Server(t, f.root)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert"), filepath.Join(dir, "key")
	pivtest.WritePEM(t, certFile, "CERTIFICATE", server.Cert.Raw)
	pivtest.WriteKey(t, keyFile, server)
	cfg, err := a.TLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	s.TLS = cfg
	s.StartTLS()
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(f.root.Cert)
	for _, trusted := range []bool{true, false} {
		leaf, inter := f.leaf, f.inter
		if !trusted {
			_, inter, leaf = pivtest.Chain(t, normal())
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.Cert.Raw, inter.Cert.Raw}, PrivateKey: leaf.Key}}}}, Timeout: time.Second}
		resp, err := client.Get(s.URL)
		client.CloseIdleConnections()
		if trusted && err != nil {
			t.Fatal(err)
		}
		if !trusted && err == nil {
			t.Error("untrusted certificate connected")
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
}
func TestOCSPRevoked(t *testing.T) {
	f := setup(t, normal())
	response, err := ocsp.CreateResponse(f.inter.Cert, f.inter.Cert, ocsp.Response{Status: ocsp.Revoked, SerialNumber: f.leaf.Cert.SerialNumber, ThisUpdate: f.now.Add(-time.Minute), NextUpdate: f.now.Add(time.Hour), RevokedAt: f.now.Add(-time.Minute)}, f.inter.Key)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Error(r.Method)
		}
		w.Header().Set("Content-Type", "application/ocsp-response")
		if _, err := w.Write(response); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	f.cfg.OCSPURL = s.URL
	_, err = f.auth(t).Authenticate(f.state())
	wantKind(t, err, Revoked)
}
func TestInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	f := setup(t, normal())
	pivtest.WritePEM(t, f.cfg.CABundle, "CERTIFICATE", f.inter.Cert.Raw)
	if _, err := New(f.cfg); err == nil {
		t.Fatal("rootless bundle accepted")
	}
	_, err := (&Authenticator{}).Authenticate(nil)
	wantKind(t, err, Untrusted)
}
