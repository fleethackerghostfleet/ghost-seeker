package opauth_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth/pivtest"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestPIVApplication(t *testing.T) {
	now := time.Now()
	options := pivtest.Options{CN: "Synthetic Operator", UPN: "synthetic-a@mil", EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	root, inter, a := pivtest.Chain(t, options)
	options.UPN = "synthetic-b@mil"
	b := pivtest.New(t, inter, options)
	options.UPN = "synthetic-viewer@mil"
	viewer := pivtest.New(t, inter, options)
	options.UPN = "synthetic-unknown@mil"
	unknown := pivtest.New(t, inter, options)
	dir := t.TempDir()
	crls := filepath.Join(dir, "crls")
	if err := os.Mkdir(crls, 0700); err != nil {
		t.Fatal(err)
	}
	ca, users := filepath.Join(dir, "ca"), filepath.Join(dir, "users")
	pivtest.WritePEM(t, ca, "CERTIFICATE", root.Cert.Raw, inter.Cert.Raw)
	pivtest.WriteDER(t, users, []byte("synthetic-a@mil: operator\nsynthetic-b@mil: operator\nsynthetic-viewer@mil: viewer\n"))
	writeCRL := func(revoked []*big.Int) {
		pivtest.WriteDER(t, filepath.Join(crls, "inter"), pivtest.CRL(t, inter, revoked, now.Add(-time.Minute), now.Add(time.Hour)))
	}
	writeCRL(nil)
	pivtest.WriteDER(t, filepath.Join(crls, "root"), pivtest.CRL(t, root, nil, now.Add(-time.Minute), now.Add(time.Hour)))
	auth, err := pivauth.New(pivauth.Config{CABundle: ca, CRLDir: crls, UsersFile: users})
	if err != nil {
		t.Fatal(err)
	}
	srvCert := pivtest.Server(t, root)
	certFile, keyFile := filepath.Join(dir, "server"), filepath.Join(dir, "key")
	pivtest.WritePEM(t, certFile, "CERTIFICATE", srvCert.Cert.Raw)
	pivtest.WriteKey(t, keyFile, srvCert)
	tlsConfig, err := auth.TLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var audit bytes.Buffer
	data := store.New(10)
	data.Add(&command.Item{ID: "synthetic-item", TS: now}, 0)
	app := server.New(data, server.WithAdmin(true), server.WithAdminToken("synthetic-admin-token"))
	// Capture the upstream bridge identity without a real shore node.
	var upstreamBy string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Override struct {
				By string `json:"by"`
			} `json:"override"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		upstreamBy = body.Override.By
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	bridge := shorebridge.New(upstream.URL, "", nil)
	mux := http.NewServeMux()
	mux.Handle("/api/shore/", bridge.Handler())
	mux.Handle("/", app)
	sessions := opauth.NewSessions("synthetic-admin-token", true).WithCerts(auth, slog.New(slog.NewJSONHandler(&audit, nil)))
	s := httptest.NewUnstartedServer(sessions.Wrap(mux))
	s.TLS = tlsConfig
	s.StartTLS()
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(root.Cert)
	client := func(c *pivtest.Certificate) *http.Client {
		cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{{Certificate: [][]byte{c.Cert.Raw, inter.Cert.Raw}, PrivateKey: c.Key}}}}, Timeout: 3 * time.Second}
		t.Cleanup(cl.CloseIdleConnections)
		return cl
	}
	caClient, cbClient, viewerClient, unknownClient := client(a), client(b), client(viewer), client(unknown)
	request := func(cl *http.Client, method, path, body string, cookie *http.Cookie, status int) (string, []*http.Cookie) {
		t.Helper()
		req, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer synthetic-admin-token")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, resp.StatusCode, status, data)
		}
		return string(data), resp.Cookies()
	}
	_, cookies := request(caClient, "GET", "/api/config", "", nil, 200)
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatal("missing secure session")
	}
	cookie := cookies[0]
	body, _ := request(caClient, "GET", "/api/auth", "", cookie, 200)
	for _, field := range []string{`"mode":"piv"`, `"id":"synthetic-a@mil"`, `"role":"operator"`, `"dev":false`} {
		if !strings.Contains(body, field) {
			t.Fatal(body)
		}
	}
	request(caClient, "POST", "/api/directives", `{"text":"small boats"}`, cookie, 201)
	body, _ = request(caClient, "GET", "/api/policy", "", cookie, 200)
	if !strings.Contains(body, "synthetic-a@mil") {
		t.Fatal("missing policy actor: " + body)
	}
	request(caClient, "PUT", "/api/policy", `{"issuedBy":"spoofed","text":"synthetic admin edit"}`, cookie, 200)
	req, err := http.NewRequest("PUT", s.URL+"/api/policy", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := caClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin edit without re-auth token: %d", resp.StatusCode)
	}
	body, _ = request(caClient, "POST", "/api/items/synthetic-item/full-res", `{"by":"spoofed"}`, cookie, 200)
	if strings.Contains(body, `"fullResBy":"spoofed"`) || !strings.Contains(body, `"fullResBy":"synthetic-a@mil"`) {
		t.Fatal(body)
	}
	request(caClient, "POST", "/api/shore/override", `{"itemId":"synthetic-item","label":"boat"}`, cookie, 200)
	if upstreamBy != "synthetic-a@mil" {
		t.Fatalf("bridge actor: %q", upstreamBy)
	}
	request(viewerClient, "GET", "/api/config", "", nil, 200)
	body, _ = request(viewerClient, "POST", "/api/directives", `{"text":"small boats"}`, nil, 403)
	if !strings.Contains(body, "read-only role") {
		t.Fatal(body)
	}
	body, _ = request(unknownClient, "GET", "/", "", nil, 403)
	if !strings.Contains(body, "synthetic-unknown@mil") || !strings.Contains(body, "not authorized") {
		t.Fatal(body)
	}
	body, cleared := request(cbClient, "GET", "/api/config", "", cookie, 401)
	if !strings.Contains(body, "different certificate") || len(cleared) == 0 || cleared[0].MaxAge != -1 {
		t.Fatal(body)
	}
	body, _ = request(caClient, "GET", "/login?next=/api/config", "", nil, 200)
	if !strings.Contains(body, "Continue") || !strings.Contains(body, "synthetic-a@mil") {
		t.Fatal(body)
	}
	body, _ = request(viewerClient, "POST", "/logout", "", nil, 200)
	if !strings.Contains(body, "Remove your card") {
		t.Fatal(body)
	}
	pivtest.WriteDER(t, users, []byte("synthetic-a@mil: viewer\nsynthetic-b@mil: operator\nsynthetic-viewer@mil: viewer\n"))
	if err := auth.Reload(); err != nil {
		t.Fatal(err)
	}
	request(caClient, "POST", "/api/directives", `{"text":"small boats"}`, cookie, 403)
	missingTLS := httptest.NewRequest("GET", "/api/config", nil)
	missingTLS.Header.Set("Authorization", "Bearer synthetic-admin-token")
	recorder := httptest.NewRecorder()
	sessions.Wrap(app).ServeHTTP(recorder, missingTLS)
	if recorder.Code != 403 {
		t.Fatalf("bearer without certificate accepted: %d", recorder.Code)
	}
	writeCRL([]*big.Int{a.Cert.SerialNumber})
	if err := auth.Reload(); err != nil {
		t.Fatal(err)
	}
	body, _ = request(caClient, "GET", "/api/config", "", cookie, 403)
	if !strings.Contains(body, "revoked") {
		t.Fatal(body)
	}
	_, _, foreign := pivtest.Chain(t, options)
	if resp, err := client(foreign).Get(s.URL + "/api/config"); err == nil {
		resp.Body.Close()
		t.Fatal("untrusted CA reached app")
	}
	events := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(audit.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"event", "id", "role", "method", "path", "result", "status", "remote", "reason"} {
			if _, ok := entry[field]; !ok {
				t.Fatalf("missing %s: %s", field, line)
			}
		}
		events[entry["event"].(string)] = true
	}
	for _, event := range []string{"auth_success", "auth_failure", "session_mismatch", "request"} {
		if !events[event] {
			t.Fatal("missing audit event " + event)
		}
	}
}

func TestTokenAuthMode(t *testing.T) {
	s := opauth.NewSessions("synthetic-token", false)
	req := httptest.NewRequest("GET", "/api/auth", nil)
	req.Header.Set("Authorization", "Bearer synthetic-token")
	w := httptest.NewRecorder()
	s.Wrap(http.NotFoundHandler()).ServeHTTP(w, req)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["mode"] != "token" || body["role"] != "operator" || body["authenticated"] != true || body["id"] != "" {
		t.Fatal(body)
	}
}
