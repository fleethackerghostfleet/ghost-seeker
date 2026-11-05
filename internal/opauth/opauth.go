// Package opauth is the shared operator credential for the shore console,
// the edge console and the operator web app: one bearer token, read from a
// flag/env/file like unitauth, or auto-generated into a local dev token file
// so a laptop demo needs no extra steps. Services compare it in constant time
// and answer 401 without it; the web app exchanges it for a browser session.
package opauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

// DefaultDevFile is where the Makefile demo targets (all run from the repo
// root) share the auto-generated token. Set the flag to "none" to disable.
const DefaultDevFile = "data/operator.token"

const minTokenBytes = 16

// Load resolves the operator token. An explicit token (flag/env) or token
// file wins and marks production; otherwise, if devFile is set and not
// "none", the token is read from it or generated there (0600). dev reports
// which path was taken so callers can relax loopback-only conveniences.
func Load(token, tokenFile, devFile string) (tok string, dev bool, err error) {
	tok, err = unitauth.Credential(token, tokenFile)
	if err != nil {
		return "", false, fmt.Errorf("operator token: %w", err)
	}
	if tok != "" {
		if len(tok) < minTokenBytes {
			return "", false, fmt.Errorf("operator token must be at least %d bytes", minTokenBytes)
		}
		return tok, false, nil
	}
	if devFile == "" || strings.EqualFold(devFile, "none") {
		return "", false, errors.New("operator token required: set -operator-token / GS_OPERATOR_TOKEN or -operator-token-file / GS_OPERATOR_TOKEN_FILE (or -dev-token-file for a local demo)")
	}
	tok, err = loadOrCreate(devFile)
	if err != nil {
		return "", false, fmt.Errorf("dev operator token: %w", err)
	}
	return tok, true, nil
}

func loadOrCreate(path string) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(raw)); len(t) >= minTokenBytes {
			return t, nil
		}
		return "", fmt.Errorf("%s: token shorter than %d bytes; delete it to regenerate", path, minTokenBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tok, err := Generate()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", rerr
		}
		return strings.TrimSpace(string(raw)), nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(tok + "\n"); err != nil {
		return "", err
	}
	return tok, nil
}

// Generate returns a fresh 256-bit token as hex.
func Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Equal is a constant-time token comparison.
func Equal(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BearerToken extracts the token from an Authorization: Bearer header.
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// RequireBearer protects an API handler (Connect ConsoleService, /blob/):
// every request must carry Authorization: Bearer <token> or gets a 401 in
// the Connect JSON error shape. CORS preflights pass through so the browser
// can learn that the request itself is not allowed.
func RequireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !Equal(token, BearerToken(r)) {
			Unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Unauthorized writes the 401 every node uses.
func Unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-seeker"`)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "unauthenticated", "message": "operator token required"})
}

// Loopback reports whether the request came straight from this machine
// (no proxy hop recorded), the only case dev auto-login trusts.
func Loopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
