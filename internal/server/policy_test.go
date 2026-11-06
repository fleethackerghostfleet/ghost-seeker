package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

type failingSaveBackend struct {
	policy.Backend
}

func TestPolicyBurstAdminRestoreAndExpiry(t *testing.T) {
	backend := policy.NewMemBackend()
	data := store.New(10)
	app := New(data, WithAdmin(true), WithAdminToken("test-token"), WithPolicyBackend(backend))
	raw := `{"layers":[{"echelon":"ECHELON_COMMAND","issuedBy":"CTF-99","emcon":{"mode":"EMCON_MODE_BURST"}}]}`
	response := rawPolicyRequest(app, http.MethodPut, "/api/policy", raw)
	if response.Code != http.StatusOK {
		t.Fatalf("admin status=%d: %s", response.Code, response.Body.String())
	}
	if stats, _ := data.Stats(); !stats.MetaOnly || stats.MetaOnlyReason != "emcon" {
		t.Fatalf("admin BURST did not reach store: %+v", stats)
	}
	restored := store.New(10)
	rt := newPolicyRuntime(serverOptions{backend: backend}, restored)
	if stats, _ := restored.Stats(); !stats.MetaOnly || stats.MetaOnlyReason != "emcon" {
		t.Fatalf("restored BURST did not reach store: %+v", stats)
	}
	rt.expire(time.Now().Add(13 * time.Hour))
	if stats, _ := restored.Stats(); stats.MetaOnly {
		t.Fatalf("expired BURST remains active: %+v", stats)
	}
}

func (failingSaveBackend) SavePolicyState(context.Context, *pb.Policy, int64) error {
	return errors.New("save failed")
}

