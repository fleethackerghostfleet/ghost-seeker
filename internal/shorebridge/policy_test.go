package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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
		proto.Reset(got)
		proto.Merge(got, req.Policy)
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

func TestSetPolicyDropsScopedRulesBeforeEdgeFlattening(t *testing.T) {
	parsed := command.ParseDirective("ignore cargo ships from EO-P7")
	if len(parsed) != 1 || len(parsed[0].Sources) != 1 || parsed[0].Sources[0] != "EO-P7" {
		t.Fatalf("source phrase parsed as %#v", parsed)
	}
	tests := []struct {
		name         string
		text         string
		wantBoost    []string
		wantSuppress []string
	}{
		{name: "scoped only", text: "ignore cargo ships from EO-P7"},
		{name: "mixed", text: "ignore cargo ships from EO-P7; prioritize submarines", wantBoost: []string{"submarine"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got pb.Policy
			shore := fakeShore(t, &got)
			defer shore.Close()
			b := New(shore.URL, "", nil)
			b.Compiler = &command.Compiler{}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/shore/policy", strings.NewReader(`{"text":"`+test.text+`"}`))
			b.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var resp struct {
				Compiled *command.Compiled `json:"compiled"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Compiled == nil {
				t.Fatalf("no compiled block in %s (%v)", rec.Body, err)
			}
			if resp.Compiled.Path != "rules" {
				t.Fatalf("path = %q", resp.Compiled.Path)
			}
			const warning = "Not applied in shore mode: source, attribute and time scopes are not supported by the edge scorer."
			if !strings.Contains(strings.Join(resp.Compiled.Warnings, "\n"), warning) {
				t.Fatalf("warnings = %v", resp.Compiled.Warnings)
			}
			if strings.Contains(strings.ToLower(resp.Compiled.Readback), "cargo ship") {
				t.Fatalf("readback includes dropped rule: %q", resp.Compiled.Readback)
			}
			if !reflect.DeepEqual(got.Interest.SuppressLabels, test.wantSuppress) ||
				!reflect.DeepEqual(got.Interest.BoostLabels, test.wantBoost) {
				t.Fatalf("edge labels = suppress %v, boost %v", got.Interest.SuppressLabels, got.Interest.BoostLabels)
			}
		})
	}
}

func TestSetPolicyInterpretFallbackOmitsCompiled(t *testing.T) {
	var got pb.Policy
	shore := fakeShore(t, &got)
	defer shore.Close()
	b := New(shore.URL, "", nil)
	b.Compiler = &command.Compiler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/shore/policy", strings.NewReader(`{"text":"ignore sailboats"}`))
	b.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"compiled"`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(got.Interest.SuppressLabels, []string{"sailboat"}) {
		t.Fatalf("interpret suppression labels = %v", got.Interest.SuppressLabels)
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
