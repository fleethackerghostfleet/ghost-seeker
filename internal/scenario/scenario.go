// Package scenario describes a replay: which frame sets to stream, at what
// rate, and operator events (policy changes, pulls) at given times. Used by
// the simulator and by the edge binary in replay mode.
package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type Scenario struct {
	Name    string            `json:"name"`
	PeriodS float64           `json:"period_s"` // seconds between captures
	Sets    []ingest.FrameSet `json:"sets"`
	Events  []Event           `json:"events"`
}

type Event struct {
	AtS      float64         `json:"at_s"`
	Policy   json.RawMessage `json:"policy,omitempty"`   // pb.Policy in protojson
	Pull     json.RawMessage `json:"pull,omitempty"`     // pb.Pull
	Override json.RawMessage `json:"override,omitempty"` // pb.LabelOverride
	// PullNthUnknown pulls the FULL form of the Nth unknown shore has seen
	// (1-based); resolved at run time since item IDs are generated.
	PullNthUnknown int `json:"pull_nth_unknown,omitempty"`
}

func (e Event) At() time.Duration { return time.Duration(e.AtS * float64(time.Second)) }

func (e Event) PolicyMsg() (*pb.Policy, error) {
	if e.Policy == nil {
		return nil, nil
	}
	p := &pb.Policy{}
	return p, protojson.Unmarshal(e.Policy, p)
}

func (e Event) PullMsg() (*pb.Pull, error) {
	if e.Pull == nil {
		return nil, nil
	}
	p := &pb.Pull{}
	return p, protojson.Unmarshal(e.Pull, p)
}

func (e Event) OverrideMsg() (*pb.LabelOverride, error) {
	if e.Override == nil {
		return nil, nil
	}
	p := &pb.LabelOverride{}
	return p, protojson.Unmarshal(e.Override, p)
}

func Load(path string) (*Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	for i := range s.Sets {
		if !filepath.IsAbs(s.Sets[i].Path) {
			s.Sets[i].Path = filepath.Join(dir, s.Sets[i].Path)
		}
	}
	if s.PeriodS <= 0 {
		s.PeriodS = 10
	}
	return &s, nil
}

// Default builds a scenario from the frame zips in frameDir: hours of cargo
// ship as the "known, low-value" bulk, open-ocean timelapses as empty
// frames, dolphins as the injected unknown. At a third of the way in the
// operator says "ignore cargo ships"; later shore pulls the first unknown.
func Default(frameDir string) *Scenario {
	z := func(n string) string { return filepath.Join(frameDir, n) }
	return &Scenario{
		Name:    "default",
		PeriodS: 10,
		Sets: []ingest.FrameSet{
			{Path: z("Cargo_Ship_Uncut_View_frames.zip"), Source: "cam-fwd", Label: "cargo ship", Conf: 0.92, Every: 60, Limit: 150},
			{Path: z("Cargo_Ship_Storm_first_hour_frames.zip"), Source: "cam-fwd", Label: "cargo ship", Conf: 0.71, Every: 60, Limit: 60},
			{Path: z("30_Days_at_Sea_Timelapse_frames.zip"), Source: "cam-port", Label: "", Every: 6, Limit: 100},
			{Path: z("Argentina_to_Alaska_Cruise_Bridge_Timelapse_frames.zip"), Source: "cam-stbd", Label: "", Every: 2, Limit: 60},
			{Path: z("Dolphins_Pungo_Ferry_Bridge_frames.zip"), Source: "cam-port", Label: "", Unknownness: 0.9, Truth: "dolphin", Limit: 6},
		},
		Events: []Event{
			{AtS: 1200, Policy: json.RawMessage(`{"issuedBy":"shore/ops","text":"we are in a shipping lane; ignore cargo ships, high priority only","interest":{"suppressLabels":["cargo ship"]}}`)},
			{AtS: 2400, PullNthUnknown: 1},
		},
	}
}
