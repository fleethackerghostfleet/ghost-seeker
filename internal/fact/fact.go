// Package fact defines the FACT form: the ≤1 KB JSON summary of an item
// that is always sent first. Shared by edge (producer) and shore (consumer).
package fact

import (
	"encoding/json"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type Label struct {
	Label string    `json:"l"`
	Conf  float32   `json:"c"`
	Box   []float32 `json:"b,omitempty"`
}

type Fact struct {
	ID         string            `json:"id"`
	Type       string            `json:"t"`
	CapturedAt int64             `json:"at"`
	Source     string            `json:"src,omitempty"`
	Lat        float64           `json:"lat,omitempty"`
	Lon        float64           `json:"lon,omitempty"`
	Heading    float64           `json:"hdg,omitempty"`
	Labels     []Label           `json:"labels,omitempty"`
	Unknown    float32           `json:"unk"`
	Model      string            `json:"model,omitempty"`
	Override   string            `json:"override,omitempty"`
	OverrideBy string            `json:"override_by,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	// Embedding is omitted from FACT to keep it under 1 KB; shore can pull it
	// later as part of a richer form if needed.
}

func FromItem(it *pb.Item) *Fact {
	f := &Fact{ID: it.Id, Type: typeName(it.Type), CapturedAt: it.CapturedAtUnixMs, Source: it.Source}
	if g := it.Geo; g != nil {
		f.Lat, f.Lon, f.Heading = g.Lat, g.Lon, g.HeadingDeg
	}
	if ft := it.Features; ft != nil {
		for _, d := range ft.Detections {
			l := Label{Label: d.Label, Conf: d.Confidence}
			if d.Box != nil {
				l.Box = []float32{d.Box.X, d.Box.Y, d.Box.W, d.Box.H}
			}
			f.Labels = append(f.Labels, l)
		}
		f.Unknown, f.Model, f.Override, f.OverrideBy, f.Tags = ft.Unknownness, ft.ModelId, ft.LabelOverride, ft.OverrideBy, ft.Tags
	}
	return f
}

func (f *Fact) Marshal() []byte {
	b, _ := json.Marshal(f)
	return b
}

func (f *Fact) ToFeatures() *pb.Features {
	ft := &pb.Features{Unknownness: f.Unknown, ModelId: f.Model, LabelOverride: f.Override, OverrideBy: f.OverrideBy, Tags: f.Tags}
	for _, l := range f.Labels {
		d := &pb.Detection{Label: l.Label, Confidence: l.Conf}
		if len(l.Box) == 4 {
			d.Box = &pb.Box{X: l.Box[0], Y: l.Box[1], W: l.Box[2], H: l.Box[3]}
		}
		ft.Detections = append(ft.Detections, d)
	}
	return ft
}

func (f *Fact) TopLabel() string {
	if f.Override != "" {
		return f.Override
	}
	best := ""
	var bc float32 = -1
	for _, l := range f.Labels {
		if l.Conf > bc {
			best, bc = l.Label, l.Conf
		}
	}
	return best
}

func typeName(t pb.ItemType) string {
	switch t {
	case pb.ItemType_ITEM_TYPE_IMAGE:
		return "image"
	case pb.ItemType_ITEM_TYPE_RECORD:
		return "record"
	case pb.ItemType_ITEM_TYPE_TELEMETRY:
		return "telemetry"
	case pb.ItemType_ITEM_TYPE_DIGEST:
		return "digest"
	}
	return "unknown"
}
