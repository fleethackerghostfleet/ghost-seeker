package opauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth"
)

// CookieName is the operator session cookie the web app sets.
const CookieName = "gs_session"

// Sessions turns the operator token into browser sessions for the web app:
// the token never reaches the browser; POST /login exchanges it for an
// HttpOnly, SameSite=Strict cookie. In dev mode (auto-generated token) a
// loopback browser is logged in automatically so `make demo-ui` stays
// zero-step. Everything else: /api/* and /blob/* answer 401 JSON, pages
// redirect to /login.
type Sessions struct {
	token               string
	reviewJoinKey       string
	dev                 bool
	anyPeer             bool
	demoAcceptAnyToken  bool
	ttl                 time.Duration
	now                 func() time.Time
	certs               *pivauth.Authenticator
	certRecheckInterval time.Duration
	audit               *slog.Logger
	bindings            map[string]pivauth.Principal

	mu       sync.Mutex
	sessions map[string]time.Time
}

// NewSessions builds the session layer for token; dev enables loopback auto-login.
func NewSessions(token string, dev bool) *Sessions {
	return &Sessions{token: token, dev: dev, ttl: 12 * time.Hour, now: time.Now, bindings: map[string]pivauth.Principal{}, sessions: map[string]time.Time{}}
}

// Dev reports whether loopback auto-login is on.
func (s *Sessions) Dev() bool { return s.dev }

// WithReviewJoinKey enables the reviewer-only bearer join path.
func (s *Sessions) WithReviewJoinKey(key string) *Sessions {
	s.reviewJoinKey = key
	return s
}

// local reports whether r qualifies for dev auto-login.
func (s *Sessions) local(r *http.Request) bool {
	if !s.dev {
		return false
	}
	if s.anyPeer {
		return !forwarded(r) && isLoopbackHost(r.Host)
	}
	return Loopback(r)
}

// Wrap mounts /login, /logout and /api/auth in front of next and enforces
// authentication on everything else.
func (s *Sessions) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.certs != nil {
			s.wrapCerts(next).ServeHTTP(w, r)
			return
		}
		if s.reviewJoinKey != "" && strings.HasPrefix(r.URL.Path, "/join/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			key := strings.TrimPrefix(r.URL.Path, "/join/")
			if r.Method != http.MethodGet || key == "" || strings.Contains(key, "/") || !Equal(s.reviewJoinKey, key) {
				http.NotFound(w, r)
				return
			}
			s.issue(w, r, pivauth.Principal{Role: "reviewer"})
			// A same-origin refresh, not a 303: browsers drop SameSite=Strict
			// cookies on redirects that continue a cross-site navigation
			// (a link clicked in chat or email).
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(reviewJoinPage))
			return
		}
		role, authed := s.authentication(r)
		if authed && role == "reviewer" && !reviewerRequestAllowed(r) {
			forbiddenReviewer(w)
			return
		}
		switch {
		case r.URL.Path == "/login":
			s.login(w, r)
			return
		case r.URL.Path == "/logout" && r.Method == http.MethodPost:
			s.logout(w, r)
			return
		case r.URL.Path == "/healthz" || publicBrandAsset(r):
			next.ServeHTTP(w, r)
			return
		}
		if !authed && s.local(r) {
			s.issue(w, r)
			authed = true
			role = "operator"
		}
		if r.URL.Path == "/api/auth" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": authed, "dev": s.dev, "mode": "token", "id": "", "name": "", "role": role})
			return
		}
		if !authed {
			if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/blob/") || r.Method != http.MethodGet {
				Unauthorized(w)
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		principal := Principal{Role: role, Mode: "token"}
		if principal.Role == "reviewer" && !reviewerRequestAllowed(r) {
			forbiddenReviewer(w)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal))
		next.ServeHTTP(w, r)
	})
}

// publicBrandAssets are the logo files the sign-in pages show before a session exists.
var publicBrandAssets = map[string]bool{"/brand/logo.png": true, "/brand/favicon-32.png": true, "/brand/apple-touch-icon.png": true}

func publicBrandAsset(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && publicBrandAssets[r.URL.Path]
}

func reviewerRequestAllowed(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || (r.Method == http.MethodPost && r.URL.Path == "/logout") {
		return true
	}
	if r.Method != http.MethodPost {
		return false
	}
	path := r.URL.EscapedPath()
	return pathResourceAction(path, "/api/items/", "/review") ||
		pathResourceAction(path, "/api/reviews/", "/reason") ||
		pathResourceAction(path, "/api/reviews/", "/undo")
}

func pathResourceAction(path, prefix, suffix string) bool {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id != "" && !strings.Contains(id, "/")
}

func forbiddenReviewer(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "forbidden", "message": "reviewer sessions are limited to review actions"})
}

