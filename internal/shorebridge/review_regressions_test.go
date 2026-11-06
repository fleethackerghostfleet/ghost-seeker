package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"google.golang.org/protobuf/proto"
)

func TestUnsupportedRawPolicyPreservesActiveRules(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	b := New(shore.server.URL, "", nil)
	b.Compiler = &command.Compiler{EdgeSubtypes: true}
	handler := b.Handler()
	post := func(text string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy", strings.NewReader(`{"text":"`+text+`"}`)))
		return rec
	}
	if rec := post("ignore birds"); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	before := proto.Clone(shore.stored())
	rec := post("ignore coast guard and unicorns")
	if rec.Code != http.StatusUnprocessableEntity || !proto.Equal(before, shore.stored()) {
		t.Fatalf("unsupported request changed policy: status=%d before=%v after=%v body=%s", rec.Code, before, shore.stored(), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), command.NoMatchReadback) {
		t.Fatalf("missing unsupported explanation: %s", rec.Body.String())
	}
}

func TestScopedRuleKeepsUnsupportedReadback(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	b := New(shore.server.URL, "", nil)
	b.Compiler = &command.Compiler{EdgeSubtypes: true}
	console := NewConsole(b).Handler()
	for _, endpoint := range []string{"/api/directives/preview", "/api/directives/compile", "/api/shore/policy"} {
		rec := httptest.NewRecorder()
		handler := console
		if endpoint == "/api/shore/policy" {
			handler = b.Handler()
		}
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"text":"prioritize unicorns; ignore cargo ships from EO-P7"}`)))
		readback := rec.Body.String()
		if rec.Code == http.StatusOK {
			var result struct{ Readback string }
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			readback = result.Readback
		}
		if !strings.Contains(readback, command.NoMatchReadback) || strings.Contains(readback, "unicorns") {
			t.Errorf("%s lost unsupported readback: status=%d %s", endpoint, rec.Code, rec.Body.String())
		}
	}
}
