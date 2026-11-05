package policy

import (
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestTierLabelsRoundTrip(t *testing.T) {
	want := &pb.Policy{Interest: &pb.Interest{TierLabels: map[string]int32{"tanker": 2}}, Layers: []*pb.PolicyLayer{{Rules: []*pb.InterestRule{{TierLabels: map[string]int32{"tanker": 2}}}}}}
	for name, codec := range map[string]struct {
		marshal   func(proto.Message) ([]byte, error)
		unmarshal func([]byte, proto.Message) error
	}{"wire": {proto.Marshal, proto.Unmarshal}, "json": {protojson.Marshal, protojson.Unmarshal}} {
		t.Run(name, func(t *testing.T) {
			data, err := codec.marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			got := &pb.Policy{}
			if err := codec.unmarshal(data, got); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(got, want) {
				t.Fatalf("round trip lost pins: %v", got)
			}
		})
	}
}
