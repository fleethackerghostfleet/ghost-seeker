package pivauth

import (
	"crypto/x509"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
	"golang.org/x/crypto/ocsp"
)

func TestOCSPResponderAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name     string
		validCRL bool
		want     Kind
	}{
		{"direct issuer", false, ""},
		{"embedded issuer", false, ""},
		{"delegated", false, ""},
		{"client-auth only", false, Revoked},
		{"expired", false, Revoked},
		{"future", false, Revoked},
		{"foreign issuer", false, Revoked},
		{"bad signature", false, Revoked},
		{"client-auth only", true, ""},
	} {
		t.Run(tc.name+map[bool]string{true: " valid CRL", false: " revoked CRL"}[tc.validCRL], func(t *testing.T) {
			f := setup(t, normal())
			if !tc.validCRL {
				f.crl(t, "inter", f.inter, []*big.Int{f.leaf.Cert.SerialNumber}, f.now.Add(time.Hour))
			}
			o := pivtest.Options{CN: "Synthetic OCSP Responder", KeyUsage: x509.KeyUsageDigitalSignature, EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning}}
			switch tc.name {
			case "client-auth only":
				o.EKU = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			case "expired":
				o.NotBefore = f.now.Add(-2 * time.Hour)
				o.NotAfter = f.now.Add(-time.Hour)
			case "future":
				o.NotBefore = f.now.Add(time.Hour)
				o.NotAfter = f.now.Add(2 * time.Hour)
			}
			parent := f.inter
			if tc.name == "foreign issuer" {
				parent = pivtest.New(t, nil, pivtest.Options{CN: "Foreign CA", CA: true})
			}
			responder := pivtest.New(t, parent, o)
			if tc.name == "direct issuer" || tc.name == "embedded issuer" {
				responder = f.inter
			}
			template := ocsp.Response{Certificate: responder.Cert, Status: ocsp.Good, SerialNumber: f.leaf.Cert.SerialNumber, ThisUpdate: f.now.Add(-time.Minute), NextUpdate: f.now.Add(time.Hour)}
			if tc.name == "direct issuer" {
				template.Certificate = nil
			}
			key := responder.Key
			if tc.name == "bad signature" {
				key = f.leaf.Key
			}
			response, err := ocsp.CreateResponse(f.inter.Cert, responder.Cert, template, key)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(response) }))
			defer server.Close()
			f.cfg.OCSPURL = server.URL
			_, err = f.auth(t).Authenticate(f.state())
			wantKind(t, err, tc.want)
		})
	}
}
