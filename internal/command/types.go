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
	Blocked     string   `json:"blocked,omitempty"`
}

type Directive struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Rules    []Rule `json:"rules"`
	Source   string `json:"source,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
	LockedBy string `json:"lockedBy,omitempty"`
	Blocked  string `json:"blocked,omitempty"`
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
	Transient   bool `json:"transient,omitempty"`
	Approaching bool `json:"approaching"`
	New         bool `json:"new"`
	Dark        bool `json:"dark"`
	Moving      bool `json:"moving"`
	Loitering   bool `json:"loitering"`
}

type Detection struct {
	Lights []Light            `json:"lights,omitempty"`
	Signal *LightSignal       `json:"sig,omitempty"`
	Class  string             `json:"cls"`
	Box    Box                `json:"b"`
	Conf   float64            `json:"conf"`
	Attrs  Attributes         `json:"attrs"`
	Ship   *catalog.ShipClass `json:"ship,omitempty"`
}

type DetectionScore struct {
	Light   bool    `json:"light,omitempty"`
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
	Night       *bool      `json:"night,omitempty"`
	Basis       string     `json:"basis,omitempty"`
	Lum         float64    `json:"lum,omitempty"`
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
	Decision     string     `json:"decision,omitempty"`
	DecisionNote string     `json:"decisionNote,omitempty"`
	DecidedAt    *time.Time `json:"decidedAt,omitempty"`
	// Operator override: send the original image at full size, no compression.
	FullRes   bool             `json:"fullRes,omitempty"`
	FullResBy string           `json:"fullResBy,omitempty"`
	FullResAt *time.Time       `json:"fullResAt,omitempty"`
	Lat       Latency          `json:"lat"`
	Dets      []Detection      `json:"dets"`
	Det       []DetectionScore `json:"det"`
	// Live is set only when the item comes from a shore node (ghost-seeker
	// -shore); the simulation leaves it nil.
	Live *LiveMeta `json:"live,omitempty"`
}

// LiveForm is the shore-side state of one form of an item's fidelity ladder.
type LiveForm struct {
	State    string `json:"state"` // none | offered | pending | partial | received
	Bytes    int64  `json:"bytes"`
	Received int64  `json:"received"`
}

// LiveMeta carries what a shore sighting knows that the simulation does not:
// which shore item backs the card, how it was grouped, which forms have
// landed and whether the standing policy deprioritizes it.
type LiveMeta struct {
	ItemID       string              `json:"itemId"` // shore item the card shows (best received form)
	SightingID   string              `json:"sightingId,omitempty"`
	Subtype      string              `json:"subtype,omitempty"` // sidecar zero-shot subtype
	Label        string              `json:"label,omitempty"`   // detector class
	Override     string              `json:"override,omitempty"`
	Event        string              `json:"event,omitempty"`
	Unknownness  float64             `json:"unknownness"`
	Unknown      bool                `json:"unknown"`
	Score        float64             `json:"score"`    // shore priority score (unbounded)
	Priority     string              `json:"priority"` // P1..P4 by rank on shore
	Suppressed   bool                `json:"suppressed"`
	SuppressedBy string              `json:"suppressedBy,omitempty"`
	Frames       int                 `json:"frames"`
	FramesKnown  bool                `json:"framesKnown"`
	Reports      int                 `json:"reports"`
	FirstMs      int64               `json:"firstMs"`
	LastMs       int64               `json:"lastMs"`
	Forms        map[string]LiveForm `json:"forms"`
	ImageForm    string              `json:"imageForm,omitempty"`
	FactURL      string              `json:"factUrl"`
	Pull         LivePull            `json:"pull"`
}

// LivePull explains the state of the "Pull full image" action for a card.
type LivePull struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label"`
	Reason  string `json:"reason"`
}

type Stats struct {
	Cap   int `json:"cap"`
	Keep  int `json:"keep"`
	Drop  int `json:"drop"`
	Bytes int `json:"bytes"`
	DB    int `json:"db"`
}
