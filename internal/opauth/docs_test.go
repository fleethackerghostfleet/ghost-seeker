package opauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
)

func TestAuthenticationPagesIncludeDocumentation(t *testing.T) {
	for name, render := range map[string]func(http.ResponseWriter){
		"token":             func(w http.ResponseWriter) { new(Sessions).page(w, "", "/") },
		"token error":       func(w http.ResponseWriter) { new(Sessions).page(w, "Invalid token", "/cdr") },
		"certificate":       func(w http.ResponseWriter) { certPage(w, http.StatusOK, "Authenticated", "/") },
		"certificate error": func(w http.ResponseWriter) { certPage(w, http.StatusForbidden, "Access denied", "") },
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			render(w)
			body := w.Body.String()
			start, end := strings.Index(body, "<header>"), strings.Index(body, "</header>")
			if start < 0 || end < start || !strings.Contains(body[start:end], httpx.DocsLinkHTML) {
				t.Fatal("documentation link missing from authentication header")
			}
			if !strings.Contains(body, httpx.DocsLinkCSS) {
				t.Fatal("documentation styling missing")
			}
		})
	}
}
