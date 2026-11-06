package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestCompileInterpretFallbackDoesNotReportMatchedWordsUnknown(t *testing.T) {
	bridge := New("http://127.0.0.1:0", "", nil)
	bridge.Compiler = &command.Compiler{}
	handler := NewConsole(bridge).Handler()
	for text, label := range map[string]string{"prioritize sailboats": "sailboat", "prioritize marine mammals": "marine mammal"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/directives/compile", strings.NewReader(`{"text":"`+text+`"}`)))
		var got struct {
			Rules   []command.Rule
			Unknown []string
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Rules) != 1 || len(got.Rules[0].Classes) != 1 || got.Rules[0].Classes[0] != label {
			t.Fatalf("%q: rules = %+v, want boost %q", text, got.Rules, label)
		}
		if len(got.Unknown) != 0 {
			t.Fatalf("%q: unknown = %v, want none once interpreted", text, got.Unknown)
		}
	}
}
