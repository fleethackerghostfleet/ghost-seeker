package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSimulationPolicySeedLocksAndPartialConflict(t *testing.T) {
	data := store.New(10)
	app := New(data, WithAdmin(true))

	state := store.State{Directives: data.Directives()}
	lockedSeed := false
	for _, directive := range state.Directives {
		if directive.Source == "CTF-72" && directive.Locked && strings.Contains(directive.ID, "ctf72-submarine") {
			lockedSeed = true
		}
	}
	if !lockedSeed {
		t.Fatal("seed policy did not expose its locked CTF-72 submarine directive")
	}
	response := request(app, http.MethodGet, "/api/policy", nil)
	var policyState struct {
		Summary struct {
			Locks []struct {
				RuleID   string `json:"ruleId"`
				LockedBy string `json:"lockedBy"`
			} `json:"locks"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &policyState); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(policyState.Summary.Locks) != 1 || policyState.Summary.Locks[0].LockedBy != "CTF-72" {
		t.Fatalf("seed policy summary = %#v, status=%d; want CTF-72 lock", policyState.Summary, response.Code)
	}

	response = request(app, http.MethodPost, "/api/directives", map[string]any{
		"text": "submarines and aircraft",
		"rules": []command.Rule{
			{Type: "suppress", Classes: []string{"submarine"}},
			{Type: "boost", Classes: []string{"tanker"}},
		},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("partial-conflict directive status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	var added struct {
		Rules     []command.Rule `json:"rules"`
		Conflicts []struct {
			RuleID string `json:"ruleId"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if len(added.Rules) != 1 || len(added.Rules[0].Classes) != 1 || added.Rules[0].Classes[0] != "tanker" {
		t.Fatalf("accepted rules = %#v, want only aircraft boost", added.Rules)
	}
	if len(added.Conflicts) != 1 {
		t.Fatalf("conflicts = %#v, want one locked-submarine conflict", added.Conflicts)
	}

	response = request(app, http.MethodPost, "/api/directives", map[string]any{
		"text":  "suppress submarines",
		"rules": []command.Rule{{Type: "suppress", Classes: []string{"submarine"}}},
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("fully blocked directive status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}

	response = request(app, http.MethodGet, "/api/policy/audit", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET audit status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var audit struct {
		Entries []struct {
			Result string `json:"result"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	foundRejected := false
	for _, entry := range audit.Entries {
		foundRejected = foundRejected || entry.Result == "rejected"
	}
	if !foundRejected {
		t.Fatalf("audit entries %#v do not include rejected directive", audit.Entries)
	}
}

func TestSimulationDelegatedTankerRuleCanBeOverridden(t *testing.T) {
	data := store.New(10)
	app := New(data, WithAdmin(true))
	response := request(app, http.MethodPost, "/api/directives", map[string]any{
		"text":  "prioritize tankers",
		"rules": []command.Rule{{Type: "boost", Classes: []string{"tanker"}, Weight: 3}},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("POST tanker override status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}

	state := store.State{Directives: data.Directives()}
	foundSource, foundOperator := false, false
	for _, directive := range state.Directives {
		if directive.Source == "CTF-72" && strings.Contains(directive.ID, "ctf72-tanker") {
			foundSource = true
		}
		if directive.Source == "" && directive.Text == "prioritize tankers" && len(directive.Rules) == 1 {
			foundOperator = directive.Rules[0].Classes[0] == "tanker"
		}
	}
	if foundSource || !foundOperator {
		t.Fatalf("delegation source remains=%v operator override=%v, directives=%#v", foundSource, foundOperator, state.Directives)
	}
}

func TestSimulationPolicyAdminValidationAndEMCONLock(t *testing.T) {
	denied := New(store.New(10))
	for _, endpoint := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/policy/validate", map[string]any{}},
		{http.MethodPut, "/api/policy", map[string]any{}},
		{http.MethodGet, "/api/policy/audit", nil},
	} {
		response := request(denied, endpoint.method, endpoint.path, endpoint.body)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want %d", endpoint.method, endpoint.path, response.Code, http.StatusForbidden)
		}
	}

	app := New(store.New(10), WithAdmin(true))
	response := rawPolicyRequest(app, http.MethodPost, "/api/policy/validate", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("valid protojson validation status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	response = rawPolicyRequest(app, http.MethodPost, "/api/policy/validate", `{"layers":`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid protojson status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	response = request(app, http.MethodPost, "/api/policy/emcon", map[string]string{"mode": "BURST"})
	if response.Code != http.StatusOK {
		t.Fatalf("unlocked EMCON update status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}

	raw, err := protojson.Marshal(&pb.Policy{Layers: []*pb.PolicyLayer{{
		Echelon:  pb.Echelon_ECHELON_COMMAND,
		IssuedBy: "CTF-99",
		Emcon: &pb.EmconSetting{
			Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: "CTF-99",
			Echelon: pb.Echelon_ECHELON_COMMAND, Locked: true,
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	response = rawPolicyRequest(app, http.MethodPut, "/api/policy", string(raw))
	if response.Code != http.StatusOK {
		t.Fatalf("PUT locked command policy status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	response = request(app, http.MethodPost, "/api/policy/emcon", map[string]string{"mode": "BURST"})
	if response.Code != http.StatusConflict {
		t.Fatalf("locked EMCON update status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	response = request(app, http.MethodGet, "/api/policy", nil)
	var current struct {
		Summary struct {
			EMCON struct {
				Mode   string `json:"mode"`
				Locked bool   `json:"locked"`
			} `json:"emcon"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Summary.EMCON.Mode != "SILENT" || !current.Summary.EMCON.Locked {
		t.Fatalf("effective EMCON = %#v, want locked SILENT", current.Summary.EMCON)
	}
}

func TestSimulationProtectsSourceDirectivesAndPublishesPolicySummary(t *testing.T) {
	data := store.New(10)
	app := New(data, WithAdmin(true))
	state := store.State{Directives: data.Directives()}
	var sourceID string
	sourceCount := 0
	for _, directive := range state.Directives {
		if directive.Source != "" {
			sourceID = directive.ID
			sourceCount++
		}
	}
	if sourceID == "" {
		t.Fatal("seed policy did not create source directives")
	}
	response := request(app, http.MethodDelete, "/api/directives/"+sourceID, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("DELETE source directive status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	response = request(app, http.MethodDelete, "/api/directives", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE all directives status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	state = store.State{Directives: data.Directives()}
	remaining := 0
	for _, directive := range state.Directives {
		if directive.Source != "" {
			remaining++
		}
	}
	if remaining != sourceCount {
		t.Fatalf("remaining source directives = %d, want %d", remaining, sourceCount)
	}

	ctx, cancel := context.WithCancel(context.Background())
	recorder := &flushCancelWriter{header: make(http.Header), cancel: cancel}
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil).WithContext(ctx)
	app.ServeHTTP(recorder, req)
	if !strings.Contains(recorder.body.String(), `"policy"`) || !strings.Contains(recorder.body.String(), `"nodeId":"DDG-51"`) {
		t.Fatalf("initial stream snapshot omitted policy summary: %s", recorder.body.String())
	}
}

type flushCancelWriter struct {
	header http.Header
	body   bytes.Buffer
	cancel context.CancelFunc
}

func (w *flushCancelWriter) Header() http.Header { return w.header }

func (w *flushCancelWriter) WriteHeader(int) {}

func (w *flushCancelWriter) Write(body []byte) (int, error) { return w.body.Write(body) }

func (w *flushCancelWriter) Flush() { w.cancel() }

func rawPolicyRequest(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
