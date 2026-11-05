package shorebridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestBridgePresentsOperatorTokenAndStripsCookies(t *testing.T) {
	var gotAuth, gotCookie string
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCookie = r.Header.Get("Authorization"), r.Header.Get("Cookie")
		if r.URL.Path == "/blob/01ITEM/full" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer shore.Close()
	b := New(shore.URL, "", nil).WithToken("operator-token-123")

	if err := b.call(context.Background(), shore.URL, "GetStatus", &pb.GetStatusRequest{}, &pb.GetStatusResponse{}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer operator-token-123" {
		t.Fatalf("console call auth = %q", gotAuth)
	}

	r := httptest.NewRequest(http.MethodGet, "/blob/01ITEM/full", nil)
	r.Header.Set("Authorization", "Bearer browser-should-not-reach-shore")
	r.AddCookie(&http.Cookie{Name: "gs_session", Value: "secret"})
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || gotAuth != "Bearer operator-token-123" || gotCookie != "" {
		t.Fatalf("blob proxy: %d auth=%q cookie=%q", w.Code, gotAuth, gotCookie)
	}
	w = httptest.NewRecorder()
	b.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/blob/01ITEM/full", nil))
	if w.Code != http.StatusMethodNotAllowed && w.Code != http.StatusNotFound {
		t.Fatalf("blob proxy must be GET-only: %d", w.Code)
	}
}
