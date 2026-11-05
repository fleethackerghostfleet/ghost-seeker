package opauth

import (
	"encoding/json"
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

func TestLoadOrCreateReviewLinkKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "review-link.key")
	key, err := LoadOrCreateReviewLinkKey(path)
	if err != nil || len(key) != 64 {
		t.Fatalf("generated review key = %q, %v", key, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("review key mode = %v, %v", info, err)
	}
	loaded, err := LoadOrCreateReviewLinkKey(path)
	if err != nil || loaded != key {
		t.Fatalf("loaded review key = %q, %v; want same as generated", loaded, err)
	}
	if _, err := LoadOrCreateReviewLinkKey(""); err == nil {
		t.Fatal("empty review key path accepted")
	}
}

func TestRequireBearer(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := RequireBearer("secret-token-12345", ok)
	for _, tc := range []struct {
		auth string
		want int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Basic secret-token-12345", http.StatusUnauthorized},
		{"Bearer secret-token-12345", http.StatusNoContent},
		{"bearer secret-token-12345", http.StatusNoContent},
	} {
		r := httptest.NewRequest(http.MethodPost, "/ghostseeker.v1.ConsoleService/SetPolicy", nil)
		if tc.auth != "" {
			r.Header.Set("Authorization", tc.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("auth %q: %d want %d", tc.auth, w.Code, tc.want)
		}
		if tc.want == http.StatusUnauthorized && (w.Header().Get("WWW-Authenticate") == "" || !strings.Contains(w.Body.String(), "unauthenticated")) {
			t.Errorf("401 shape: %v %s", w.Header(), w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/blob/x/full", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight should pass through: %d", w.Code)
	}
}

func newApp(dev bool) (*Sessions, http.Handler) {
	s := NewSessions("secret-token-12345", dev)
	return s, s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app:" + r.URL.Path)) }))
}

func do(h http.Handler, method, target, remote string, hdr map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var body *strings.Reader
	if method == http.MethodPost && strings.HasPrefix(target, "/login") {
		body = strings.NewReader(hdr["form"])
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	r.Host = "localhost:8090"
	r.RemoteAddr = remote
	for k, v := range hdr {
		if k != "form" {
			r.Header.Set(k, v)
		}
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSessionsProductionFlow(t *testing.T) {
	_, h := newApp(false)
	// Unauthenticated API/blob -> 401 JSON; page -> redirect to /login; even from loopback.
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "127.0.0.1:5", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("api: %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/blob/x/full", "127.0.0.1:5", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("blob: %d", w.Code)
	}
	if w := do(h, http.MethodPost, "/api/shore/policy", "127.0.0.1:5", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("post: %d", w.Code)
	}
	w := do(h, http.MethodGet, "/cdr", "10.0.0.9:5", nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login?next=%2Fcdr" {
		t.Fatalf("page redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, http.MethodGet, "/login", "10.0.0.9:5", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="token"`) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatalf("login page: %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/login?next=%2Fcdr", "10.0.0.9:5", nil); !strings.Contains(w.Body.String(), `name="next" value="/cdr"`) {
		t.Fatalf("login page must carry next: %s", w.Body.String())
	}
	if w := do(h, http.MethodGet, "/login?next=//evil", "10.0.0.9:5", nil); strings.Contains(w.Body.String(), `name="next"`) {
		t.Fatalf("unsafe next must be dropped")
	}
	if w := do(h, http.MethodGet, "/api/auth", "10.0.0.9:5", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatalf("auth status: %d %s", w.Code, w.Body.String())
	}
	// Wrong token -> 401, no cookie.
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "form": url.Values{"token": {"nope"}}.Encode()}
	if w := do(h, http.MethodPost, "/login", "10.0.0.9:5", form); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("bad login: %d %v", w.Code, w.Result().Cookies())
	}
	// Right token -> HttpOnly SameSite=Strict cookie, redirect to safe next only.
	form["form"] = url.Values{"token": {"secret-token-12345"}, "next": {"//evil.example/x"}}.Encode()
	w = do(h, http.MethodPost, "/login", "10.0.0.9:5", form)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("login: %d %q", w.Code, w.Header().Get("Location"))
	}
	form["form"] = url.Values{"token": {"secret-token-12345"}, "next": {"/cdr"}}.Encode()
	if w := do(h, http.MethodPost, "/login", "10.0.0.9:5", form); w.Header().Get("Location") != "/cdr" {
		t.Fatalf("login must honor next: %q", w.Header().Get("Location"))
	}
	form["form"] = url.Values{"token": {"wrong"}, "next": {"/cdr"}}.Encode()
	if w := do(h, http.MethodPost, "/login", "10.0.0.9:5", form); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `name="next" value="/cdr"`) {
		t.Fatalf("failed login must keep next: %d", w.Code)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != CookieName || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteStrictMode || cs[0].Path != "/" || cs[0].Secure {
		t.Fatalf("cookie attrs: %+v", cs)
	}
	c := &http.Cookie{Name: CookieName, Value: cs[0].Value}
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "10.0.0.9:5", nil, c); w.Code != http.StatusOK || w.Body.String() != "app:/api/shore/snapshot" {
		t.Fatalf("with cookie: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "10.0.0.9:5", nil, &http.Cookie{Name: CookieName, Value: "forged"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie: %d", w.Code)
	}
	// Bearer works for scripts; TLS marks the cookie Secure.
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "10.0.0.9:5", map[string]string{"Authorization": "Bearer secret-token-12345"}); w.Code != http.StatusOK {
		t.Fatalf("bearer: %d", w.Code)
	}
	form["form"] = url.Values{"token": {"secret-token-12345"}}.Encode()
	form["X-Forwarded-Proto"] = "https"
	if cs := do(h, http.MethodPost, "/login", "10.0.0.9:5", form).Result().Cookies(); len(cs) != 1 || !cs[0].Secure {
		t.Fatalf("https cookie not Secure: %+v", cs)
	}
	// Logout invalidates the server-side session.
	if w := do(h, http.MethodPost, "/logout", "10.0.0.9:5", nil, c); w.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "10.0.0.9:5", nil, c); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", w.Code)
	}
}