func TestAdminPolicyApplyFailureRestoresHigherLayers(t *testing.T) {
	app := New(store.New(10), WithAdmin(true), WithAdminToken("test-token"), WithNodeID("DDG-51"),
		WithPolicyBackend(failingSaveBackend{Backend: policy.NewMemBackend()}))
	previous := app.policy.higherLayers
	if len(previous) == 0 {
		t.Fatal("expected seeded higher policy layer")
	}
	raw, err := protojson.Marshal(&pb.Policy{Layers: []*pb.PolicyLayer{{
		Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-99",
		Rules: []*pb.InterestRule{{Id: "new-lock", Locked: true, BoostLabels: []string{"aircraft"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	response := rawPolicyRequest(app, http.MethodPut, "/api/policy", string(raw))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("PUT status=%d body=%s", response.Code, response.Body.String())
	}
	if len(app.policy.higherLayers) != len(previous) || app.policy.higherLayers[0] != previous[0] {
		t.Fatalf("higher layers changed after failure: got=%v want=%v", app.policy.higherLayers, previous)
	}
}

func TestSimulationPolicySeedLocksAndPartialConflict(t *testing.T) {
	data := store.New(10)
	app := New(data, WithAdmin(true), WithAdminToken("test-token"))
	app.SetCompiler(&command.Compiler{Locks: app.PolicyLocks()})

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
	if !strings.Contains(response.Body.String(), "Locked by CTF-72") {
		t.Fatalf("fully blocked response omitted lock reason: %s", response.Body.String())
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

func TestCompileDirectiveReportsActivePolicyLocks(t *testing.T) {
	app := New(store.New(10), WithAdmin(true), WithAdminToken("test-token"))
	app.SetCompiler(&command.Compiler{Locks: app.PolicyLocks()})

	response := request(app, http.MethodPost, "/api/directives/compile", map[string]string{"text": "ignore submarines"})
	if response.Code != http.StatusOK {
		t.Fatalf("locked compile status = %d: %s", response.Code, response.Body.String())
	}
	var compiled command.Compiled
	if err := json.Unmarshal(response.Body.Bytes(), &compiled); err != nil {
		t.Fatal(err)
	}
	if !compiled.Blocked || !strings.Contains(strings.Join(compiled.Warnings, "\n"), "Locked by CTF-72") {
		t.Fatalf("locked compile = %+v, want CTF-72 blocked warning", compiled)
	}
	if !strings.Contains(compiled.Readback, "Suppress: submarine") {
		t.Fatalf("locked compile lost readback: %+v", compiled)
	}

	response = request(app, http.MethodPost, "/api/directives/compile", map[string]string{"text": "prioritize tankers"})
	if response.Code != http.StatusOK {
		t.Fatalf("delegated compile status = %d: %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Blocked || strings.Contains(strings.Join(compiled.Warnings, "\n"), "Locked by") {
		t.Fatalf("delegated tanker compile = %+v, want unblocked without lock warning", compiled)
	}
}

func TestCompileDirectiveIgnoresExpiredPolicyLocks(t *testing.T) {
	app := New(store.New(10))
	now := time.Now()
	app.policy.higherLayers = []*pb.PolicyLayer{{
		Echelon:  pb.Echelon_ECHELON_COMMAND,
		IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{
			Id: "expired-submarine", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3,
			ExpiresAtUnixMs: now.Add(-time.Minute).UnixMilli(),
		}},
	}}
	if _, err := app.policy.apply("test", "CTF-72", "Expired lock", now); err != nil {
		t.Fatalf("apply expired policy: %v", err)
	}
	active, _ := app.policy.guard.Active(now)
	if active == nil {
		t.Fatal("expected active policy with expired rule")
	}
	_, wouldBlock := policy.CheckCommandRules(policy.Locks(active.Layers), pb.Echelon_ECHELON_SHIP, []policy.RuleLabels{{
		Type: "suppress", Classes: []string{"submarine"},
	}})
	if !wouldBlock {
		t.Fatal("expired lock fixture would not block while included")
	}

	app.SetCompiler(&command.Compiler{Locks: app.PolicyLocks()})
	response := request(app, http.MethodPost, "/api/directives/compile", map[string]string{"text": "ignore submarines"})
	if response.Code != http.StatusOK {
		t.Fatalf("compile status=%d: %s", response.Code, response.Body.String())
	}
	var compiled command.Compiled
	if err := json.Unmarshal(response.Body.Bytes(), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Blocked || strings.Contains(strings.Join(compiled.Warnings, "\n"), "Locked by") {
		t.Fatalf("expired lock blocked directive: %+v", compiled)
	}
}

func TestSimulationPolicyExpiryClearsLocksAndSourceDirectives(t *testing.T) {
	data := store.New(10)
	app := New(data, WithAdmin(true), WithAdminToken("test-token"))
	input := map[string]any{"text": "ignore submarines", "rules": []command.Rule{
		{Type: "suppress", Classes: []string{"submarine"}},
	}}
	if response := request(app, http.MethodPost, "/api/directives", input); response.Code != http.StatusConflict {
		t.Fatalf("pre-expiry directive status=%d body=%s", response.Code, response.Body.String())
	}

	app.policy.expire(time.Now().Add(13 * time.Hour))
	for _, directive := range data.Directives() {
		if directive.Source == "CTF-72" {
			t.Fatalf("expired source directive remains: %+v", directive)
		}
	}
	audit, err := app.policy.guard.Audit(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	foundExpired := false
	for _, entry := range audit {
		foundExpired = foundExpired || entry.Result == "expired"
	}
	if !foundExpired {
		t.Fatalf("expired audit entry missing: %+v", audit)
	}
	if response := request(app, http.MethodPost, "/api/directives", input); response.Code != http.StatusCreated {
		t.Fatalf("post-expiry directive status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRestoreOperatorLayerSkipsScopedRules(t *testing.T) {
	data := store.New(10)
	rt := &policyRuntime{store: data}
	rt.restoreOperatorLayer(&pb.PolicyLayer{Rules: []*pb.InterestRule{
		{Id: "directive:0", BoostLabels: []string{"tanker"}},
		{Id: "directive:1:scoped", BoostLabels: []string{"submarine"}},
	}})
	directives := data.Directives()
	if len(directives) != 1 || len(directives[0].Rules) != 1 ||
		len(directives[0].Rules[0].Classes) != 1 || directives[0].Rules[0].Classes[0] != "tanker" {
		t.Fatalf("restored directives=%+v", directives)
	}
}

func TestRestoreSyncsConflictsAgainstRebuiltOperatorRules(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	backend, err := policy.NewFileBackend(filepath.Join(t.TempDir(), "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := policy.NewStore(ctx, backend, policy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Accept(ctx, &pb.Policy{
		Id: "persisted", Version: 1, IssuedAtUnixMs: now.UnixMilli(),
		Layers: []*pb.PolicyLayer{
			{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72", Rules: []*pb.InterestRule{
				{Id: "ctf72-submarine", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3},
			}},
			{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Rules: []*pb.InterestRule{
				{Id: "directive:0:scoped", BoostLabels: []string{"tanker"}},
				{Id: "directive:1", SuppressLabels: []string{"submarine"}},
			}},
		},
	}, "console", now)
	if err != nil {
		t.Fatal(err)
	}

	data := store.New(10)
	rt := newPolicyRuntime(serverOptions{backend: backend}, data)
	if rt.err != nil {
		t.Fatal(rt.err)
	}
	directives := data.Directives()
	var operator *command.Directive
	var sourceDirectives []command.Directive
	for i := range directives {
		if directives[i].ID == "directive" {
			operator = &directives[i]
		}
		if directives[i].Source != "" {
			sourceDirectives = append(sourceDirectives, directives[i])
		}
	}
	if operator == nil || len(operator.Rules) != 1 || operator.Blocked == "" {
		t.Fatalf("restored operator directive=%+v", operator)
	}
	if got, want := command.EffectiveWeights("maritime", directives), command.EffectiveWeights("maritime", sourceDirectives); !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked restored rule changed effective weights: got=%v want=%v", got, want)
	}
}

func TestPartialHigherPolicyConflictKeepsUnblockedRuleEffective(t *testing.T) {
	data := store.New(10)
	data.RestoreDirectives([]command.Directive{{
		ID: "operator", Rules: []command.Rule{
			{Type: "suppress", Classes: []string{"submarine"}},
			{Type: "boost", Classes: []string{"tanker"}, Weight: 2},
		},
	}})
	rt := &policyRuntime{store: data, nodeID: "DDG-51"}
	rt.syncStore(&pb.Policy{Layers: []*pb.PolicyLayer{
		{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72", Rules: []*pb.InterestRule{
			{Id: "locked-submarine", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3},
		}},
		{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Rules: []*pb.InterestRule{
			{Id: "operator:0", SuppressLabels: []string{"submarine"}},
			{Id: "operator:1", BoostLabels: []string{"tanker"}, Weight: 2},
		}},
	}}, time.Now())
	directives := data.Directives()
	var operator *command.Directive
	for i := range directives {
		if directives[i].ID == "operator" {
			operator = &directives[i]
			break
		}
	}
	if operator == nil || operator.Blocked != "" ||
		operator.Rules[0].Blocked == "" || operator.Rules[1].Blocked != "" {
		t.Fatalf("partial conflict state=%+v", directives)
	}
	weights := command.EffectiveWeights("maritime", directives)
	if weights["tanker"].Effective <= weights["tanker"].Base {
		t.Fatalf("unblocked tanker rule did not affect weights: %+v", weights["tanker"])
	}
}

func TestSimulationDelegatedTankerRuleCanBeOverridden(t *testing.T) {
	data := store.New(10)
	app := New(data, WithAdmin(true), WithAdminToken("test-token"))
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
	} {
		response := request(denied, endpoint.method, endpoint.path, endpoint.body)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want %d", endpoint.method, endpoint.path, response.Code, http.StatusForbidden)
		}
	}
	response := request(denied, http.MethodGet, "/api/policy/audit", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET audit without admin status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}

	app := New(store.New(10), WithAdmin(true), WithAdminToken("test-token"))
	response = rawPolicyRequest(app, http.MethodPost, "/api/policy/validate", `{}`)
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
	app := New(data, WithAdmin(true), WithAdminToken("test-token"))
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
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAdminPolicyEditsRequireOperatorBearer(t *testing.T) {
	app := New(store.New(10), WithAdmin(true), WithAdminToken("test-token"))
	for _, auth := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest(http.MethodPut, "/api/policy", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		if auth != "" {
			request.Header.Set("Authorization", auth)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("auth=%q status=%d, want 401: %s", auth, response.Code, response.Body.String())
		}
	}
	if response := rawPolicyRequest(app, http.MethodPost, "/api/policy/validate", `{}`); response.Code == http.StatusUnauthorized {
		t.Fatalf("valid bearer rejected: %s", response.Body.String())
	}
	noToken := New(store.New(10), WithAdmin(true))
	if response := rawPolicyRequest(noToken, http.MethodPut, "/api/policy", `{}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("admin without a configured token must refuse edits: %d", response.Code)
	}
}
