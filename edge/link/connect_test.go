package link

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestConnectAttachesUnitToken(t *testing.T) {
	const key = "test-signing-key-at-least-32-bytes-long"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	auth, err := unitauth.New(true, key)
	if err != nil {
		t.Fatal(err)
	}
	_, h := ghostseekerv1connect.NewSyncServiceHandler(ssync.Handler{Service: ssync.New(st, clock.Real{}, nil)}, connect.WithInterceptors(auth))
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	defer srv.Close()
	token, err := unitauth.Mint("edge-1", key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client := NewConnect(srv.URL, time.Second, NewEstimator(64000, 300, 1e9), token)
	if _, err := client.Exchange(context.Background(), &pb.ExchangeRequest{EdgeId: "edge-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Exchange(context.Background(), &pb.ExchangeRequest{EdgeId: "edge-2"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
}
