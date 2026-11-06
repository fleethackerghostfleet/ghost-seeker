package pivtest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChainOptionsAndVerification(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	policy := asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 2, 1, 3, 13}
	opts := Options{CN: "Synthetic Operator", UPN: "test@mil", KeyUsage: x509.KeyUsageDigitalSignature,
		EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, UnknownEKU: []asn1.ObjectIdentifier{{1, 2, 3, 4}},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Policies: []asn1.ObjectIdentifier{policy}}
	root, intermediate, client := Chain(t, opts)
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root.Cert)
	intermediates.AddCert(intermediate.Cert)
	chains, err := client.Cert.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates,
		CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil || len(chains) != 1 || len(chains[0]) != 3 {
		t.Fatalf("chain=%v err=%v", chains, err)
	}
	for _, ca := range []*Certificate{root, intermediate} {
		if !ca.Cert.IsCA || ca.Cert.KeyUsage&x509.KeyUsageCertSign == 0 || ca.Cert.KeyUsage&x509.KeyUsageCRLSign == 0 {
			t.Fatalf("CA lacks signing usages: %+v", ca.Cert)
		}
		if !reflect.DeepEqual(ca.Cert.PolicyIdentifiers, opts.Policies) {
			t.Fatal("CA policy not propagated")
		}
	}
	if client.Cert.IsCA || client.Cert.Subject.CommonName != opts.CN || client.Cert.KeyUsage != opts.KeyUsage ||
		!client.Cert.NotBefore.Equal(opts.NotBefore) || !client.Cert.NotAfter.Equal(opts.NotAfter) ||
		!reflect.DeepEqual(client.Cert.ExtKeyUsage, opts.EKU) || !reflect.DeepEqual(client.Cert.UnknownExtKeyUsage, opts.UnknownEKU) ||
		!reflect.DeepEqual(client.Cert.PolicyIdentifiers, opts.Policies) {
		t.Fatal("client options not preserved")
	}
	if !client.Key.PublicKey.Equal(client.Cert.PublicKey) || root.Key.Equal(client.Key) {
		t.Fatal("client key does not match certificate or is shared with root")
	}
	var upn string
	for _, ext := range client.Cert.Extensions {
		if !ext.Id.Equal(asn1.ObjectIdentifier{2, 5, 29, 17}) {
			continue
		}
		var sequence, other, explicit asn1.RawValue
		var oid asn1.ObjectIdentifier
		if rest, err := asn1.Unmarshal(ext.Value, &sequence); err != nil || len(rest) != 0 {
			t.Fatalf("SAN sequence: %v", err)
		}
		if _, err := asn1.Unmarshal(sequence.Bytes, &other); err != nil || other.Class != 2 || other.Tag != 0 {
			t.Fatalf("SAN otherName: %v", err)
		}
		rest, err := asn1.Unmarshal(other.Bytes, &oid)
		if err != nil || !oid.Equal(asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2, 3}) {
			t.Fatalf("UPN oid=%v err=%v", oid, err)
		}
		if _, err := asn1.Unmarshal(rest, &explicit); err != nil || explicit.Class != 2 || explicit.Tag != 0 {
			t.Fatalf("UPN explicit value: %v", err)
		}
		if _, err := asn1.Unmarshal(explicit.Bytes, &upn); err != nil {
			t.Fatal(err)
		}
	}
	if upn != opts.UPN {
		t.Fatalf("UPN=%q, want %q", upn, opts.UPN)
	}
	if _, err := client.Cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("chain verified without intermediate")
	}
	if _, err := client.Cert.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: opts.NotAfter.Add(time.Second), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("expired client verified")
	}
}

