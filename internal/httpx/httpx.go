// Package httpx has the small HTTP plumbing both nodes share: CORS for the
// HTML console, blob serving, static UI, server timeouts.
package httpx

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HostAllowlist is the set of Host names this node answers to.
type HostAllowlist struct{ names map[string]bool }

// NewHostAllowlist builds an allowlist from local names, the listen host and
// any additional public names.
func NewHostAllowlist(listen string, extra []string) *HostAllowlist {
	a := &HostAllowlist{names: make(map[string]bool)}
	add := func(hostport string) {
		if name := normalizeHost(hostport); name != "" {
			a.names[name] = true
		}
	}
	for _, name := range []string{"localhost", "127.0.0.1", "::1"} {
		add(name)
	}
	if host := normalizeHost(listen); host != "" && host != "0.0.0.0" && host != "::" {
		add(listen)
	}
	if hostname, err := os.Hostname(); err == nil {
		add(strings.ToLower(hostname))
	}
	for _, name := range extra {
		add(name)
	}
	return a
}

// Allowed reports whether a Host header value is acceptable.
func (a *HostAllowlist) Allowed(hostport string) bool {
	if hostport == "" {
		return true
	}
	if a == nil {
		a = defaultHostAllowlist
	}
	host := normalizeHost(hostport)
	if net.ParseIP(host) != nil {
		// Rebinding pages send their DNS name as Host; probes and direct-IP clients use IPs.
		return true
	}
	return a.names[host]
}

var defaultHostAllowlist = NewHostAllowlist("", nil)

func normalizeHost(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// CORS is the strict default: only same-origin browsers (and non-browser
// clients, which send no Origin) may call the API. Equivalent to
// CORSAllow(nil, nil).
func CORS(next http.Handler) http.Handler { return CORSAllow(nil, nil, next) }

// CORSAllow guards requests with a Host allowlist before applying the
// cross-origin policy. Same-origin browser requests must also use an
// allowlisted host; -cors-origins may additionally allow exact origins.
func CORSAllow(origins []string, hosts *HostAllowlist, next http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, o := range origins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allow[strings.ToLower(o)] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts.Allowed(r.Host) {
			http.Error(w, "host not allowed", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		originURL, err := url.Parse(origin)
		originHost := ""
		if err == nil {
			originHost = originURL.Host
		}
		ok := origin == "" || allow[strings.ToLower(origin)] || (hosts.Allowed(originHost) && sameOrigin(origin, r))
		site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
		if site == "cross-site" && !allow[strings.ToLower(origin)] {
			ok = false
		}
		if origin != "" && ok {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			if !ok {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !ok && r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin requires scheme and host to match the request's own origin;
// the scheme comes from the connection (or X-Forwarded-Proto behind TLS
// termination).
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	return strings.EqualFold(u.Scheme, requestScheme(r))
}

func requestScheme(r *http.Request) string {
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return strings.ToLower(strings.TrimSpace(strings.Split(p, ",")[0]))
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// SplitList parses a comma/space separated flag value.
func SplitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Server returns an http.Server with the header/idle timeouts and header cap
// every node uses, so a slow or chatty client cannot pin connections.
func Server(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// StaticUI serves dir at / if it exists (the HTML team's subdir), else a
// one-line index pointing at the API.
func StaticUI(mux *http.ServeMux, dir string, apiHint string) {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		mux.Handle("/", http.FileServer(http.Dir(dir)))
		return
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<style>header{display:flex}` + DocsLinkCSS + `</style><header>` + DocsLinkHTML + `</header><pre>ghost-seeker ` + apiHint + "\nAPI: POST /ghostseeker.v1.ConsoleService/&lt;Method&gt; (JSON, Authorization: Bearer &lt;operator token&gt;)\nBlobs: GET /blob/{item_id}/{form}\n</pre>"))
	})
}
