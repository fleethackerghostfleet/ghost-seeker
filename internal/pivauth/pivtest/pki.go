// Package pivtest generates ephemeral synthetic PKI for tests.
package pivtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"time"
)

// Reporter is the minimal testing/logging interface used by the generators.
type Reporter interface {
	Helper()
	Fatal(...any)
}

type Certificate struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}
type Options struct {
	CN, UPN             string
	EKU                 []x509.ExtKeyUsage
	UnknownEKU          []asn1.ObjectIdentifier
	KeyUsage            x509.KeyUsage
	NotBefore, NotAfter time.Time
	Policies            []asn1.ObjectIdentifier
	CA                  bool
}

func New(t Reporter, parent *Certificate, o Options) *Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	if o.NotBefore.IsZero() {
		o.NotBefore = time.Now().Add(-time.Hour)
	}
	if o.NotAfter.IsZero() {
		o.NotAfter = time.Now().Add(24 * time.Hour)
	}
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: o.CN}, NotBefore: o.NotBefore, NotAfter: o.NotAfter, KeyUsage: o.KeyUsage, ExtKeyUsage: o.EKU, UnknownExtKeyUsage: o.UnknownEKU, IsCA: o.CA, BasicConstraintsValid: true, PolicyIdentifiers: o.Policies}
	if o.CA {
		tpl.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	}
	for _, policy := range o.Policies {
		oid, err := x509.ParseOID(policy.String())
		if err != nil {
			t.Fatal(err)
		}
		tpl.Policies = append(tpl.Policies, oid)
	}
	if o.UPN != "" {
		oid, _ := asn1.Marshal(asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2, 3})
		value, _ := asn1.MarshalWithParams(o.UPN, "utf8")
		explicit, _ := asn1.Marshal(asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: value})
		other, _ := asn1.Marshal(asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: append(oid, explicit...)})
		san, _ := asn1.Marshal(asn1.RawValue{Tag: 16, IsCompound: true, Bytes: other})
		tpl.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: san}}
	}
	signer, issuer := key, tpl
	if parent != nil {
		signer, issuer = parent.Key, parent.Cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, issuer, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &Certificate{cert, key}
}
func Chain(t Reporter, o Options) (root, intermediate, client *Certificate) {
	t.Helper()
	root = New(t, nil, Options{CN: "Synthetic Root", CA: true, Policies: o.Policies})
	intermediate = New(t, root, Options{CN: "Synthetic Intermediate", CA: true, Policies: o.Policies})
	client = New(t, intermediate, o)
	return
}
func CRL(t Reporter, issuer *Certificate, revoked []*big.Int, thisUpdate, nextUpdate time.Time) []byte {
	t.Helper()
	entries := []x509.RevocationListEntry{}
	for _, s := range revoked {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: s, RevocationTime: thisUpdate})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(thisUpdate.UnixNano()), ThisUpdate: thisUpdate, NextUpdate: nextUpdate, RevokedCertificateEntries: entries}, issuer.Cert, issuer.Key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
func WriteDER(t Reporter, path string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, der, 0600); err != nil {
		t.Fatal(err)
	}
}
func WritePEM(t Reporter, path, kind string, ders ...[]byte) {
	t.Helper()
	var data []byte
	for _, der := range ders {
		data = append(data, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})...)
	}
	WriteDER(t, path, data)
}
func WriteKey(t Reporter, path string, c *Certificate) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(c.Key)
	if err != nil {
		t.Fatal(err)
	}
	WritePEM(t, path, "PRIVATE KEY", der)
}
func Server(t Reporter, root *Certificate) *Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, root.Cert, &key.PublicKey, root.Key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &Certificate{cert, key}
}
