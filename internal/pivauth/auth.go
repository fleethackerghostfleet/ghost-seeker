// Package pivauth implements offline, fail-closed CAC/PIV authentication.
package pivauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"
	"gopkg.in/yaml.v3"
)

type Config struct {
	CABundle, CRLDir         string
	CRLGrace, ReloadInterval time.Duration
	OCSPURL                  string
	PolicyOIDs               []string
	UsersFile                string
	Now                      func() time.Time
	Logger                   *slog.Logger
}

type Principal struct{ ID, Name, Role, Fingerprint string }
type Kind string

const (
	Untrusted     Kind = "untrusted"
	Expired       Kind = "expired"
	Revoked       Kind = "revoked"
	CRLMissing    Kind = "crl-missing"
	CRLStale      Kind = "crl-stale"
	KeyUsage      Kind = "key-usage"
	Policy        Kind = "policy"
	NotAuthorized Kind = "not-authorized"
)

type Rejection struct {
	Reason string
	Kind   Kind
}

func (r *Rejection) Error() string       { return r.Reason }
func reject(k Kind, reason string) error { return &Rejection{Kind: k, Reason: reason} }

type snapshot struct {
	crls  []*x509.RevocationList
	users map[string]string
	stamp string
}
type Authenticator struct {
	cfg                              Config
	roots, intermediates, acceptable *x509.CertPool
	policies                         []x509.OID
	mu                               sync.RWMutex
	reloadMu                         sync.Mutex
	state                            snapshot
	client                           *http.Client
}

