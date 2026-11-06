package inference

import (
	"context"
	"net/http"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// CapabilitiesHandler is mounted behind the edge's operator-token middleware.
func CapabilitiesHandler(an Analyzer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caps := &pb.CapabilitiesResponse{}
		if remote, ok := an.(interface {
			Capabilities(context.Context) (*pb.CapabilitiesResponse, error)
		}); ok {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			var err error
			caps, err = remote.Capabilities(ctx)
			if err != nil {
				http.Error(w, "inference capabilities unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		data, err := protojson.Marshal(caps)
		if err != nil {
			http.Error(w, "invalid inference capabilities", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
}
