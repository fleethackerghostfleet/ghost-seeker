// Package inference is the edge's view of the interchangeable image model.
// Analyzer is satisfied by the Connect client to a sidecar (ONNX, etc.) and
// by Mock for simulation and tests.
package inference

import (
	"context"
	"hash/fnv"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/vec"
)

// Frame is one captured image plus the context a stateful engine needs to
// track the same object across frames of one source.
type Frame struct {
	Image      []byte
	Source     string
	CapturedAt time.Time
}

type Analyzer interface {
	Analyze(ctx context.Context, f Frame, knownLabels []string, hints map[string]string) (*pb.AnalyzeResponse, error)
	EmbedText(ctx context.Context, texts []string) ([][]float32, error)
}

// Remote talks to any InferenceService over Connect.
type Remote struct {
	c ghostseekerv1connect.InferenceServiceClient
}

func NewRemote(url string) *Remote {
	return &Remote{c: ghostseekerv1connect.NewInferenceServiceClient(&http.Client{Timeout: 60 * time.Second}, url, connect.WithProtoJSON())}
}

func (r *Remote) Analyze(ctx context.Context, f Frame, known []string, _ map[string]string) (*pb.AnalyzeResponse, error) {
	req := &pb.AnalyzeRequest{Image: f.Image, KnownLabels: known, WantEmbedding: true, Source: f.Source}
	if !f.CapturedAt.IsZero() {
		req.CapturedAtUnixMs = f.CapturedAt.UnixMilli()
	}
	resp, err := r.c.Analyze(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (r *Remote) EmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := r.c.EmbedText(ctx, connect.NewRequest(&pb.EmbedTextRequest{Texts: texts}))
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(resp.Msg.Embeddings))
	for i, e := range resp.Msg.Embeddings {
		out[i] = e.Values
	}
	return out, nil
}

// Mock is a deterministic stand-in: it reads ground truth from hints
// ("label", "conf", "unknownness") and produces embeddings such that the
// same text always maps to the same vector, so interest-vector similarity
// behaves like a real joint embedding would (same concept ⇒ cosine ≈ 1).
type Mock struct {
	Dim   int
	Noise float64 // per-image jitter added to the label vector
}

func NewMock() *Mock { return &Mock{Dim: 64, Noise: 0.15} }

func (m *Mock) Analyze(_ context.Context, f Frame, _ []string, hints map[string]string) (*pb.AnalyzeResponse, error) {
	image := f.Image
	label := hints["label"]
	conf := parseF(hints["conf"], 0.9)
	unk := parseF(hints["unknownness"], 0)
	if label == "" && unk == 0 {
		unk = 0.1 // empty ocean: nothing detected, but nothing surprising either
	}
	resp := &pb.AnalyzeResponse{ModelId: "mock-v1", Unknownness: float32(unk)}
	if label != "" {
		resp.Detections = []*pb.Detection{{Label: label, Confidence: float32(conf), Box: &pb.Box{X: 0.3, Y: 0.3, W: 0.4, H: 0.4}}}
	}
	seedText := label
	if seedText == "" {
		seedText = "open ocean"
	}
	if unk >= 0.5 {
		seedText = "unknown:" + hints["truth"] // unknowns embed near their true concept, far from known labels
	}
	e := m.textVec(seedText)
	h := fnv.New64a()
	h.Write(image)
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	for i := range e {
		e[i] += float32(r.NormFloat64() * m.Noise)
	}
	resp.Embedding = vec.Normalize(e)
	return resp, nil
}

func (m *Mock) EmbedText(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.textVec(t)
	}
	return out, nil
}

// textVec is a sum of unit vectors per normalized word, so "red sailboat"
// is close to "sailboat". Good enough to exercise the pipeline; the real
// sidecar replaces this with a CLIP-family text encoder.
func (m *Mock) textVec(text string) []float32 {
	v := make([]float32, m.Dim)
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		words = []string{"nothing"}
	}
	for _, w := range words {
		w = strings.TrimSuffix(w, "s")
		h := fnv.New64a()
		h.Write([]byte(w))
		r := rand.New(rand.NewSource(int64(h.Sum64())))
		for i := range v {
			v[i] += float32(r.NormFloat64())
		}
	}
	return vec.Normalize(v)
}

func parseF(s string, def float64) float64 {
	if s == "" {
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) {
		return def
	}
	return f
}
