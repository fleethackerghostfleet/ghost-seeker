package link

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

type echoShore struct{}

func (echoShore) Exchange(_ context.Context, req *connect.Request[pb.ExchangeRequest]) (*connect.Response[pb.ExchangeResponse], error) {
	resp := &pb.ExchangeResponse{}
	for _, m := range req.Msg.Manifest {
		resp.Acks = append(resp.Acks, &pb.Ack{ItemId: m.ItemId, Manifest: true})
	}
	return connect.NewResponse(resp), nil
}

func TestConnectGzipsExchange(t *testing.T) {
	var mu sync.Mutex
	// gRPC envelope: byte 0 is the per-message compressed flag.
	var reqGzipped []bool
	var respEnc []string
	path, h := ghostseekerv1connect.NewSyncServiceHandler(echoShore{}, HandlerOptions()...)
	mux := http.NewServeMux()
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		h.ServeHTTP(w, r)
		mu.Lock()
		reqGzipped = append(reqGzipped, len(body) > 0 && body[0] == 1 && r.Header.Get("Grpc-Encoding") == "gzip")
		respEnc = append(respEnc, w.Header().Get("Grpc-Encoding"))
		mu.Unlock()
	}))
	srv := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer srv.Close()

	c := NewConnect(srv.URL, 10*time.Second, NewEstimator(64000, 300, 1e9))
	// Small request: under CompressMinBytes, sent plain.
	if _, err := c.Exchange(context.Background(), &pb.ExchangeRequest{EdgeId: "e"}); err != nil {
		t.Fatal(err)
	}
	// Manifest batch: well over the threshold, gzipped both ways.
	req := &pb.ExchangeRequest{EdgeId: "e"}
	for i := 0; i < 100; i++ {
		req.Manifest = append(req.Manifest, &pb.ManifestEntry{ItemId: "01JABCDEFGHJKMNPQRSTVWXYZ" + strings.Repeat("0", i%3),
			Type: pb.ItemType_ITEM_TYPE_IMAGE, Source: "cam-fwd", TopLabel: "cargo ship", TopConf: 0.92})
	}
	resp, err := c.Exchange(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Acks) != 100 {
		t.Fatalf("acks = %d", len(resp.Acks))
	}
	mu.Lock()
	defer mu.Unlock()
	if reqGzipped[0] {
		t.Errorf("tiny request should not be gzipped")
	}
	if !reqGzipped[1] || respEnc[1] != "gzip" {
		t.Errorf("manifest exchange: request gzipped %v, response encoding %q; want gzip both", reqGzipped[1], respEnc[1])
	}
}

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
