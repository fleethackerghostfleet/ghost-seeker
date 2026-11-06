package link

import (
	"bytes"
	"context"
	"fmt"
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

type trailerShore struct{ echoShore }

func (s trailerShore) Exchange(ctx context.Context, req *connect.Request[pb.ExchangeRequest]) (*connect.Response[pb.ExchangeResponse], error) {
	if req.Msg.EdgeId == "denied" {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}
	return s.echoShore.Exchange(ctx, req)
}

func TestConnectH2COnly(t *testing.T) {
	_, handler := ghostseekerv1connect.NewSyncServiceHandler(trailerShore{}, HandlerOptions()...)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("sync protocol = %s, want HTTP/2", r.Proto)
		}
		handler.ServeHTTP(w, r)
	}))
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	defer server.Close()

	for _, supplied := range []bool{false, true} {
		t.Run(fmt.Sprintf("supplied-client=%v", supplied), func(t *testing.T) {
			est := NewEstimator(64000, 300, 1e9)
			client := NewConnect(server.URL, time.Second, est)
			if supplied {
				transport := http.DefaultTransport.(*http.Transport).Clone()
				original := transport.Protocols
				hc := &http.Client{Transport: transport, Timeout: time.Second}
				client = NewConnectWithClient(server.URL, hc, est)
				if hc.Transport != transport || transport.Protocols != original {
					t.Fatal("modified caller's transport")
				}
			}
			for _, request := range []*pb.ExchangeRequest{{}, {Manifest: []*pb.ManifestEntry{{ItemId: "h2c-item"}}}} {
				response, err := client.Exchange(context.Background(), request)
				if err != nil || len(response.GetAcks()) != len(request.Manifest) {
					t.Fatalf("h2c exchange: %v %v", response, err)
				}
			}
			// gRPC success requires status 0; this also verifies a nonzero status trailer.
			if _, err := client.Exchange(context.Background(), &pb.ExchangeRequest{EdgeId: "denied"}); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("gRPC status trailer: %v", err)
			}
		})
	}
}

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
