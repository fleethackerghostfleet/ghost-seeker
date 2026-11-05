package unitauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
	"github.com/golang-jwt/jwt/v5"
)

const testKey = "test-signing-key-at-least-32-bytes-long"

func token(t *testing.T, sub, role, key, alg string, exp *jwt.NumericDate) string {
	t.Helper()
	c := claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: sub, ExpiresAt: exp}}
	v, err := jwt.NewWithClaims(jwt.GetSigningMethod(alg), c).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tokenWithEchelon(t *testing.T, sub, role, echelon, key, alg string, exp *jwt.NumericDate) string {
	t.Helper()
	c := claims{Role: role, Echelon: echelon, RegisteredClaims: jwt.RegisteredClaims{Subject: sub, ExpiresAt: exp}}
	v, err := jwt.NewWithClaims(jwt.GetSigningMethod(alg), c).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSyncAuth(t *testing.T) {
	future := jwt.NewNumericDate(time.Now().Add(time.Hour))
	valid, err := Mint("edge-1", testKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	operatorToken, err := MintOperator("edge-1", pb.Echelon_ECHELON_SHIP, testKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, bearer, node string
		required           bool
		code               connect.Code
	}{
		{"missing", "", "edge-1", true, connect.CodeUnauthenticated},
		{"malformed", "broken", "edge-1", true, connect.CodeUnauthenticated},
		{"expired", token(t, "edge-1", "unit", testKey, "HS256", jwt.NewNumericDate(time.Now().Add(-time.Second))), "edge-1", true, connect.CodeUnauthenticated},
		{"bad signature", token(t, "edge-1", "unit", "other-key-at-least-32-bytes-long!!", "HS256", future), "edge-1", true, connect.CodeUnauthenticated},
		{"wrong algorithm", token(t, "edge-1", "unit", testKey, "HS384", future), "edge-1", true, connect.CodeUnauthenticated},
		{"missing expiry", token(t, "edge-1", "unit", testKey, "HS256", nil), "edge-1", true, connect.CodeUnauthenticated},
		{"empty subject", token(t, "", "unit", testKey, "HS256", future), "edge-1", true, connect.CodeUnauthenticated},
		{"relay role", token(t, "edge-1", "relay", testKey, "HS256", future), "edge-1", true, connect.CodeUnauthenticated},
		{"missing role", token(t, "edge-1", "", testKey, "HS256", future), "edge-1", true, connect.CodeUnauthenticated},
		{"wrong node", valid, "edge-2", true, connect.CodePermissionDenied},
		{"missing node", valid, "", true, connect.CodePermissionDenied},
		{"valid", valid, "edge-1", true, 0},
		{"operator token", operatorToken, "edge-1", true, connect.CodeUnauthenticated},
		{"auth off", "", "edge-1", false, 0},
		{"auth off ignores invalid token", "broken", "edge-1", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			auth, err := New(tc.required, testKey)
			if err != nil {
				t.Fatal(err)
			}
			_, h := ghostseekerv1connect.NewSyncServiceHandler(ssync.Handler{Service: ssync.New(st, clock.Real{}, nil)}, connect.WithInterceptors(auth))
			srv := httptest.NewServer(h)
			defer srv.Close()
			client := ghostseekerv1connect.NewSyncServiceClient(srv.Client(), srv.URL)
			req := connect.NewRequest(&pb.ExchangeRequest{EdgeId: tc.node})
			if tc.bearer != "" {
				req.Header().Set("Authorization", "Bearer "+tc.bearer)
			}
			_, err = client.Exchange(context.Background(), req)
			if tc.code == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else if connect.CodeOf(err) != tc.code {
				t.Fatalf("got %v, want %v", err, tc.code)
			}
		})
	}
}

func TestOperatorFromHeader(t *testing.T) {
	future := jwt.NewNumericDate(time.Now().Add(time.Hour))
	valid, err := MintOperator("TAO-DDG51", pb.Echelon_ECHELON_SHIP, testKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := New(false, testKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		bearer string
		want   connect.Code
	}{
		{name: "missing", want: connect.CodeUnauthenticated},
		{name: "malformed", bearer: "broken", want: connect.CodeUnauthenticated},
		{name: "expired", bearer: tokenWithEchelon(t, "TAO", "operator", "ship", testKey, "HS256",
			jwt.NewNumericDate(time.Now().Add(-time.Second))), want: connect.CodeUnauthenticated},
		{name: "bad signature", bearer: tokenWithEchelon(t, "TAO", "operator", "ship",
			"other-key-at-least-32-bytes-long!!", "HS256", future), want: connect.CodeUnauthenticated},
		{name: "wrong algorithm", bearer: tokenWithEchelon(t, "TAO", "operator", "ship", testKey, "HS384", future),
			want: connect.CodeUnauthenticated},
		{name: "missing expiry", bearer: tokenWithEchelon(t, "TAO", "operator", "ship", testKey, "HS256", nil),
			want: connect.CodeUnauthenticated},
		{name: "empty subject", bearer: tokenWithEchelon(t, "", "operator", "ship", testKey, "HS256", future),
			want: connect.CodeUnauthenticated},
		{name: "unit role", bearer: tokenWithEchelon(t, "edge-1", "unit", "ship", testKey, "HS256", future),
			want: connect.CodeUnauthenticated},
		{name: "missing echelon", bearer: tokenWithEchelon(t, "TAO", "operator", "", testKey, "HS256", future),
			want: connect.CodeUnauthenticated},
		{name: "unknown echelon", bearer: tokenWithEchelon(t, "TAO", "operator", "fleet", testKey, "HS256", future),
			want: connect.CodeUnauthenticated},
		{name: "valid", bearer: valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := make(http.Header)
			if tc.bearer != "" {
				header.Set("Authorization", "Bearer "+tc.bearer)
			}
			operator, err := auth.OperatorFromHeader(header)
			if tc.want != 0 {
				if connect.CodeOf(err) != tc.want {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if operator.ID != "TAO-DDG51" || operator.Echelon != pb.Echelon_ECHELON_SHIP {
				t.Fatalf("operator=%+v", operator)
			}
		})
	}
}

func TestOperatorEchelonHelpersAndUnverifiedClaims(t *testing.T) {
	for name, echelon := range map[string]pb.Echelon{
		"unit": pb.Echelon_ECHELON_UNIT, "ship": pb.Echelon_ECHELON_SHIP, "command": pb.Echelon_ECHELON_COMMAND,
	} {
		got, err := EchelonFromString(name)
		if err != nil || got != echelon || EchelonString(echelon) != name {
			t.Fatalf("%s: got %v, %v", name, got, err)
		}
	}
	if _, err := EchelonFromString("unknown"); err == nil {
		t.Fatal("accepted unknown echelon")
	}
	raw, err := MintOperator("TAO", pb.Echelon_ECHELON_COMMAND, testKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := UnverifiedOperator(raw)
	if err != nil {
		t.Fatal(err)
	}
	if operator.ID != "TAO" || operator.Echelon != pb.Echelon_ECHELON_COMMAND {
		t.Fatalf("operator=%+v", operator)
	}
	if _, err := UnverifiedOperator("broken"); err == nil {
		t.Fatal("accepted malformed token")
	}
}

type fakeStream struct {
	connect.StreamingHandlerConn
	header http.Header
	nodes  []string
}

func (s *fakeStream) RequestHeader() http.Header { return s.header }
func (s *fakeStream) Receive(msg any) error {
	msg.(*pb.ExchangeRequest).EdgeId = s.nodes[0]
	s.nodes = s.nodes[1:]
	return nil
}

func TestStreamingChecksEveryMessage(t *testing.T) {
	valid, _ := Mint("edge-1", testKey, time.Hour)
	for _, tc := range []struct {
		name, token string
		nodes       []string
		required    bool
		want        connect.Code
	}{
		{"missing", "", []string{"edge-1"}, true, connect.CodeUnauthenticated},
		{"valid", valid, []string{"edge-1", "edge-1"}, true, 0},
		{"changed node", valid, []string{"edge-1", "edge-2"}, true, connect.CodePermissionDenied},
		{"off", "", []string{"edge-1", "edge-2"}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, _ := New(tc.required, testKey)
			s := &fakeStream{header: make(http.Header), nodes: append([]string(nil), tc.nodes...)}
			if tc.token != "" {
				s.header.Set("Authorization", "Bearer "+tc.token)
			}
			err := i.WrapStreamingHandler(func(ctx context.Context, c connect.StreamingHandlerConn) error {
				for range tc.nodes {
					if err := c.Receive(&pb.ExchangeRequest{}); err != nil {
						return err
					}
				}
				return nil
			})(context.Background(), s)
			if tc.want == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else if connect.CodeOf(err) != tc.want {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestKeyAndTTLValidation(t *testing.T) {
	if _, err := New(true, ""); err == nil {
		t.Fatal("accepted empty key")
	}
	if _, err := New(false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Mint("edge-1", testKey, 0); err == nil {
		t.Fatal("accepted zero TTL")
	}
	if _, err := Mint("", testKey, time.Hour); err == nil {
		t.Fatal("accepted empty node")
	}
	for _, echelon := range []pb.Echelon{pb.Echelon_ECHELON_UNSPECIFIED, pb.Echelon(99)} {
		if _, err := MintOperator("TAO", echelon, testKey, time.Hour); err == nil {
			t.Fatalf("accepted invalid operator echelon %v", echelon)
		}
	}
	if _, err := MintOperator("TAO", pb.Echelon_ECHELON_SHIP, "short", time.Hour); err == nil {
		t.Fatal("accepted short operator key")
	}
	if _, err := MintOperator("TAO", pb.Echelon_ECHELON_SHIP, testKey, time.Second-time.Nanosecond); err == nil {
		t.Fatal("accepted short operator TTL")
	}
	if _, err := MintOperator(" \t ", pb.Echelon_ECHELON_SHIP, testKey, time.Hour); err == nil {
		t.Fatal("accepted empty operator id")
	}
}
