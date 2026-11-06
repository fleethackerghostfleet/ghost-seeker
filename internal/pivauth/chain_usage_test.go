package pivauth

import (
	"crypto/x509"
	"encoding/asn1"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
)

func TestAuthenticationRequiresCommonChainPurpose(t *testing.T) {
	smartcard := []asn1.ObjectIdentifier{{1, 3, 6, 1, 4, 1, 311, 20, 2, 2}}
	for _, tc := range []struct {
		name          string
		issuerEKU     []x509.ExtKeyUsage
		issuerUnknown []asn1.ObjectIdentifier
		smartcardLeaf bool
		want          Kind
	}{
		{"unconstrained client", nil, nil, false, ""},
		{"server-only issuer bypass", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, nil, false, KeyUsage},
		{"client issuer", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, false, ""},
		{"smartcard-only leaf", nil, nil, true, ""},
		{"smartcard path", nil, smartcard, true, ""},
		{"mixed purposes", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, true, KeyUsage},
		{"reverse mixed purposes", nil, smartcard, false, KeyUsage},
		{"any issuer", []x509.ExtKeyUsage{x509.ExtKeyUsageAny}, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, normal())
			f.inter = pivtest.New(t, f.root, pivtest.Options{CN: "Synthetic Issuer", CA: true, EKU: tc.issuerEKU, UnknownEKU: tc.issuerUnknown})
			o := normal()
			if tc.smartcardLeaf {
				o.EKU = nil
				o.UnknownEKU = smartcard
			}
			f.leaf = pivtest.New(t, f.inter, o)
			pivtest.WritePEM(t, f.cfg.CABundle, "CERTIFICATE", f.root.Cert.Raw, f.inter.Cert.Raw)
			f.crl(t, "inter", f.inter, nil, f.now.Add(time.Hour))
			f.users(t, "operator")
			p, err := f.auth(t).Authenticate(f.state())
			wantKind(t, err, tc.want)
			if err == nil && p.Role != "operator" {
				t.Fatalf("role=%q", p.Role)
			}
		})
	}
}