func TestReviewJoinLinkIssuesReviewerSession(t *testing.T) {
	s := NewSessions("secret-token-12345", false).WithReviewJoinKey("review-link-secret")
	h := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok || p.Role != "reviewer" || p.Mode != "token" {
			t.Errorf("principal = %+v, present = %v", p, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	joined := do(h, http.MethodGet, "/join/review-link-secret", "10.0.0.9:5", nil)
	cookies := joined.Result().Cookies()
	if joined.Code != http.StatusOK || !strings.Contains(joined.Body.String(), `content="0;url=/#review"`) || joined.Header().Get("Location") != "" {
		t.Fatalf("join = %d, %q, %q", joined.Code, joined.Header().Get("Location"), joined.Body.String())
	}
	if joined.Header().Get("Cache-Control") != "no-store" || joined.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("join headers = %#v", joined.Header())
	}
	if len(cookies) != 1 || cookies[0].Name != CookieName || !cookies[0].HttpOnly {
		t.Fatalf("join cookies = %+v", cookies)
	}
	cookie := cookies[0]
	auth := do(h, http.MethodGet, "/api/auth", "10.0.0.9:5", nil, cookie)
	var identity struct {
		Authenticated bool   `json:"authenticated"`
		Mode          string `json:"mode"`
		Role          string `json:"role"`
	}
	if err := json.Unmarshal(auth.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if !identity.Authenticated || identity.Mode != "token" || identity.Role != "reviewer" {
		t.Fatalf("reviewer auth identity = %+v", identity)
	}
	if stream := do(h, http.MethodGet, "/api/stream", "10.0.0.9:5", nil, cookie); stream.Code != http.StatusNoContent {
		t.Fatalf("reviewer stream = %d", stream.Code)
	}
}

func TestReviewJoinWrongKeyAndDisabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	enabled := NewSessions("secret-token-12345", false).WithReviewJoinKey("review-link-secret").Wrap(next)
	wrong := do(enabled, http.MethodGet, "/join/wrong-key", "10.0.0.9:5", nil)
	if wrong.Code != http.StatusNotFound || len(wrong.Result().Cookies()) != 0 {
		t.Fatalf("wrong key = %d, cookies %+v", wrong.Code, wrong.Result().Cookies())
	}

	disabled := NewSessions("secret-token-12345", false).Wrap(next)
	ordinary := do(disabled, http.MethodGet, "/join/anything", "10.0.0.9:5", nil)
	if ordinary.Code != http.StatusFound || ordinary.Header().Get("Location") != "/login?next=%2Fjoin%2Fanything" {
		t.Fatalf("disabled join path = %d %q", ordinary.Code, ordinary.Header().Get("Location"))
	}
	if len(ordinary.Result().Cookies()) != 0 {
		t.Fatalf("disabled join issued cookies: %+v", ordinary.Result().Cookies())
	}
}

func TestReviewerMethodAllowlistAndOperatorAccess(t *testing.T) {
	s := NewSessions("secret-token-12345", false).WithReviewJoinKey("review-link-secret")
	h := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			t.Errorf("no principal for %s %s", r.Method, r.URL.Path)
		}
		wantRole := "reviewer"
		if r.URL.Path == "/api/directives" {
			wantRole = "operator"
		}
		if p.Role != wantRole || p.Mode != "token" {
			t.Errorf("principal = %+v, want role %s", p, wantRole)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	joined := do(h, http.MethodGet, "/join/review-link-secret", "10.0.0.9:5", nil)
	cookie := joined.Result().Cookies()[0]

	for _, path := range []string{
		"/api/items/x/review",
		"/api/items/x%2Fy/review",
		"/api/reviews/r/reason",
		"/api/reviews/r/undo",
	} {
		if w := do(h, http.MethodPost, path, "10.0.0.9:5", nil, cookie); w.Code != http.StatusNoContent {
			t.Errorf("reviewer POST %s = %d: %s", path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/model"},
		{http.MethodPost, "/api/directives"},
		{http.MethodPost, "/api/items/x/upload-override"},
		{http.MethodPost, "/api/items/x/decision"},
		{http.MethodPost, "/api/review-sync/proposals"},
		{http.MethodPost, "/api/auth"},
		{http.MethodPost, "/login"},
	} {
		w := do(h, tc.method, tc.path, "10.0.0.9:5", nil, cookie)
		if w.Code != http.StatusForbidden || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("reviewer %s %s = %d, %s", tc.method, tc.path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := do(h, http.MethodHead, "/api/stream", "10.0.0.9:5", nil, cookie); w.Code != http.StatusNoContent {
		t.Errorf("reviewer HEAD stream = %d", w.Code)
	}
	if w := do(h, http.MethodPost, "/logout", "10.0.0.9:5", nil, cookie); w.Code != http.StatusSeeOther {
		t.Errorf("reviewer logout = %d", w.Code)
	}
	if w := do(h, http.MethodPost, "/api/directives", "10.0.0.9:5", map[string]string{"Authorization": "Bearer secret-token-12345"}); w.Code != http.StatusNoContent {
		t.Errorf("operator POST directives = %d: %s", w.Code, w.Body.String())
	}
	form := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"form":         url.Values{"token": {"secret-token-12345"}}.Encode(),
	}
	operatorLogin := do(h, http.MethodPost, "/login", "10.0.0.9:5", form)
	operatorCookies := operatorLogin.Result().Cookies()
	if operatorLogin.Code != http.StatusSeeOther || len(operatorCookies) != 1 {
		t.Fatalf("operator login = %d, cookies %+v", operatorLogin.Code, operatorCookies)
	}
	if w := do(h, http.MethodPost, "/api/directives", "10.0.0.9:5", nil, operatorCookies[0]); w.Code != http.StatusNoContent {
		t.Errorf("operator cookie POST directives = %d: %s", w.Code, w.Body.String())
	}
	if w := do(h, http.MethodGet, "/api/auth", "10.0.0.9:5", nil, operatorCookies[0]); !strings.Contains(w.Body.String(), `"role":"operator"`) {
		t.Errorf("operator cookie identity = %s", w.Body.String())
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

func TestSessionsDevLoopbackAutoLogin(t *testing.T) {
	_, h := newApp(true)
	w := do(h, http.MethodGet, "/api/shore/snapshot", "127.0.0.1:4242", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("loopback dev should auto-login: %d", w.Code)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("auto cookie: %+v", cs)
	}
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "[::1]:4242", nil); w.Code != http.StatusOK {
		t.Fatalf("ipv6 loopback: %d", w.Code)
	}
	// Dev mode still requires the token from anything that is not loopback, or behind a proxy.
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "192.168.1.20:4242", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("lan dev: %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/api/shore/snapshot", "127.0.0.1:4242", map[string]string{"X-Forwarded-For": "192.168.1.20"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("proxied dev: %d", w.Code)
	}
	// A same-box reverse proxy serving a public name is not loopback either.
	r := httptest.NewRequest(http.MethodGet, "http://ops.ship.mil/api/shore/snapshot", nil)
	r.RemoteAddr = "127.0.0.1:4242"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("public Host via loopback proxy: %d", w.Code)
	}
	for _, host := range []string{"localhost:8090", "127.0.0.1:8090", "[::1]:8090", "app.localhost"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/shore/snapshot", nil)
		r.RemoteAddr = "[::1]:7"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("host %s: %d", host, w.Code)
		}
	}
	if w := do(h, http.MethodGet, "/login", "127.0.0.1:1", nil); w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("dev /login should bounce home: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestDevLoopbackSessionRoleRemainsOperator(t *testing.T) {
	s := NewSessions("secret-token-12345", true)
	h := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok || p.Role != "operator" || p.Mode != "token" {
			t.Errorf("loopback principal = %+v, present = %v", p, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	w := do(h, http.MethodGet, "/api/stream", "127.0.0.1:4242", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("loopback request = %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("loopback cookies = %+v", cookies)
	}
	auth := do(h, http.MethodGet, "/api/auth", "10.0.0.9:5", nil, cookies[0])
	if !strings.Contains(auth.Body.String(), `"role":"operator"`) {
		t.Fatalf("loopback session identity = %s", auth.Body.String())
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

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/cdr": "/cdr", "//evil": "/", "/\\evil": "/", "http://evil": "/", "/login?next=/": "/", "/cdr\t": "/", "/x\ny": "/", "/a b": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q want %q", in, got, want)
		}
	}
}
