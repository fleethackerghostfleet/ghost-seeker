package shorebridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const operatorTokenKey = "shorebridge-operator-test-key-at-least-32"

func mintBridgeOperator(t *testing.T, id string, echelon pb.Echelon) string {
	t.Helper()
	token, err := unitauth.MintOperator(id, echelon, operatorTokenKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestSetOperatorJWTRejectsInvalidClaims(t *testing.T) {
	unknownEchelon := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"TAO-DDG51","role":"operator","echelon":"admiral"}`))
	invalid := []string{"not-a-jwt", "eyJhbGciOiJub25lIn0." + unknownEchelon + ".x"}
	for _, token := range invalid {
		if err := New("", "", nil).SetOperatorJWT(token); err == nil {
			t.Fatalf("accepted invalid operator token %q", token)
		}
	}
}

func writeProtoResponse(t *testing.T, w http.ResponseWriter, message proto.Message) {
	t.Helper()
	raw, err := protojson.Marshal(message)
	if err != nil {
		t.Errorf("marshal response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func TestOperatorJWTReachesShoreAndBearerReachesBoth(t *testing.T) {
	jwtToken := mintBridgeOperator(t, "TAO-DDG51", pb.Echelon_ECHELON_SHIP)
	bearerToken := "opaque-operator-token-123"
	var mu sync.Mutex
	var shoreAuthorizations, shoreJWTs []string
	var edgeAuthorization, edgeJWT string
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		shoreAuthorizations = append(shoreAuthorizations, r.Header.Get("Authorization"))
		shoreJWTs = append(shoreJWTs, r.Header.Get(unitauth.OperatorJWTHeader))
		mu.Unlock()
		switch r.URL.Path {
		case consolePath + "GetStatus":
			writeProtoResponse(t, w, &pb.GetStatusResponse{NodeId: "SHORE-1"})
		case consolePath + "GetPolicy":
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: &pb.Policy{}})
		case consolePath + "ListItems":
			writeProtoResponse(t, w, &pb.ListItemsResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer shore.Close()
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		edgeAuthorization = r.Header.Get("Authorization")
		edgeJWT = r.Header.Get(unitauth.OperatorJWTHeader)
		mu.Unlock()
		if r.URL.Path != consolePath+"GetStatus" {
			http.NotFound(w, r)
			return
		}
		writeProtoResponse(t, w, &pb.GetStatusResponse{NodeId: "EDGE-1"})
	}))
	defer edge.Close()

	bridge := New(shore.URL, edge.URL, nil).WithToken(bearerToken)
	if err := bridge.SetOperatorJWT(jwtToken); err != nil {
		t.Fatal(err)
	}
	bridge.refresh(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(shoreAuthorizations) == 0 {
		t.Fatal("bridge made no shore ConsoleService calls")
	}
	for i, header := range shoreAuthorizations {
		if header != "Bearer "+bearerToken {
			t.Fatalf("shore Authorization=%q, want opaque operator bearer", header)
		}
		if shoreJWTs[i] != jwtToken {
			t.Fatalf("shore %s=%q, want raw operator JWT", unitauth.OperatorJWTHeader, shoreJWTs[i])
		}
	}
	if edgeAuthorization != "Bearer "+bearerToken {
		t.Fatalf("edge Authorization=%q, want opaque operator bearer", edgeAuthorization)
	}
	if edgeJWT != "" {
		t.Fatalf("edge %s=%q, want no operator JWT", unitauth.OperatorJWTHeader, edgeJWT)
	}
}

func TestShoreLockCheckerUsesOperatorEchelon(t *testing.T) {
	active := &pb.Policy{Layers: []*pb.PolicyLayer{{
		Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "locked-submarine", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3}},
	}}}
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != consolePath+"GetPolicy" {
			http.NotFound(w, r)
			return
		}
		writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: active})
	}))
	defer shore.Close()
	rules := []command.Rule{{Type: "suppress", Classes: []string{"submarine"}}}

	shipBridge := New(shore.URL, "", nil)
	if _, blocked := shipBridge.PolicyLocks().CheckLocks(rules); !blocked {
		t.Fatal("ship operator should be blocked by COMMAND lock")
	}

	commandBridge := New(shore.URL, "", nil)
	if err := commandBridge.SetOperatorJWT(mintBridgeOperator(t, "CTF-72", pb.Echelon_ECHELON_COMMAND)); err != nil {
		t.Fatal(err)
	}
	warnings, blocked := commandBridge.PolicyLocks().CheckLocks(rules)
	if blocked || len(warnings) != 0 {
		t.Fatalf("command operator lock result warnings=%v blocked=%t", warnings, blocked)
	}
}

func TestSetPolicyBuildsOperatorLayerAtTokenEchelon(t *testing.T) {
	current := &pb.Policy{Interest: &pb.Interest{}}
	var sent *pb.Policy
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetPolicy":
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: current})
		case consolePath + "SetPolicy":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read SetPolicy request: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var request pb.SetPolicyRequest
			if err := protojson.Unmarshal(raw, &request); err != nil {
				t.Errorf("decode SetPolicy request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			sent = proto.Clone(request.Policy).(*pb.Policy)
			writeProtoResponse(t, w, &pb.SetPolicyResponse{Policy: sent})
		default:
			http.NotFound(w, r)
		}
	}))
	defer shore.Close()

	bridge := New(shore.URL, "", nil)
	if err := bridge.SetOperatorJWT(mintBridgeOperator(t, "TAO-DDG51", pb.Echelon_ECHELON_COMMAND)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/shore/policy", strings.NewReader(`{"boost":["tanker"]}`))
	bridge.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusOK || sent == nil {
		t.Fatalf("status=%d body=%s policy=%v", rec.Code, rec.Body.String(), sent)
	}
	if len(sent.Layers) != 1 || sent.Layers[0].Echelon != pb.Echelon_ECHELON_COMMAND {
		t.Fatalf("operator layer not created at token echelon: %+v", sent.Layers)
	}
}

func TestOperatorSubjectOverridesIssuedByForPolicyAndEMCON(t *testing.T) {
	var mu sync.Mutex
	stored := &pb.Policy{Interest: &pb.Interest{}}
	var requests []*pb.Policy
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetPolicy":
			mu.Lock()
			policy := proto.Clone(stored).(*pb.Policy)
			mu.Unlock()
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: policy})
		case consolePath + "SetPolicy":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read SetPolicy request: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var request pb.SetPolicyRequest
			if err := protojson.Unmarshal(raw, &request); err != nil {
				t.Errorf("decode SetPolicy request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.Policy == nil {
				t.Error("SetPolicy request omitted policy")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			received := proto.Clone(request.Policy).(*pb.Policy)
			mu.Lock()
			requests = append(requests, received)
			stored = proto.Clone(received).(*pb.Policy)
			for _, layer := range stored.Layers {
				if layer != nil && layer.Echelon == pb.Echelon_ECHELON_SHIP {
					layer.IssuedBy = "ops-1"
				}
			}
			responsePolicy := proto.Clone(stored).(*pb.Policy)
			mu.Unlock()
			writeProtoResponse(t, w, &pb.SetPolicyResponse{Policy: responsePolicy})
		default:
			http.NotFound(w, r)
		}
	}))
	defer shore.Close()

	bridge := New(shore.URL, "", nil)
	if err := bridge.SetOperatorJWT(mintBridgeOperator(t, "ops-1", pb.Echelon_ECHELON_SHIP)); err != nil {
		t.Fatal(err)
	}
	handler := bridge.Handler()
	post := func(path, body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		handler.ServeHTTP(rec, request)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	post("/api/shore/policy", `{"text":"ignore cargo","issuedBy":"spoof"}`)
	post("/api/shore/policy", `{"text":"prioritize tankers","issuedBy":"spoof"}`)

	mu.Lock()
	if len(requests) != 2 {
		mu.Unlock()
		t.Fatalf("SetPolicy calls=%d, want 2", len(requests))
	}
	second := proto.Clone(requests[1]).(*pb.Policy)
	mu.Unlock()
	if len(second.Layers) != 1 || second.Layers[0].Echelon != pb.Echelon_ECHELON_SHIP ||
		second.Layers[0].IssuedBy != "ops-1" {
		t.Fatalf("second policy layers=%+v", second.Layers)
	}
	if len(second.Layers[0].Rules) != 1 {
		t.Fatalf("second directive rules=%+v", second.Layers[0].Rules)
	}
	rule := second.Layers[0].Rules[0]
	if rule.Author != "ops-1" || rule.Text != "prioritize tankers" ||
		len(rule.BoostLabels) != 1 || rule.BoostLabels[0] != "tanker" || len(rule.SuppressLabels) != 0 {
		t.Fatalf("second directive rule=%+v", rule)
	}

	post("/api/shore/emcon", `{"mode":"SILENT","issuedBy":"spoof"}`)
	mu.Lock()
	emconPolicy := proto.Clone(requests[len(requests)-1]).(*pb.Policy)
	mu.Unlock()
	if len(emconPolicy.Layers) != 1 || emconPolicy.Layers[0].Emcon == nil ||
		emconPolicy.Layers[0].Emcon.SetBy != "ops-1" {
		t.Fatalf("EMCON layer=%+v", emconPolicy.Layers)
	}
}

func TestTimedOperatorDirectiveExpiresItsRule(t *testing.T) {
	var mu sync.Mutex
	var submitted *pb.Policy
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetPolicy":
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: &pb.Policy{Interest: &pb.Interest{}}})
		case consolePath + "SetPolicy":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read SetPolicy request: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var request pb.SetPolicyRequest
			if err := protojson.Unmarshal(raw, &request); err != nil {
				t.Errorf("decode SetPolicy request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			submitted = proto.Clone(request.Policy).(*pb.Policy)
			mu.Unlock()
			writeProtoResponse(t, w, &pb.SetPolicyResponse{Policy: request.Policy})
		default:
			http.NotFound(w, r)
		}
	}))
	defer shore.Close()

	bridge := New(shore.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	if err := bridge.SetOperatorJWT(mintBridgeOperator(t, "ops-1", pb.Echelon_ECHELON_SHIP)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/shore/policy",
		strings.NewReader(`{"text":"ignore cargo for 30 minutes"}`))
	bridge.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	policy := proto.Clone(submitted).(*pb.Policy)
	mu.Unlock()
	if policy.ExpiresAtUnixMs != 0 {
		t.Fatalf("policy expiry=%d, want 0", policy.ExpiresAtUnixMs)
	}
	if len(policy.Layers) != 1 || len(policy.Layers[0].Rules) != 1 {
		t.Fatalf("submitted layers=%+v", policy.Layers)
	}
	if expiry := policy.Layers[0].Rules[0].ExpiresAtUnixMs; expiry <= time.Now().UnixMilli() {
		t.Fatalf("rule expiry=%d, want future expiry", expiry)
	}
}

func TestOperatorEMCONWrapPreservesFlatBoostFactor(t *testing.T) {
	var mu sync.Mutex
	var submitted *pb.Policy
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetPolicy":
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: &pb.Policy{
				Interest: &pb.Interest{BoostFactor: 3, BoostLabels: []string{"tanker"}},
			}})
		case consolePath + "SetPolicy":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read SetPolicy request: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var request pb.SetPolicyRequest
			if err := protojson.Unmarshal(raw, &request); err != nil {
				t.Errorf("decode SetPolicy request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			submitted = proto.Clone(request.Policy).(*pb.Policy)
			mu.Unlock()
			writeProtoResponse(t, w, &pb.SetPolicyResponse{Policy: request.Policy})
		default:
			http.NotFound(w, r)
		}
	}))
	defer shore.Close()

	bridge := New(shore.URL, "", nil)
	if err := bridge.SetOperatorJWT(mintBridgeOperator(t, "ops-1", pb.Echelon_ECHELON_SHIP)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/shore/emcon",
		strings.NewReader(`{"mode":"SILENT","issuedBy":"spoof"}`))
	bridge.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	policy := proto.Clone(submitted).(*pb.Policy)
	mu.Unlock()
	if len(policy.Layers) != 1 || len(policy.Layers[0].Rules) != 1 {
		t.Fatalf("submitted layers=%+v", policy.Layers)
	}
	if got := policy.Layers[0].Rules[0].Weight; got != 3 {
		t.Fatalf("boost rule weight=%v, want 3", got)
	}
}

func TestShoreAuthorizationFailureIsReturnedAsJSON(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
	}{
		{name: "policy", path: "/api/shore/policy", body: `{"text":"ignore submarines"}`},
		{name: "emcon", path: "/api/shore/emcon", body: `{"mode":"SILENT"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == consolePath+"GetPolicy" {
					writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: &pb.Policy{Interest: &pb.Interest{}}})
					return
				}
				if r.URL.Path != consolePath+"SetPolicy" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code":"permission_denied","message":"operator layer denied by shore"}`))
			}))
			defer shore.Close()

			bridge := New(shore.URL, "", nil)
			if err := bridge.SetOperatorJWT(mintBridgeOperator(t, "TAO-DDG51", pb.Echelon_ECHELON_SHIP)); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			bridge.Handler().ServeHTTP(rec, request)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Fatalf("Content-Type=%q", got)
			}
			var response struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error response: %v body=%s", err, rec.Body.String())
			}
			if response.Error != "operator layer denied by shore" {
				t.Fatalf("error=%q", response.Error)
			}
		})
	}
}
