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

func TestSetOperatorTokenRejectsInvalidClaims(t *testing.T) {
	unknownEchelon := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"TAO-DDG51","role":"operator","echelon":"admiral"}`))
	invalid := []string{"not-a-jwt", "eyJhbGciOiJub25lIn0." + unknownEchelon + ".x"}
	for _, token := range invalid {
		if err := New("", "", nil).SetOperatorToken(token); err == nil {
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

func TestOperatorTokenOnlySentToShore(t *testing.T) {
	token := mintBridgeOperator(t, "TAO-DDG51", pb.Echelon_ECHELON_SHIP)
	var mu sync.Mutex
	var shoreHeaders []string
	var edgeHeader string
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		shoreHeaders = append(shoreHeaders, r.Header.Get("Authorization"))
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
		edgeHeader = r.Header.Get("Authorization")
		mu.Unlock()
		if r.URL.Path != consolePath+"GetStatus" {
			http.NotFound(w, r)
			return
		}
		writeProtoResponse(t, w, &pb.GetStatusResponse{NodeId: "EDGE-1"})
	}))
	defer edge.Close()

	bridge := New(shore.URL, edge.URL, nil)
	if err := bridge.SetOperatorToken(token); err != nil {
		t.Fatal(err)
	}
	bridge.refresh(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(shoreHeaders) == 0 {
		t.Fatal("bridge made no shore ConsoleService calls")
	}
	for _, header := range shoreHeaders {
		if header != "Bearer "+token {
			t.Fatalf("shore Authorization=%q, want operator bearer", header)
		}
	}
	if edgeHeader != "" {
		t.Fatalf("edge Authorization=%q, want no operator token", edgeHeader)
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
	if err := commandBridge.SetOperatorToken(mintBridgeOperator(t, "CTF-72", pb.Echelon_ECHELON_COMMAND)); err != nil {
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
	if err := bridge.SetOperatorToken(mintBridgeOperator(t, "TAO-DDG51", pb.Echelon_ECHELON_COMMAND)); err != nil {
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
			if err := bridge.SetOperatorToken(mintBridgeOperator(t, "TAO-DDG51", pb.Echelon_ECHELON_SHIP)); err != nil {
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
