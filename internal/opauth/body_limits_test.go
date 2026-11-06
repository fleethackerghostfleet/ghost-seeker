package opauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Fatal("unauthenticated body was read")
	return 0, nil
}

func TestAuthenticationRejectsBeforeReadingProtectedBody(t *testing.T) {
	for _, wrap := range []func(http.Handler) http.Handler{
		NewSessions("test-token", false).Wrap,
		func(h http.Handler) http.Handler { return RequireBearer("test-token", h) },
	} {
		w := httptest.NewRecorder()
		h := wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler reached") }))
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/directives", unreadBody{t}))
		if w.Code != 401 {
			t.Fatalf("status=%d", w.Code)
		}
	}
}

func TestLoginRejectsOversizedFormBeforeIssuingSession(t *testing.T) {
	h := NewSessions("test-token", false).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler reached") }))
	for _, body := range []string{"token=" + strings.Repeat("x", 4<<10), "token=test-token&next=" + strings.Repeat("x", 4<<10)} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(w, r)
		if w.Code != 413 || len(w.Result().Cookies()) != 0 {
			t.Fatalf("status=%d cookies=%v", w.Code, w.Result().Cookies())
		}
	}
}