func New(cfg Config) (*Authenticator, error) {
	if cfg.CABundle == "" || cfg.CRLDir == "" || cfg.UsersFile == "" {
		return nil, errors.New("CA bundle, CRL directory, and users file are required")
	}
	if cfg.CRLGrace < 0 || cfg.ReloadInterval < 0 {
		return nil, errors.New("durations cannot be negative")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ReloadInterval == 0 {
		cfg.ReloadInterval = time.Minute
	}
	a := &Authenticator{cfg: cfg, roots: x509.NewCertPool(), intermediates: x509.NewCertPool(), acceptable: x509.NewCertPool(), client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, s := range cfg.PolicyOIDs {
		oid, err := x509.ParseOID(s)
		if err != nil {
			return nil, fmt.Errorf("policy OID: %w", err)
		}
		a.policies = append(a.policies, oid)
	}
	files, err := paths(cfg.CABundle, false)
	if err != nil {
		return nil, err
	}
	roots := 0
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		count := 0
		for len(bytes.TrimSpace(data)) > 0 {
			block, rest := pem.Decode(data)
			if block == nil {
				return nil, fmt.Errorf("invalid CA PEM: %s", p)
			}
			data = rest
			if block.Type != "CERTIFICATE" {
				continue
			}
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, err
			}
			count++
			a.acceptable.AddCert(c)
			if bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignatureFrom(c) == nil {
				a.roots.AddCert(c)
				roots++
			} else {
				a.intermediates.AddCert(c)
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("no certificates in %s", p)
		}
	}
	if roots == 0 {
		return nil, errors.New("CA bundle has no root")
	}
	if err := a.Reload(); err != nil {
		return nil, err
	}
	return a, nil
}

func paths(path string, directory bool) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if directory {
			return nil, errors.New("CRL path must be a directory")
		}
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, filepath.Join(path, e.Name()))
		}
	}
	return out, nil
}
func (a *Authenticator) stamp() (string, error) {
	files, err := paths(a.cfg.CRLDir, true)
	if err != nil {
		return "", err
	}
	files = append(files, a.cfg.UsersFile)
	var b strings.Builder
	for _, p := range files {
		i, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s:%d:%d;", p, i.Size(), i.ModTime().UnixNano())
	}
	return b.String(), nil
}
func (a *Authenticator) Reload() error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	stamp, err := a.stamp()
	if err != nil {
		return err
	}
	files, err := paths(a.cfg.CRLDir, true)
	if err != nil {
		return err
	}
	s := snapshot{stamp: stamp}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("-----BEGIN")) {
			for len(bytes.TrimSpace(data)) > 0 {
				block, rest := pem.Decode(data)
				if block == nil {
					return fmt.Errorf("invalid CRL PEM: %s", p)
				}
				crl, err := x509.ParseRevocationList(block.Bytes)
				if err != nil {
					return err
				}
				s.crls = append(s.crls, crl)
				data = rest
			}
		} else {
			crl, err := x509.ParseRevocationList(data)
			if err != nil {
				return err
			}
			s.crls = append(s.crls, crl)
		}
	}
	data, err := os.ReadFile(a.cfg.UsersFile)
	if err != nil {
		return err
	}
	if err = yaml.Unmarshal(data, &s.users); err != nil {
		return err
	}
	for id, role := range s.users {
		if id == "" || (role != "operator" && role != "viewer") {
			return errors.New("invalid users entry or role")
		}
	}
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	return nil
}
func (a *Authenticator) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(a.cfg.ReloadInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stamp, err := a.stamp()
				a.mu.RLock()
				old := a.state.stamp
				a.mu.RUnlock()
				if err == nil && stamp != old {
					err = a.Reload()
				}
				if err != nil {
					a.cfg.Logger.Warn("PIV reload failed; retaining previous configuration", "error", err)
				}
			}
		}
	}()
}
func (a *Authenticator) verify(certs []*x509.Certificate, policy bool) ([][]*x509.Certificate, error) {
	if len(certs) == 0 {
		return nil, reject(Untrusted, "A client certificate is required.")
	}
	intermediates := a.intermediates.Clone()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	opts := x509.VerifyOptions{Roots: a.roots, Intermediates: intermediates, CurrentTime: a.cfg.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}
	if policy {
		opts.CertificatePolicies = a.policies
	}
	leaf := certs[0]
	if policy && len(a.policies) > 0 {
		// VerifyOptions selects policies but does not require an explicit policy.
		// Set the local verification constraint without altering the signed DER.
		copy := *leaf
		copy.RequireExplicitPolicyZero = true
		leaf = &copy
	}
	chains, err := leaf.Verify(opts)
	if err != nil {
		var invalid x509.CertificateInvalidError
		if errors.As(err, &invalid) {
			if invalid.Reason == x509.Expired {
				return nil, reject(Expired, "The certificate is expired or not yet valid.")
			}
			if invalid.Reason == x509.NoValidChains && policy && len(a.policies) > 0 {
				return nil, reject(Policy, "The certificate does not meet the required certificate policy.")
			}
		}
		return nil, reject(Untrusted, "The certificate chain is not trusted.")
	}
	return chains, nil
}
func (a *Authenticator) TLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, ClientCAs: a.acceptable.Clone(), ClientAuth: tls.RequireAnyClientCert, VerifyConnection: func(cs tls.ConnectionState) error { _, err := a.verify(cs.PeerCertificates, false); return err }}, nil
}
func (a *Authenticator) Authenticate(cs *tls.ConnectionState) (Principal, error) {
	if cs == nil {
		return Principal{}, reject(Untrusted, "A client certificate is required.")
	}
	chains, err := a.verify(cs.PeerCertificates, false)
	if err != nil {
		return Principal{}, err
	}
	leaf := cs.PeerCertificates[0]
	eku := false
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageClientAuth {
			eku = true
		}
	}
	for _, u := range leaf.UnknownExtKeyUsage {
		if u.String() == "1.3.6.1.4.1.311.20.2.2" {
			eku = true
		}
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !eku {
		return Principal{}, reject(KeyUsage, "The certificate is not valid for smartcard authentication.")
	}
	if len(a.policies) > 0 {
		chains, err = a.verify(cs.PeerCertificates, true)
		if err != nil {
			return Principal{}, err
		}
	}
	var authChains [][]*x509.Certificate
	for _, chain := range chains {
		if authenticationPurpose(chain) {
			authChains = append(authChains, chain)
		}
	}
	if len(authChains) == 0 {
		return Principal{}, reject(KeyUsage, "The certificate chain is not valid for smartcard authentication.")
	}
	a.mu.RLock()
	s := a.state
	a.mu.RUnlock()
	var revErr error
	for _, chain := range authChains {
		revErr = nil
		for i := 0; i < len(chain)-1; i++ {
			if revErr = a.revocation(chain[i], chain[i+1], s.crls); revErr != nil {
				break
			}
		}
		if revErr == nil {
			break
		}
	}
	if revErr != nil {
		return Principal{}, revErr
	}
	p := identity(leaf)
	role, ok := s.users[p.ID]
	if !ok {
		return Principal{}, reject(NotAuthorized, "Your certificate is valid but ID "+p.ID+" is not authorized for this system. Ask the system administrator to add it to the users file.")
	}
	p.Role = role
	return p, nil
}

func authenticationPurpose(chain []*x509.Certificate) bool {
	const clientAuth, smartcard = 1, 2
	common := clientAuth | smartcard
	for _, cert := range chain {
		if len(cert.ExtKeyUsage) == 0 && len(cert.UnknownExtKeyUsage) == 0 {
			continue
		}
		allowed := 0
		for _, usage := range cert.ExtKeyUsage {
			switch usage {
			case x509.ExtKeyUsageAny:
				allowed |= clientAuth | smartcard
			case x509.ExtKeyUsageClientAuth:
				allowed |= clientAuth
			}
		}
		for _, usage := range cert.UnknownExtKeyUsage {
			if usage.String() == "1.3.6.1.4.1.311.20.2.2" {
				allowed |= smartcard
			}
		}
		common &= allowed
	}
	return len(chain) > 0 && common != 0
}

