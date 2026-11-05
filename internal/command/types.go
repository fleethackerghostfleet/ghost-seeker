package command

import (
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

type Rule struct {
	Type        string   `json:"type"`
	Classes     []string `json:"classes"`
	Attrs       []string `json:"attrs"`
	Only        bool     `json:"only"`
	DirectiveID string   `json:"directiveId,omitempty"`
	Weight      float64  `json:"weight,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	WithinMin   int      `json:"withinMin,omitempty"`
	Edited      bool     `json:"edited,omitempty"`
}

type Directive struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Rules []Rule `json:"rules"`
}

type Box struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	W   float64 `json:"w"`
	H   float64 `json:"h"`
	CX  float64 `json:"cx,omitempty"`
	CY  float64 `json:"cy,omitempty"`
	L   float64 `json:"L,omitempty"`
	B   float64 `json:"B,omitempty"`
	A   float64 `json:"a,omitempty"`
	Top bool    `json:"top,omitempty"`
}

type Attributes struct {
	Approaching bool `json:"approaching"`
	New         bool `json:"new"`
	Dark        bool `json:"dark"`
	Moving      bool `json:"moving"`
	Loitering   bool `json:"loitering"`
}

type Detection struct {
	Class string             `json:"cls"`
	Box   Box                `json:"b"`
	Conf  float64            `json:"conf"`
	Attrs Attributes         `json:"attrs"`
	Ship  *catalog.ShipClass `json:"ship,omitempty"`
}

type DetectionScore struct {
	Score   float64 `json:"s"`
	Matched bool    `json:"matched"`
	Sup     bool    `json:"sup"`
	Focus   bool    `json:"focus"`
	Notes   []Rule  `json:"notes"`
}

type Latency struct {
	Edge int `json:"edge"`
	Up   int `json:"up"`
	Cen  int `json:"cen"`
}

type Item struct {
	Bands       []string   `json:"bands,omitempty"`
	ID          string     `json:"id"`
	Img         string     `json:"img,omitempty"`
	DBID        int        `json:"dbId"`
	Frame       int        `json:"frame"`
	SourceID    string     `json:"src"`
	TS          time.Time  `json:"ts"`
	Seed        uint32     `json:"seed"`
	HZ          float64    `json:"hz"`
	KB          int        `json:"kb"`
	Edge        float64    `json:"edge"`
	Central     float64    `json:"central"`
	Base        float64    `json:"base"`
	CentralTier int        `json:"centralTier"`
	BaseTier    int        `json:"baseTier"`
	Tier        int        `json:"tier"`
	Manual      int        `json:"manual"`
	Reviewed    bool       `json:"reviewed"`
	Dismissed   bool       `json:"dismissed"`
	Sent        bool       `json:"sent"`
	SentAt      *time.Time `json:"sentAt,omitempty"`
	// Commander's final decision on an image the watch floor escalated.
	Decision     string           `json:"decision,omitempty"`
	DecisionNote string           `json:"decisionNote,omitempty"`
	DecidedAt    *time.Time       `json:"decidedAt,omitempty"`
	Lat          Latency          `json:"lat"`
	Dets         []Detection      `json:"dets"`
	Det          []DetectionScore `json:"det"`
}

type Stats struct {
	Cap   int `json:"cap"`
	Keep  int `json:"keep"`
	Drop  int `json:"drop"`
	Bytes int `json:"bytes"`
	DB    int `json:"db"`
}
