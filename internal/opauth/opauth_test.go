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

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/cdr": "/cdr", "//evil": "/", "/\\evil": "/", "http://evil": "/", "/login?next=/": "/", "/cdr\t": "/", "/x\ny": "/", "/a b": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q want %q", in, got, want)
		}
	}
}
