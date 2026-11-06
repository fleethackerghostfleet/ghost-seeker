package inference

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type capableAnalyzer struct {
	Analyzer
	fail bool
}

func (a capableAnalyzer) Capabilities(context.Context) (*pb.CapabilitiesResponse, error) {
	if a.fail {
		return nil, errors.New("offline")
	}
	return &pb.CapabilitiesResponse{OpenVocabulary: true, EmbeddingDim: 512}, nil
}

func TestCapabilitiesHandler(t *testing.T) {
	for _, tt := range []struct {
		an   Analyzer
		code int
		open bool
	}{
		{capableAnalyzer{}, 200, true}, {capableAnalyzer{fail: true}, 503, false}, {nil, 200, false},
	} {
		r := httptest.NewRecorder()
		CapabilitiesHandler(tt.an).ServeHTTP(r, httptest.NewRequest("GET", "/ship/inference", nil))
		if r.Code != tt.code || (strings.Contains(r.Body.String(), `"openVocabulary":true`) != tt.open) {
			t.Fatalf("response %d %s", r.Code, r.Body.String())
		}
	}
}
