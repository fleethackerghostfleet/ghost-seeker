package link

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"google.golang.org/protobuf/proto"
)

// Connect is a real link: Connect-protocol (binary protobuf over HTTP) to
// the shore SyncService. Put Toxiproxy or a radio in front of it.
type Connect struct {
	client ghostseekerv1connect.SyncServiceClient
	est    *Estimator
	token  string
}

// CompressMinBytes is the smallest message worth gzipping. Below it the
// gzip header and CPU outweigh the saving.
const CompressMinBytes = 512

// Requests are gzipped (manifest entries, FACT JSON and protobuf framing
// compress well; image chunks are already JPEG and pass through at ~0.1%
// overhead). Fast level: on a fat link the exchange is mostly image bytes.
func NewConnect(shoreURL string, timeout time.Duration, est *Estimator, token ...string) *Connect {
	hc := &http.Client{Timeout: timeout}
	return NewConnectWithClient(shoreURL, hc, est, token...)
}

// NewConnectWithClient preserves the caller's settings and enables HTTP/2 on standard transports.
func NewConnectWithClient(shoreURL string, hc *http.Client, est *Estimator, token ...string) *Connect {
	client := *hc
	transport := hc.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		transport := standard.Clone()
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP2(true)
		transport.Protocols.SetUnencryptedHTTP2(true)
		client.Transport = transport
	}
	c := &Connect{
		client: ghostseekerv1connect.NewSyncServiceClient(&client, shoreURL, connect.WithGRPC(),
			connect.WithAcceptCompression(gzipName, newGzipDecompressor, newGzipCompressor),
			connect.WithSendCompression(gzipName),
			connect.WithCompressMinBytes(CompressMinBytes)),
		est: est,
	}
	if len(token) > 0 {
		c.token = token[0]
	}
	return c
}

// HandlerOptions configures the shore SyncService handler to match: accept
// gzip requests and gzip responses (acks, downlink policy) to clients that
// accept it.
func HandlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{
		connect.WithCompression(gzipName, newGzipDecompressor, newGzipCompressor),
		connect.WithCompressMinBytes(CompressMinBytes),
	}
}

const gzipName = "gzip"

func newGzipDecompressor() connect.Decompressor { return &gzip.Reader{} }

func newGzipCompressor() connect.Compressor {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}

func (c *Connect) Exchange(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	start := time.Now()
	request := connect.NewRequest(req)
	if c.token != "" {
		request.Header().Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Exchange(ctx, request)
	if err != nil {
		c.est.Failure()
		return nil, err
	}
	c.est.Success(int64(proto.Size(req)+proto.Size(resp.Msg)), time.Since(start), time.Now())
	return resp.Msg, nil
}

func (c *Connect) State() *pb.LinkState { return c.est.State() }
