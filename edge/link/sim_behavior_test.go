package link

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"google.golang.org/protobuf/proto"
)

func TestSimAccountsForBothDirections(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	request := &pb.ExchangeRequest{EdgeId: "edge-1"}
	response := &pb.ExchangeResponse{Acks: []*pb.Ack{{ItemId: "capture", Manifest: true}}}
	var calls int
	sim := NewSim(Trace{Windows: []Window{{Duration: time.Hour, Bps: 8000, RTT: 20 * time.Millisecond}}}, clk,
		func(_ context.Context, got *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
			calls++
			if !proto.Equal(got, request) {
				t.Errorf("handler request changed: %v", got)
			}
			return response, nil
		}, 1)
	got, err := sim.Exchange(context.Background(), request)
	wantBytes := int64(proto.Size(request) + proto.Size(response) + 128)
	wantTime := time.Duration(wantBytes)*time.Millisecond + 20*time.Millisecond
	if err != nil || !proto.Equal(got, response) || calls != 1 {
		t.Fatalf("exchange: %v %v calls=%d", got, err, calls)
	}
	if sim.BytesOnAir != wantBytes || sim.Exchanges != 1 || sim.Failed != 0 || clk.Now().Sub(start) != wantTime {
		t.Fatalf("accounting: bytes=%d exchanges=%d failed=%d elapsed=%v; want %d bytes and %v", sim.BytesOnAir, sim.Exchanges, sim.Failed, clk.Now().Sub(start), wantBytes, wantTime)
	}
	if state := sim.State(); !state.Up || state.LastSuccessUnixMs != clk.Now().UnixMilli() || state.ConsecutiveFailures != 0 {
		t.Fatalf("link success state: %v", state)
	}
}

func TestSimLossBeforeAndAfterDelivery(t *testing.T) {
	request, response := &pb.ExchangeRequest{EdgeId: "edge"}, &pb.ExchangeResponse{}
	reqBytes, respBytes := int64(proto.Size(request)+64), int64(proto.Size(response)+64)
	uplinkTime := time.Duration(reqBytes) * time.Millisecond
	handlerError := errors.New("shore rejected exchange")
	for _, tc := range []struct {
		name         string
		trace        Trace
		handlerError error
		wantError    error
		phase        string
		calls        int
		bytes        int64
	}{
		{"empty trace", Trace{}, nil, ErrLinkDown, "", 0, 0},
		{"uplink window closes", Trace{Windows: []Window{{Duration: uplinkTime - time.Nanosecond, Bps: 8000}, {Duration: time.Hour}}}, nil, ErrLost, "uplink", 0, reqBytes},
		{"uplink lost", Trace{Windows: []Window{{Duration: time.Hour, Bps: 8000, Loss: 1}}}, nil, ErrLost, "uplink", 0, reqBytes},
		{"handler rejects", Trace{Windows: []Window{{Duration: time.Hour, Bps: 8000}}}, handlerError, handlerError, "", 1, reqBytes},
		{"response window closes", Trace{Windows: []Window{{Duration: uplinkTime + time.Nanosecond, Bps: 8000}, {Duration: time.Hour}}}, nil, ErrLost, "downlink", 1, reqBytes + respBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewVirtual(time.Unix(1000, 0))
			calls := 0
			sim := NewSim(tc.trace, clk, func(context.Context, *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
				calls++
				return response, tc.handlerError
			}, 1)
			got, err := sim.Exchange(context.Background(), request)
			if got != nil || !errors.Is(err, tc.wantError) || !strings.Contains(err.Error(), tc.phase) {
				t.Fatalf("got %v %v, want %v during %q", got, err, tc.wantError, tc.phase)
			}
			if calls != tc.calls || sim.BytesOnAir != tc.bytes || sim.Exchanges != 1 || sim.Failed != 1 {
				t.Fatalf("delivery/accounting: calls=%d bytes=%d exchanges=%d failures=%d", calls, sim.BytesOnAir, sim.Exchanges, sim.Failed)
			}
			if sim.State().ConsecutiveFailures != 1 {
				t.Fatalf("estimator missed failure: %v", sim.State())
			}
			if errors.Is(err, ErrLinkDown) && clk.Now().Sub(sim.Start) != 2*time.Second {
				t.Fatal("down-link probe did not charge its timeout")
			}
		})
	}
}

func TestSimCancellationDoesNotReturnAnAck(t *testing.T) {
	for _, cancelBeforeDelivery := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		sim := NewSim(Trace{Windows: []Window{{Duration: time.Hour, Bps: 8000}}}, clock.NewVirtual(time.Unix(1000, 0)),
			func(context.Context, *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
				calls++
				cancel()
				return &pb.ExchangeResponse{}, nil
			}, 1)
		if cancelBeforeDelivery {
			cancel()
		}
		got, err := sim.Exchange(ctx, &pb.ExchangeRequest{})
		cancel()
		wantCalls := 1
		if cancelBeforeDelivery {
			wantCalls = 0
		}
		if got != nil || !errors.Is(err, context.Canceled) || calls != wantCalls {
			t.Fatalf("cancel before=%v: got %v %v calls=%d", cancelBeforeDelivery, got, err, calls)
		}
	}
}

func TestEstimatorSampleBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bytes   int64
		elapsed time.Duration
		want    float64
	}{
		{"zero duration", 4096, 0, 6000},
		{"empty sample", 0, time.Second, 6000},
		{"latency dominated", 100, time.Second, 6000},
		{"small fast sample caps growth", 100, time.Millisecond, 10000},
		{"large fast sample hits ceiling", 4096, time.Millisecond, 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			est := NewEstimator(6000, 300, 10000)
			at := time.Unix(1000, 0)
			est.Success(tc.bytes, tc.elapsed, at)
			if est.Bps() != tc.want || est.State().LastSuccessUnixMs != at.UnixMilli() {
				t.Fatalf("sample: got %v, want %v bps", est.State(), tc.want)
			}
		})
	}
	est := NewEstimator(300, 300, 10000)
	est.Success(2048, time.Hour, time.Unix(1000, 0))
	if est.Bps() != 300 {
		t.Fatalf("sample crossed floor: %v", est.State())
	}
}
