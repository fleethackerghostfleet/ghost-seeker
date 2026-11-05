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
	token  string
}

func NewConnect(shoreURL string, timeout time.Duration, est *Estimator, token ...string) *Connect {
	hc := &http.Client{Timeout: timeout}
	c := &Connect{
		client: ghostseekerv1connect.NewSyncServiceClient(hc, shoreURL, connect.WithGRPC()),
		est:    est,
	}
	if len(token) > 0 {
		c.token = token[0]
	}
	return c
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
