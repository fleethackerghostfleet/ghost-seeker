package httpx

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticUIFallbackIncludesDocumentation(t *testing.T) {
	mux := http.NewServeMux()
	StaticUI(mux, filepath.Join(t.TempDir(), "missing"), "edge")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), DocsLinkHTML) {
		t.Fatal("fallback console missing documentation link")
	}
}