func (a *Authenticator) revocation(cert, issuer *x509.Certificate, crls []*x509.RevocationList) error {
	if a.cfg.OCSPURL != "" {
		status, err := a.ocspStatus(cert, issuer)
		if err == nil {
			if status == ocsp.Revoked {
				return reject(Revoked, "The certificate has been revoked.")
			}
			if status == ocsp.Good {
				return nil
			}
		}
	}
	var best *x509.RevocationList
	for _, crl := range crls {
		if !bytes.Equal(crl.RawIssuer, cert.RawIssuer) {
			continue
		}
		if err := crl.CheckSignatureFrom(issuer); err != nil {
			a.cfg.Logger.Warn("Ignoring CRL with invalid signature")
			continue
		}
		if crl.ThisUpdate.After(a.cfg.Now()) {
			continue
		}
		if best == nil || crl.ThisUpdate.After(best.ThisUpdate) {
			best = crl
		}
	}
	if best == nil {
		return reject(CRLMissing, "Revocation information is missing for this certificate.")
	}
	for _, r := range best.RevokedCertificateEntries {
		if cert.SerialNumber.Cmp(r.SerialNumber) == 0 {
			return reject(Revoked, "The certificate has been revoked.")
		}
	}
	if best.NextUpdate.IsZero() || a.cfg.Now().After(best.NextUpdate.Add(a.cfg.CRLGrace)) {
		return reject(CRLStale, "Revocation information is out of date.")
	}
	if a.cfg.Now().After(best.NextUpdate) {
		a.cfg.Logger.Warn("Accepting stale CRL within configured grace period")
	}
	return nil
}
func (a *Authenticator) ocspStatus(cert, issuer *x509.Certificate) (int, error) {
	data, err := ocsp.CreateRequest(cert, issuer, nil)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, a.cfg.OCSPURL, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/ocsp-request")
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, errors.New("OCSP response failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	result, err := ocsp.ParseResponseForCert(body, cert, nil)
	if err != nil {
		return 0, err
	}
	now := a.cfg.Now()
	signer := issuer
	if delegated := result.Certificate; delegated != nil && !bytes.Equal(delegated.Raw, issuer.Raw) {
		if !bytes.Equal(delegated.RawIssuer, issuer.RawSubject) {
			return 0, errors.New("OCSP responder has wrong issuer")
		}
		if err := delegated.CheckSignatureFrom(issuer); err != nil {
			return 0, errors.New("OCSP responder is not issued by the certificate issuer")
		}
		authorized := false
		for _, usage := range delegated.ExtKeyUsage {
			if usage == x509.ExtKeyUsageOCSPSigning {
				authorized = true
			}
		}
		if !authorized || now.Before(delegated.NotBefore) || now.After(delegated.NotAfter) {
			return 0, errors.New("OCSP responder is not authorized or current")
		}
		signer = delegated
	}
	if err := result.CheckSignatureFrom(signer); err != nil {
		return 0, errors.New("invalid OCSP response signature")
	}
	if result.ThisUpdate.After(now) || result.NextUpdate.IsZero() || now.After(result.NextUpdate) {
		return 0, errors.New("OCSP response outside validity period")
	}
	return result.Status, nil
}

var trailingID = regexp.MustCompile(`\.(\d{10,})$`)

func identity(c *x509.Certificate) Principal {
	sum := sha256.Sum256(c.Raw)
	fp := hex.EncodeToString(sum[:])
	id := upn(c)
	cn := c.Subject.CommonName
	if id == "" {
		if match := trailingID.FindStringSubmatch(cn); match != nil {
			id = match[1]
		} else {
			id = "sha256:" + fp
		}
	}
	name := cn
	if match := trailingID.FindStringIndex(cn); match != nil {
		parts := strings.Split(cn[:match[0]], ".")
		if len(parts) >= 2 {
			words := append(append([]string{}, parts[1:]...), parts[0])
			for i, w := range words {
				w = strings.ToLower(w)
				if len(w) > 0 {
					w = strings.ToUpper(w[:1]) + w[1:]
				}
				if len(w) == 1 {
					w += "."
				}
				words[i] = w
			}
			name = strings.Join(words, " ")
		}
	}
	return Principal{ID: id, Name: name, Fingerprint: fp}
}
func upn(c *x509.Certificate) string {
	for _, ext := range c.Extensions {
		if ext.Id.String() != "2.5.29.17" {
			continue
		}
		var seq asn1.RawValue
		rest, err := asn1.Unmarshal(ext.Value, &seq)
		if err != nil || len(rest) != 0 || seq.Tag != asn1.TagSequence {
			continue
		}
		data := seq.Bytes
		for len(data) > 0 {
			var v asn1.RawValue
			data, err = asn1.Unmarshal(data, &v)
			if err != nil {
				break
			}
			if v.Class != asn1.ClassContextSpecific || v.Tag != 0 {
				continue
			}
			var oid asn1.ObjectIdentifier
			inner, e := asn1.Unmarshal(v.Bytes, &oid)
			if e != nil || oid.String() != "1.3.6.1.4.1.311.20.2.3" {
				continue
			}
			var explicit asn1.RawValue
			tail, e := asn1.Unmarshal(inner, &explicit)
			if e != nil || len(tail) != 0 || explicit.Class != asn1.ClassContextSpecific || explicit.Tag != 0 {
				continue
			}
			var value asn1.RawValue
			tail, e = asn1.Unmarshal(explicit.Bytes, &value)
			if e == nil && len(tail) == 0 && value.Class == 0 && value.Tag == asn1.TagUTF8String {
				return string(value.Bytes)
			}
		}
	}
	return ""
}