func (s *Sessions) authenticated(r *http.Request) bool {
	_, ok := s.authentication(r)
	return ok
}

func (s *Sessions) authentication(r *http.Request) (string, bool) {
	if Equal(s.token, BearerToken(r)) {
		return "operator", true
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok {
		return "", false
	}
	if s.now().After(exp) {
		delete(s.sessions, c.Value)
		delete(s.bindings, c.Value)
		return "", false
	}
	binding, ok := s.bindings[c.Value]
	if !ok || binding.Role == "" {
		return "", false
	}
	return binding.Role, true
}

func (s *Sessions) issue(w http.ResponseWriter, r *http.Request, principal ...pivauth.Principal) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	s.mu.Lock()
	if previous, err := r.Cookie(CookieName); err == nil {
		delete(s.sessions, previous.Value)
		delete(s.bindings, previous.Value)
	}
	for k, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, k)
			delete(s.bindings, k)
		}
	}
	s.sessions[id] = now.Add(s.ttl)
	s.bindings[id] = pivauth.Principal{Role: "operator"}
	if len(principal) > 0 {
		s.bindings[id] = principal[0]
	}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: secure(r), MaxAge: int(s.ttl / time.Second),
	})
	r.AddCookie(&http.Cookie{Name: CookieName, Value: id})
}

func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Sessions) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		if s.authenticated(r) || s.local(r) {
			if !s.authenticated(r) {
				s.issue(w, r)
			}
			http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusFound)
			return
		}
		s.page(w, "", r.URL.Query().Get("next"))
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", httpx.RequestBodyStatus(err))
			return
		}
		token := strings.TrimSpace(r.PostForm.Get("token"))
		if token == "" || (!s.demoAcceptAnyToken && !Equal(s.token, token)) {
			w.WriteHeader(http.StatusUnauthorized)
			s.page(w, "invalid operator token", r.PostForm.Get("next"))
			return
		}
		s.issue(w, r)
		http.Redirect(w, r, safeNext(r.PostForm.Get("next")), http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Sessions) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		delete(s.bindings, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure(r), MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeNext only allows same-site absolute paths so /login cannot redirect off-site.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, "/login") {
		return "/"
	}
	for _, c := range next {
		if c <= ' ' || c == 0x7f {
			return "/"
		}
	}
	return next
}

func (s *Sessions) page(w http.ResponseWriter, msg string, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "same-origin") // so the form post carries a real Origin, not "null"
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Ghost Seeker — operator sign-in</title>` + brandHead +
		`<style>body{font:15px system-ui,sans-serif;background:#0b1118;color:#d6e2ee;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}` +
		`form{background:#121c27;border:1px solid #243445;border-radius:8px;padding:28px 32px;width:360px}h1{margin:0 0 4px}h1 img{display:block}p{margin:4px 0 16px;color:#8aa0b6}` +
		`input{width:100%;box-sizing:border-box;padding:9px;border-radius:6px;border:1px solid #2d4258;background:#0b1118;color:#fff;font-size:14px}` +
		`button{margin-top:14px;width:100%;padding:9px;border:0;border-radius:6px;background:#1e88e5;color:#fff;font-weight:600;cursor:pointer}.err{color:#ff7b72}header{display:flex;align-items:center;gap:16px;margin-bottom:8px}` + httpx.DocsLinkCSS + `</style></head><body>` +
		`<form method="post" action="/login"><header>` + brandLogo + httpx.DocsLinkHTML + `</header>`)
	if s.demoAcceptAnyToken {
		b.WriteString(`<p>DEMO MODE: any non-empty token grants operator access.</p>`)
	} else {
		b.WriteString(`<p>Enter the operator token (GS_OPERATOR_TOKEN) to open the shore console.</p>`)
	}
	if msg != "" {
		b.WriteString(`<p class="err">` + html.EscapeString(msg) + `</p>`)
	}
	if next = safeNext(next); next != "/" {
		b.WriteString(`<input type="hidden" name="next" value="` + html.EscapeString(next) + `">`)
	}
	b.WriteString(`<input type="password" name="token" placeholder="operator token" autocomplete="current-password" autofocus required>` +
		`<button type="submit">Sign in</button></form></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}

const (
	brandHead = `<link rel="icon" type="image/png" sizes="32x32" href="/brand/favicon-32.png"><link rel="apple-touch-icon" href="/brand/apple-touch-icon.png">`
	brandLogo = `<h1><img src="/brand/logo.png" width="178" height="96" alt="Ghost Seeker"></h1>`
)

const reviewJoinPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/#review"><title>Ghost Seeker review</title></head><body><p><a href="/#review">Start Operator Review</a></p></body></html>`
