package opauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoginInvalidatesOnlyPresentedSession(t *testing.T) {
	const credential = "synthetic-operator-credential"
	s := NewSessions(credential, false)
	h := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	login := func(previous *http.Cookie, token string, want int) *http.Cookie {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"token": {token}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if previous != nil {
			r.AddCookie(previous)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("login status=%d want=%d", w.Code, want)
		}
		cookies := w.Result().Cookies()
		if want != http.StatusSeeOther {
			if len(cookies) != 0 {
				t.Fatal("failed login issued a cookie")
			}
			return nil
		}
		if len(cookies) != 1 {
			t.Fatal("login did not issue exactly one cookie")
		}
		return cookies[0]
	}
	check := func(cookie *http.Cookie, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/protected", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("protected status=%d want=%d", w.Code, want)
		}
	}
	previous := login(nil, credential, http.StatusSeeOther)
	otherDevice := login(nil, credential, http.StatusSeeOther)
	for _, invalid := range []string{"", "wrong-credential"} {
		login(previous, invalid, http.StatusUnauthorized)
		check(previous, http.StatusNoContent)
	}
	current := login(previous, credential, http.StatusSeeOther)
	if previous.Value == current.Value {
		t.Fatal("session ID did not rotate")
	}
	check(previous, http.StatusUnauthorized)
	check(current, http.StatusNoContent)
	check(otherDevice, http.StatusNoContent)
	logout := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logout.AddCookie(current)
	h.ServeHTTP(httptest.NewRecorder(), logout)
	check(current, http.StatusUnauthorized)
	check(previous, http.StatusUnauthorized)
	check(otherDevice, http.StatusNoContent)
}
