package link

import (
	"context"
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
}

func NewConnect(shoreURL string, timeout time.Duration, est *Estimator) *Connect {
	hc := &http.Client{Timeout: timeout}
	return &Connect{
		client: ghostseekerv1connect.NewSyncServiceClient(hc, shoreURL, connect.WithGRPC()),
		est:    est,
	}
}

func (c *Connect) Exchange(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	start := time.Now()
	resp, err := c.client.Exchange(ctx, connect.NewRequest(req))
	if err != nil {
		c.est.Failure()
		return nil, err
	}
	c.est.Success(int64(proto.Size(req)+proto.Size(resp.Msg)), time.Since(start), time.Now())
	return resp.Msg, nil
}

func (c *Connect) State() *pb.LinkState { return c.est.State() }