func TestDefaultsAndServerIdentity(t *testing.T) {
	before := time.Now().Add(-time.Hour - time.Minute)
	root := New(t, nil, Options{CN: "Synthetic Root", CA: true})
	if root.Cert.NotBefore.Before(before) || root.Cert.NotAfter.Before(time.Now().Add(23*time.Hour)) {
		t.Fatal("unexpected default validity")
	}
	if err := root.Cert.CheckSignatureFrom(root.Cert); err != nil {
		t.Fatal(err)
	}
	server := Server(t, root)
	roots := x509.NewCertPool()
	roots.AddCert(root.Cert)
	for _, name := range []string{"localhost", "127.0.0.1"} {
		if _, err := server.Cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err != nil {
			t.Fatalf("server name %q: %v", name, err)
		}
	}
	if err := server.Cert.VerifyHostname("shore.example"); err == nil {
		t.Fatal("server accepted unrelated hostname")
	}
	if !server.Key.PublicKey.Equal(server.Cert.PublicKey) || server.Cert.IsCA {
		t.Fatal("invalid server key or CA status")
	}
}

func TestCRLSignaturesAndRevokedSerials(t *testing.T) {
	root, intermediate, client := Chain(t, Options{CN: "Synthetic Client"})
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name    string
		issuer  *Certificate
		revoked []*big.Int
	}{{"empty", root, nil}, {"revoked", intermediate, []*big.Int{client.Cert.SerialNumber, big.NewInt(123)}}} {
		t.Run(tc.name, func(t *testing.T) {
			list, err := x509.ParseRevocationList(CRL(t, tc.issuer, tc.revoked, now, now.Add(time.Hour)))
			if err != nil {
				t.Fatal(err)
			}
			if err := list.CheckSignatureFrom(tc.issuer.Cert); err != nil {
				t.Fatal(err)
			}
			if !list.ThisUpdate.Equal(now) || !list.NextUpdate.Equal(now.Add(time.Hour)) || len(list.RevokedCertificateEntries) != len(tc.revoked) {
				t.Fatal("CRL update times or revoked count changed")
			}
			for i, entry := range list.RevokedCertificateEntries {
				if entry.SerialNumber.Cmp(tc.revoked[i]) != 0 || !entry.RevocationTime.Equal(now) {
					t.Fatal("CRL revoked entry changed")
				}
			}
		})
	}
}

func TestFileEncodingsAndPermissions(t *testing.T) {
	cert := New(t, nil, Options{CN: "Synthetic File"})
	dir := t.TempDir()
	derPath, pemPath, keyPath := filepath.Join(dir, "cert.der"), filepath.Join(dir, "chain.pem"), filepath.Join(dir, "key.pem")
	WriteDER(t, derPath, cert.Cert.Raw)
	WritePEM(t, pemPath, "CERTIFICATE", cert.Cert.Raw, cert.Cert.Raw)
	WriteKey(t, keyPath, cert)
	for _, path := range []string{derPath, pemPath, keyPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private file permissions: %s %v", path, err)
		}
	}
	der, err := os.ReadFile(derPath)
	if err != nil || !bytes.Equal(der, cert.Cert.Raw) {
		t.Fatalf("DER differs: %v", err)
	}
	data, err := os.ReadFile(pemPath)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || !bytes.Equal(block.Bytes, cert.Cert.Raw) {
			t.Fatal("PEM chain differs")
		}
		data = rest
	}
	if len(data) != 0 {
		t.Fatal("unexpected trailing PEM data")
	}
	data, err = os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		t.Fatal("invalid private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil || !key.(*ecdsa.PrivateKey).Equal(cert.Key) {
		t.Fatalf("PKCS8 key differs: %v", err)
	}
}

type fatalReporter struct{}
type reportedFatal struct{ args []any }

func (fatalReporter) Helper()           {}
func (fatalReporter) Fatal(args ...any) { panic(reportedFatal{args}) }

func TestInvalidInputsReportFatal(t *testing.T) {
	leaf := New(t, nil, Options{CN: "Not a CRL signer"})
	for name, run := range map[string]func(){
		"invalid policy":         func() { New(fatalReporter{}, nil, Options{Policies: []asn1.ObjectIdentifier{{-1, 2}}}) },
		"non-signing CRL issuer": func() { CRL(fatalReporter{}, leaf, nil, time.Now(), time.Now().Add(time.Hour)) },
		"missing output parent":  func() { WriteDER(fatalReporter{}, filepath.Join(t.TempDir(), "absent", "file"), []byte("data")) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				fatal, ok := recover().(reportedFatal)
				if !ok || len(fatal.args) == 0 {
					t.Fatal("expected reported error")
				}
			}()
			run()
		})
	}
}
