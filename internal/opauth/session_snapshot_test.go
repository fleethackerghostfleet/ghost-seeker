package opauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type logoutOnHeader struct {
	*httptest.ResponseRecorder
	once   sync.Once
	logout func()
}

func (w *logoutOnHeader) Header() http.Header {
	w.once.Do(w.logout)
	return w.ResponseRecorder.Header()
}

func TestReviewerSnapshotSurvivesConcurrentLogout(t *testing.T) {
	s := NewSessions("synthetic-operator-credential", false).WithReviewJoinKey("synthetic-review-key")
	h := s.Wrap(http.NotFoundHandler())
	join := httptest.NewRecorder()
	h.ServeHTTP(join, httptest.NewRequest(http.MethodGet, "/join/synthetic-review-key", nil))
	if len(join.Result().Cookies()) != 1 {
		t.Fatal("reviewer join failed")
	}
	cookie := join.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/api/auth", nil)
	r.AddCookie(cookie)
	w := &logoutOnHeader{ResponseRecorder: httptest.NewRecorder(), logout: func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			logout := httptest.NewRequest(http.MethodPost, "/logout", nil)
			logout.AddCookie(cookie)
			h.ServeHTTP(httptest.NewRecorder(), logout)
		}()
		<-done
	}}
	h.ServeHTTP(w, r)
	var response struct {
		Authenticated bool   `json:"authenticated"`
		Role          string `json:"role"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Authenticated || response.Role != "reviewer" {
		t.Fatalf("identity=%+v", response)
	}
	write := httptest.NewRequest(http.MethodPost, "/api/directives", nil)
	write.AddCookie(cookie)
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, write)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out write status=%d", denied.Code)
	}
}

func TestSessionMissingRoleFailsClosed(t *testing.T) {
	for _, missing := range []bool{false, true} {
		s := NewSessions("synthetic-operator-credential", false)
		issued := httptest.NewRecorder()
		s.issue(issued, httptest.NewRequest(http.MethodGet, "/", nil))
		cookie := issued.Result().Cookies()[0]
		s.mu.Lock()
		if missing {
			delete(s.bindings, cookie.Value)
		} else {
			binding := s.bindings[cookie.Value]
			binding.Role = ""
			s.bindings[cookie.Value] = binding
		}
		s.mu.Unlock()
		h := s.Wrap(http.NotFoundHandler())
		r := httptest.NewRequest(http.MethodGet, "/api/auth", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var response struct {
			Authenticated bool   `json:"authenticated"`
			Role          string `json:"role"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Authenticated || response.Role != "" {
			t.Fatalf("identity=%+v", response)
		}
		write := httptest.NewRequest(http.MethodPost, "/api/directives", nil)
		write.AddCookie(cookie)
		denied := httptest.NewRecorder()
		h.ServeHTTP(denied, write)
		if denied.Code != http.StatusUnauthorized {
			t.Fatalf("unbound write status=%d", denied.Code)
		}
	}
}
