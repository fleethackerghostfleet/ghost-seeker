//go:build !prod

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type observed struct{ base http.RoundTripper }

type responseBody struct {
	io.ReadCloser
	response *http.Response
}

func (b responseBody) Close() error {
	err := b.ReadCloser.Close()
	json.NewEncoder(os.Stdout).Encode(map[string]any{"grpcStatus": b.response.Trailer.Get("Grpc-Status")})
	return err
}

func (t observed) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("User-Agent", "uds-harness-sync-probe")
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if resp.TLS == nil {
		resp.Body.Close()
		return nil, fmt.Errorf("probe requires a TLS response")
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"protocol": resp.Proto, "alpn": resp.TLS.NegotiatedProtocol,
		"verifiedChains": len(resp.TLS.VerifiedChains), "httpStatus": resp.StatusCode,
	})
	resp.Body = responseBody{resp.Body, resp}
	return resp, nil
}

func main() {
	url := flag.String("url", "https://shore.uds.dev:443", "shore URL through shaper")
	destination := flag.String("dial", "172.19.0.1:443", "TCP destination, preserving URL TLS identity")
	tokenFile := flag.String("token-file", "", "private unit-token file")
	flag.Parse()
	token, err := os.ReadFile(*tokenFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP2(true)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, *destination)
	}
	client := &http.Client{Transport: observed{transport}, Timeout: 15 * time.Second}
	lk := link.NewConnectWithClient(*url, client, link.NewEstimator(64000, 300, 16000000), string(token))
	_, err = lk.Exchange(context.Background(), &pb.ExchangeRequest{EdgeId: "edge-1"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"success": true, "link": lk.State()})
}
