package httpx

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type prefixResponse struct {
	header     http.Header
	limit      int
	err        error
	written    bytes.Buffer
	calls      int
	status     int
	sentHeader http.Header
}

func (w *prefixResponse) Header() http.Header { return w.header }
func (w *prefixResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.sentHeader = w.header.Clone()
	}
}
func (w *prefixResponse) Write(p []byte) (int, error) {
	w.calls++
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n := min(w.limit, len(p))
	w.written.Write(p[:n])
	return n, w.err
}

func TestServeBlobPreservesSecurityHeadersOnPartialTransportFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		err   error
	}{
		{"complete", 100, nil}, {"partial", 3, io.ErrClosedPipe}, {"failed", 0, io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"frame":12}`)
			w := &prefixResponse{header: make(http.Header), limit: tc.limit, err: tc.err}
			ServeBlob(w, "application/json", body)
			if w.calls != 1 || w.status != http.StatusOK || !bytes.Equal(w.written.Bytes(), body[:min(tc.limit, len(body))]) {
				t.Fatalf("writes=%d status=%d accepted=%q", w.calls, w.status, w.written.Bytes())
			}
			for key, want := range map[string]string{
				"Content-Type": "application/json", "Content-Length": strconv.Itoa(len(body)), "Content-Disposition": "inline",
				"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Content-Security-Policy": "default-src 'none'; sandbox",
			} {
				if got := w.sentHeader.Get(key); got != want {
					t.Errorf("%s=%q want=%q", key, got, want)
				}
			}
		})
	}
}

func TestStaticUIServesOnlyConfiguredRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ui")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("local UI"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := []byte("outside-root-secret")
	secretPath := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secretPath, secret, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(secretPath); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("parent control=%q %v", got, err)
	}
	mux := http.NewServeMux()
	StaticUI(mux, root, "shore")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || w.Body.String() != "local UI" {
		t.Fatalf("UI=%d %q", w.Code, w.Body.String())
	}
	for _, path := range []string{"/secret.txt", "/../secret.txt"} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusMovedPermanently || w.Code == http.StatusTemporaryRedirect {
			if w.Header().Get("Location") != "/secret.txt" {
				t.Fatalf("unexpected redirect=%q", w.Header().Get("Location"))
			}
			if bytes.Contains(w.Body.Bytes(), secret) {
				t.Fatal("outside-root bytes leaked in redirect")
			}
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/secret.txt", nil))
		}
		if w.Code != http.StatusNotFound {
			t.Fatalf("outside root %s=%d", path, w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), secret) {
			t.Fatal("outside-root bytes leaked")
		}
	}
}

func TestStaticUIFallbackAndServerConfiguration(t *testing.T) {
	for _, kind := range []string{"missing", "file"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ui")
			if kind == "file" {
				if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			mux := http.NewServeMux()
			StaticUI(mux, path, "shore-node")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "ghost-seeker shore-node") || w.Header().Get("Content-Type") != "text/html" {
				t.Fatalf("fallback=%d %q", w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing", nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("missing=%d", w.Code)
			}
			s := Server("127.0.0.1:12345", mux)
			if s.Addr != "127.0.0.1:12345" || s.Handler != mux || s.ReadHeaderTimeout != 10*time.Second || s.IdleTimeout != 120*time.Second || s.MaxHeaderBytes != 1<<20 {
				t.Fatalf("server=%+v", s)
			}
		})
	}
}

func TestTLSAndForwardedRequestSchemes(t *testing.T) {
	for _, tc := range []struct{ url, forwarded, want string }{
		{"http://shore.local/", "", "http"}, {"https://shore.local/", "", "https"},
		{"http://shore.local/", " HTTPS , http ", "https"},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.url, nil)
		r.Header.Set("X-Forwarded-Proto", tc.forwarded)
		if got := requestScheme(r); got != tc.want {
			t.Fatalf("scheme=%q want=%q", got, tc.want)
		}
	}
	if got := normalizeHost("[::1]"); got != "::1" {
		t.Fatalf("IPv6=%q", got)
	}
	if !(*HostAllowlist)(nil).Allowed("") {
		t.Fatal("empty host rejected")
	}
}
