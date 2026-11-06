package opauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
)

func TestTokenAuthMode(t *testing.T) {
	s := opauth.NewSessions("synthetic-token", false)
	req := httptest.NewRequest("GET", "/api/auth", nil)
	req.Header.Set("Authorization", "Bearer synthetic-token")
	w := httptest.NewRecorder()
	s.Wrap(http.NotFoundHandler()).ServeHTTP(w, req)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["mode"] != "token" || body["role"] != "operator" || body["authenticated"] != true || body["id"] != "" {
		t.Fatal(body)
	}
}
