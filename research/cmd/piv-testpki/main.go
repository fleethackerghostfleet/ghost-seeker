// piv-testpki generates synthetic, short-lived PKI for local demonstrations only.
package main

import (
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
)

type reporter struct{}

func (reporter) Helper()           {}
func (reporter) Fatal(args ...any) { log.Fatal(args...) }

func main() {
	out := flag.String("out", "data/piv-demo", "synthetic demo PKI output directory (must not exist)")
	flag.Parse()
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		log.Fatal("output must not already exist; choose a new directory to avoid overwriting keys")
	}
	for _, dir := range []string{*out, filepath.Join(*out, "ca-bundle"), filepath.Join(*out, "crl")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			log.Fatal(err)
		}
	}
	t := reporter{}
	root := pivtest.New(t, nil, pivtest.Options{CN: "SYNTHETIC DEMO Root - NOT FOR OPERATIONAL USE", CA: true})
	inter := pivtest.New(t, root, pivtest.Options{CN: "SYNTHETIC DEMO Intermediate", CA: true})
	pivtest.WritePEM(t, filepath.Join(*out, "ca-bundle/root.pem"), "CERTIFICATE", root.Cert.Raw)
	pivtest.WritePEM(t, filepath.Join(*out, "ca-bundle/intermediate.pem"), "CERTIFICATE", inter.Cert.Raw)
	server := pivtest.Server(t, root)
	pivtest.WritePEM(t, filepath.Join(*out, "server.crt"), "CERTIFICATE", server.Cert.Raw)
	pivtest.WriteKey(t, filepath.Join(*out, "server.key"), server)
	var revoked *big.Int
	for _, demo := range []struct{ name, cn, upn string }{{"operator", "DOE.JANE.Q.1000000001", "1000000001@mil"}, {"viewer", "SMITH.JOHN.A.1000000002", ""}, {"unlisted", "ROE.RICHARD.B.1000000003", ""}, {"revoked", "FAKE.REVOKED.X.1000000004", "1000000004@mil"}} {
		client := pivtest.New(t, inter, pivtest.Options{CN: demo.cn, UPN: demo.upn, KeyUsage: x509.KeyUsageDigitalSignature, EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		pivtest.WritePEM(t, filepath.Join(*out, demo.name+".crt"), "CERTIFICATE", client.Cert.Raw, inter.Cert.Raw)
		pivtest.WriteKey(t, filepath.Join(*out, demo.name+".key"), client)
		if demo.name == "revoked" {
			revoked = client.Cert.SerialNumber
		}
	}
	now := time.Now()
	pivtest.WriteDER(t, filepath.Join(*out, "crl/root.crl"), pivtest.CRL(t, root, nil, now.Add(-time.Minute), now.Add(12*time.Hour)))
	pivtest.WriteDER(t, filepath.Join(*out, "crl/intermediate.crl"), pivtest.CRL(t, inter, []*big.Int{revoked}, now.Add(-time.Minute), now.Add(12*time.Hour)))
	pivtest.WriteDER(t, filepath.Join(*out, "users.yaml"), []byte("# SYNTHETIC demo identities only\n\"1000000001@mil\": operator\n\"1000000002\": viewer\n"))
	fmt.Printf("SYNTHETIC / UNCLASSIFIED demo PKI generated in %s (private files mode 0600).\nCA/client validity: 24h; server: 1h; CRLs: 12h. Never use operationally.\nClients: operator, viewer (CN identity), unlisted, revoked (on intermediate CRL).\nPKCS#12 skipped: x/crypto/pkcs12 supports decoding, not encoding. PEM certs and keys are provided.\n\nRun (loads the existing dev bridge token):\ngo run ./cmd/ghost-seeker -addr :8443 -auth-mode piv -tls-cert %q -tls-key %q -piv-ca-bundle %q -piv-crl-dir %q -piv-users %q\n", *out, filepath.Join(*out, "server.crt"), filepath.Join(*out, "server.key"), filepath.Join(*out, "ca-bundle"), filepath.Join(*out, "crl"), filepath.Join(*out, "users.yaml"))
}
