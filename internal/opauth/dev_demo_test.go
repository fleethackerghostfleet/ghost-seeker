//go:build !prod

package opauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExplicitDevAndProduction(t *testing.T) {
	tok, dev, err := Load("0123456789abcdef", "", DefaultDevFile)
	if err != nil || dev || tok != "0123456789abcdef" {
		t.Fatalf("explicit: %q %v %v", tok, dev, err)
	}
	if _, _, err := Load("short", "", ""); err == nil {
		t.Fatal("short token accepted")
	}
	if _, _, err := Load("", "", "none"); err == nil || !strings.Contains(err.Error(), "operator token required") {
		t.Fatalf("production without token: %v", err)
	}
	devFile := filepath.Join(t.TempDir(), "sub", "operator.token")
	tok1, dev, err := Load("", "", devFile)
	if err != nil || !dev || len(tok1) != 64 {
		t.Fatalf("dev: %q %v %v", tok1, dev, err)
	}
	if st, _ := os.Stat(devFile); st.Mode().Perm() != 0o600 {
		t.Fatalf("dev file mode %v", st.Mode().Perm())
	}
	tok2, _, err := Load("", "", devFile)
	if err != nil || tok2 != tok1 {
		t.Fatalf("second process must share the dev token: %q vs %q (%v)", tok2, tok1, err)
	}
	tok3, dev, err := Load("", devFile, "none")
	if err != nil || dev || tok3 != tok1 {
		t.Fatalf("token file: %q %v %v", tok3, dev, err)
	}
}

func TestLoadConcurrentDevStart(t *testing.T) {
	devFile := filepath.Join(t.TempDir(), "operator.token")
	const n = 8
	toks := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			tok, _, err := Load("", "", devFile)
			toks <- tok
			errs <- err
		}()
	}
	first := ""
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		tok := <-toks
		if len(tok) != 64 {
			t.Fatalf("partial token %q", tok)
		}
		if first == "" {
			first = tok
		} else if tok != first {
			t.Fatalf("processes disagree: %q vs %q", tok, first)
		}
	}
}

func TestSessionsDevTrustPeers(t *testing.T) {
	s, h := newApp(true)
	s.TrustPeers(true)
	// Containers: the host's browser arrives from the bridge gateway, addressing localhost.
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "172.18.0.1:4242", nil); w.Code != http.StatusOK {
		t.Fatalf("bridge peer with localhost Host should auto-login: %d", w.Code)
	}
	// Still never behind a proxy hop, and never for a non-loopback Host.
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "172.18.0.1:4242", map[string]string{"X-Forwarded-For": "192.168.1.20"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("proxied: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "http://ops.ship.mil/api/shore/snapshot", nil)
	r.RemoteAddr = "172.18.0.1:4242"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("public host: %d", w.Code)
	}
	// Production ignores the switch.
	ps, ph := newApp(false)
	ps.TrustPeers(true)
	if w := do(ph, http.MethodGet, "/api/shore/snapshot", "172.18.0.1:4242", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("prod trust-peers: %d", w.Code)
	}
}

func TestDemoAcceptAnyToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		demo  bool
		token string
		want  int
	}{
		{"off wrong", false, "arbitrary-demo-token", http.StatusUnauthorized},
		{"off valid", false, "secret-token-12345", http.StatusSeeOther},
		{"on arbitrary", true, "arbitrary-demo-token", http.StatusSeeOther},
		{"on empty", true, "", http.StatusUnauthorized},
		{"on whitespace", true, "   ", http.StatusUnauthorized},
		{"off empty", false, "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSessions("secret-token-12345", false).DemoAcceptAnyToken(tc.demo)
			h := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p, ok := PrincipalFrom(r.Context())
				if !ok || p.Role != "operator" || p.Mode != "token" {
					t.Errorf("principal = %+v, present = %v", p, ok)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			page := do(h, http.MethodGet, "/login", "10.0.0.9:5", nil)
			if strings.Contains(page.Body.String(), "any non-empty token") != tc.demo {
				t.Fatal("login hint does not match demo mode")
			}
			w := do(h, http.MethodPost, "/login", "10.0.0.9:5", map[string]string{
				"Content-Type": "application/x-www-form-urlencoded", "form": url.Values{"token": {tc.token}}.Encode(),
			})
			if w.Code != tc.want {
				t.Fatalf("login = %d, want %d", w.Code, tc.want)
			}
			cookies := w.Result().Cookies()
			if tc.want == http.StatusUnauthorized {
				if len(cookies) != 0 {
					t.Fatal("rejected login issued a cookie")
				}
				return
			}
			if len(cookies) != 1 {
				t.Fatalf("cookies = %v", cookies)
			}
			if w := do(h, http.MethodPost, "/api/operator", "10.0.0.9:5", nil, cookies...); w.Code != http.StatusNoContent {
				t.Fatalf("operator access = %d", w.Code)
			}
			if w := do(h, http.MethodGet, "/api/auth", "10.0.0.9:5", nil, cookies...); !strings.Contains(w.Body.String(), `"role":"operator"`) {
				t.Fatalf("auth identity = %s", w.Body.String())
			}
			if w := do(h, http.MethodPost, "/api/operator", "10.0.0.9:5", map[string]string{"Authorization": "Bearer arbitrary-demo-token"}); w.Code != http.StatusUnauthorized {
				t.Fatalf("arbitrary bearer accepted: %d", w.Code)
			}
		})
	}
}
