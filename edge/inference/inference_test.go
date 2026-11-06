package inference

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/vec"
)

func TestMockEmbeddingsAndHints(t *testing.T) {
	m := NewMock()
	m.Noise = 0
	ctx := context.Background()
	vectors, err := m.EmbedText(ctx, []string{"BOATS", " boat ", "", "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vectors[0], vectors[1]) || !reflect.DeepEqual(vectors[2], vectors[3]) {
		t.Fatal("word normalization changed embeddings")
	}
	for _, v := range vectors {
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if len(v) != 64 || math.Abs(norm-1) > 1e-6 {
			t.Fatalf("embedding dimension/norm = %d/%g", len(v), norm)
		}
	}
	if got, err := m.EmbedText(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty embeddings: %v, %v", got, err)
	}
	r, err := m.Analyze(ctx, Frame{}, nil, nil)
	if err != nil || len(r.Detections) != 0 || r.Unknownness != .1 {
		t.Fatalf("empty ocean: %v, %v", r, err)
	}
	r, err = m.Analyze(ctx, Frame{}, nil, map[string]string{"label": "boat", "conf": "invalid", "unknownness": "NaN"})
	if err != nil || len(r.Detections) != 1 || r.Detections[0].Confidence != .9 || r.Unknownness != 0 || vec.Cosine(r.Embedding, vectors[0]) < .99999 {
		t.Fatalf("label/defaults: %v, %v", r, err)
	}
	r, err = m.Analyze(ctx, Frame{}, nil, map[string]string{"unknownness": "0.5", "truth": "dolphin"})
	u, _ := m.EmbedText(ctx, []string{"unknown:dolphin"})
	if err != nil || r.Unknownness != .5 || vec.Cosine(r.Embedding, u[0]) < .99999 {
		t.Fatalf("unknown threshold: %v, %v", r, err)
	}
	m.Noise = .15
	a, _ := m.Analyze(ctx, Frame{Image: []byte("frame")}, nil, nil)
	b, _ := m.Analyze(ctx, Frame{Image: []byte("frame")}, nil, nil)
	c, _ := m.Analyze(ctx, Frame{Image: []byte("other")}, nil, nil)
	if !reflect.DeepEqual(a.Embedding, b.Embedding) || reflect.DeepEqual(a.Embedding, c.Embedding) {
		t.Fatal("image-seeded noise is not deterministic/distinct")
	}
}

type testService struct {
	ghostseekerv1connect.UnimplementedInferenceServiceHandler
	analyze func(*pb.AnalyzeRequest) (*pb.AnalyzeResponse, error)
	embed   func(*pb.EmbedTextRequest) (*pb.EmbedTextResponse, error)
}

func (s testService) Analyze(_ context.Context, r *connect.Request[pb.AnalyzeRequest]) (*connect.Response[pb.AnalyzeResponse], error) {
	msg, err := s.analyze(r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func (s testService) EmbedText(_ context.Context, r *connect.Request[pb.EmbedTextRequest]) (*connect.Response[pb.EmbedTextResponse], error) {
	msg, err := s.embed(r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func TestRemoteWireAndErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rpc-error"}[fail], func(t *testing.T) {
			svc := testService{
				analyze: func(r *pb.AnalyzeRequest) (*pb.AnalyzeResponse, error) {
					if fail {
						return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad image"))
					}
					if string(r.Image) != "image" || r.Source != "mast" || !r.WantEmbedding || !reflect.DeepEqual(r.KnownLabels, []string{"boat"}) || r.CapturedAtUnixMs != 1234 {
						t.Errorf("analyze request: %v", r)
					}
					return &pb.AnalyzeResponse{ModelId: "remote", Embedding: []float32{1, 0}}, nil
				},
				embed: func(r *pb.EmbedTextRequest) (*pb.EmbedTextResponse, error) {
					if fail {
						return nil, connect.NewError(connect.CodeUnavailable, errors.New("offline"))
					}
					if !reflect.DeepEqual(r.Texts, []string{"boat", "person"}) {
						t.Errorf("embed request: %v", r)
					}
					return &pb.EmbedTextResponse{Embeddings: []*pb.Embedding{{Values: []float32{1, 0}}, {Values: []float32{0, 1}}}}, nil
				},
			}
			path, handler := ghostseekerv1connect.NewInferenceServiceHandler(svc)
			mux := http.NewServeMux()
			mux.Handle(path, handler)
			server := httptest.NewServer(mux)
			defer server.Close()
			r := NewRemote(server.URL)
			got, err := r.Analyze(context.Background(), Frame{Image: []byte("image"), Source: "mast", CapturedAt: time.UnixMilli(1234)}, []string{"boat"}, map[string]string{"label": "ignored"})
			if fail {
				if got != nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("analyze error: %v, %v", got, err)
				}
			} else if err != nil || got.ModelId != "remote" || !reflect.DeepEqual(got.Embedding, []float32{1, 0}) {
				t.Fatalf("analyze: %v, %v", got, err)
			}
			vs, err := r.EmbedText(context.Background(), []string{"boat", "person"})
			if fail {
				if vs != nil || connect.CodeOf(err) != connect.CodeUnavailable {
					t.Fatalf("embed error: %v, %v", vs, err)
				}
			} else if err != nil || !reflect.DeepEqual(vs, [][]float32{{1, 0}, {0, 1}}) {
				t.Fatalf("embed: %v, %v", vs, err)
			}
		})
	}
}

func TestRemoteEmptyAndCanceled(t *testing.T) {
	svc := testService{
		analyze: func(r *pb.AnalyzeRequest) (*pb.AnalyzeResponse, error) {
			if r.CapturedAtUnixMs != 0 || len(r.Image) != 0 || len(r.KnownLabels) != 0 {
				t.Errorf("empty request: %v", r)
			}
			return &pb.AnalyzeResponse{}, nil
		},
		embed: func(r *pb.EmbedTextRequest) (*pb.EmbedTextResponse, error) {
			if len(r.Texts) != 0 {
				t.Errorf("empty texts: %v", r)
			}
			return &pb.EmbedTextResponse{}, nil
		},
	}
	_, handler := ghostseekerv1connect.NewInferenceServiceHandler(svc)
	server := httptest.NewServer(handler)
	defer server.Close()
	r := NewRemote(server.URL)
	if got, err := r.Analyze(context.Background(), Frame{}, nil, nil); err != nil || got == nil {
		t.Fatalf("empty analyze: %v, %v", got, err)
	}
	if got, err := r.EmbedText(context.Background(), nil); err != nil || len(got) != 0 {
		t.Fatalf("empty embed: %v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Analyze(ctx, Frame{}, nil, nil); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled analyze: %v", err)
	}
	if _, err := r.EmbedText(ctx, nil); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled embed: %v", err)
	}
}
