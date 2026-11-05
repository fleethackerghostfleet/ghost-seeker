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

func TestSyncAuth(t *testing.T) {
	future := jwt.NewNumericDate(time.Now().Add(time.Hour))
	valid, err := Mint("edge-1", testKey, time.Hour)
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
}
