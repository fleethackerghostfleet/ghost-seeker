package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func fakeShore(t *testing.T, got *pb.Policy) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/SetPolicy") {
			http.NotFound(w, r)
			return
		}
		var req pb.SetPolicyRequest
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		if err := protojson.Unmarshal(body, &req); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		*got = *req.Policy
		raw, _ := protojson.Marshal(&pb.SetPolicyResponse{Policy: req.Policy})
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
}

func TestSetPolicyCompilesWithReadback(t *testing.T) {
	var got pb.Policy
	shore := fakeShore(t, &got)
	defer shore.Close()
	b := New(shore.URL, "", nil)
	b.Compiler = &command.Compiler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/shore/policy", strings.NewReader(`{"text":"ignore cargo ships; prioritize submarines for 30 minutes"}`))
	b.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Compiled *command.Compiled `json:"compiled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Compiled == nil {
		t.Fatalf("no compiled block in %s (%v)", rec.Body, err)
	}
	if resp.Compiled.Path != "rules" || !strings.Contains(resp.Compiled.Readback, "Suppress") {
		t.Fatalf("compiled = %+v", resp.Compiled)
	}
	if len(got.Interest.SuppressLabels) == 0 || len(got.Interest.BoostLabels) == 0 {
		t.Fatalf("labels not forwarded: %v", got.Interest)
	}
	if got.ExpiresAtUnixMs == 0 {
		t.Fatalf("duration not applied")
	}
}

func TestSetPolicyBlockedByGuardrails(t *testing.T) {
	var got pb.Policy
	shore := fakeShore(t, &got)
	defer shore.Close()
	b := New(shore.URL, "", nil)
	b.Compiler = &command.Compiler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/shore/policy", strings.NewReader(`{"text":"prioritize everything"}`))
	b.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Text != "" {
		t.Fatalf("blocked directive reached shore")
	}
}

func TestSetPolicyWithoutCompilerUsesInterpret(t *testing.T) {
	var got pb.Policy
	shore := fakeShore(t, &got)
	defer shore.Close()
	b := New(shore.URL, "", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/shore/policy", strings.NewReader(`{"text":"ignore cargo ships"}`))
	b.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"compiled"`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(got.Interest.SuppressLabels) != 1 {
		t.Fatalf("interpret not used: %v", got.Interest)
	}
}
