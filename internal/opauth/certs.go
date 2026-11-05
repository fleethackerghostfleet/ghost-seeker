package opauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth"
)

type Principal struct{ ID, Name, Role, Mode string }
type principalKey struct{}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
func Actor(ctx context.Context, fallback string) string {
	if p, ok := PrincipalFrom(ctx); ok && p.Mode == "piv" {
		return p.ID
	}
	return fallback
}
func (s *Sessions) WithCerts(auth *pivauth.Authenticator, audit *slog.Logger) *Sessions {
	s.certs = auth
	s.certRecheckInterval = 15 * time.Second
	s.dev = false
	s.anyPeer = false
	s.bindings = map[string]pivauth.Principal{}
	if audit == nil {
		audit = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	s.audit = audit
	return s
}

func (s *Sessions) WithCertRecheckInterval(interval time.Duration) *Sessions {
	if interval > 0 {
		s.certRecheckInterval = interval
	}
	return s
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (s *Sessions) auditEvent(r *http.Request, p Principal, event, kind, reason string, status int) {
	s.audit.Info("operator authentication", "event", event, "id", p.ID, "role", p.Role, "method", r.Method, "path", r.URL.Path, "result", kind, "status", status, "remote", r.RemoteAddr, "reason", reason)
}
func certPage(w http.ResponseWriter, status int, message, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Ghost Seeker — operator sign-in</title><style>body{font:15px system-ui,sans-serif;background:#0b1118;color:#d6e2ee;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}main{background:#121c27;border:1px solid #243445;border-radius:8px;padding:28px 32px;width:360px}h1{font-size:18px}p{color:#8aa0b6}a{color:#64b5f6}header{display:flex;align-items:center;gap:16px}</style><style>%s</style></head><body><main><header><h1>Ghost Seeker</h1>%s</header><p>%s</p>`, httpx.DocsLinkCSS, httpx.DocsLinkHTML, html.EscapeString(message))
	if next != "" {
		_, _ = fmt.Fprintf(w, `<a href="%s">Continue</a>`, html.EscapeString(safeNext(next)))
	}
	_, _ = fmt.Fprint(w, `</main></body></html>`)
}
func certFailure(w http.ResponseWriter, r *http.Request, status int, reason string) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/blob/") || r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "forbidden", "message": reason})
		return
	}
	certPage(w, status, reason, "")
}
func (s *Sessions) clearCertSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		delete(s.bindings, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func (s *Sessions) wrapCerts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		w = sw
		p := Principal{Mode: "piv"}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			defer func() {
				status := sw.status
				if status == 0 {
					status = 200
				}
				s.auditEvent(r, p, "request", "completed", "", status)
			}()
		}
		cp, err := s.certs.Authenticate(r.TLS)
		if err != nil {
			kind := "untrusted"
			var rejection *pivauth.Rejection
			if errors.As(err, &rejection) {
				kind = string(rejection.Kind)
			}
			s.auditEvent(r, p, "auth_failure", kind, err.Error(), 403)
			certFailure(w, r, 403, err.Error())
			return
		}
		p = Principal{ID: cp.ID, Name: cp.Name, Role: cp.Role, Mode: "piv"}
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
		valid := false
		if c, err := r.Cookie(CookieName); err == nil {
			s.mu.Lock()
			exp, exists := s.sessions[c.Value]
			bound := s.bindings[c.Value]
			valid = exists && !s.now().After(exp)
			mismatch := valid && bound.Fingerprint != cp.Fingerprint
			s.mu.Unlock()
			if mismatch {
				s.clearCertSession(w, r)
				reason := "The session belongs to a different certificate. Please try again."
				s.auditEvent(r, p, "session_mismatch", "fingerprint", reason, 401)
				certFailure(w, r, 401, reason)
				return
			}
		}
		if r.URL.Path == "/logout" && r.Method == http.MethodPost {
			s.clearCertSession(w, r)
			certPage(w, 200, "Signed out. Remove your card or close the browser to end the certificate session; while the card is present the browser will sign you in again.", "")
			return
		}
		if !valid {
			s.issue(w, r, cp)
			s.auditEvent(r, p, "auth_success", "success", "", 200)
		}
		if p.Role == "viewer" && r.URL.Path != "/login" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.auditEvent(r, p, "auth_failure", "read-only", "read-only role", 403)
			certFailure(w, r, 403, "read-only role")
			return
		}
		if r.URL.Path == "/login" {
			if r.Method == http.MethodPost {
				http.Redirect(w, r, "/login?next="+url.QueryEscape(safeNext(r.URL.Query().Get("next"))), http.StatusSeeOther)
				return
			}
			certPage(w, 200, fmt.Sprintf("%s (%s), role: %s", p.Name, p.ID, p.Role), safeNext(r.URL.Query().Get("next")))
			return
		}
		if r.URL.Path == "/api/auth" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "dev": false, "mode": "piv", "id": p.ID, "name": p.Name, "role": p.Role})
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(s.certRecheckInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					current, err := s.certs.Authenticate(r.TLS)
					if err != nil || current.Role != cp.Role {
						cancel()
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-done }()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
