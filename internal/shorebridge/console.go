package shorebridge

import (
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"sync"
	"time"
)

// Console serves the operator console's API (what web/static/app.js and
// cdr.js speak: /api/config, /api/stream, /api/items/{id}, /api/directives*)
// on live shore data, so the same console runs on the simulation and on a
// shore node. Items are command.Item like the simulation's, one per grouped
// object (sighting); the shore-only facts travel in Item.Live. Operator state
// the shore does not keep (manual tier, reviewed, dismissed, sent to the
// commander, the commander's decision) is an in-memory overlay keyed by object.
type Console struct {
	objects map[string]*Object
	reviews *reviews.Log
	pol     *pb.Policy        // flattened shore policy the current view was built from
	ruleFor map[string]string // lowercased label -> id of the directive that boosts/suppresses it
	b       *Bridge

	mu      sync.Mutex
	base    map[string]*command.Item // last built items without the overlay, by id
	order   []string
	overlay map[string]*overlay
	dbIDs   map[string]int
	classes map[string]catalog.Class  // live labels not in the catalog
	sources map[string]catalog.Source // cameras seen so far
	live    LiveStatus
	policy  LivePolicy
	ready   bool

	lastKey string // ids+policy+classes fingerprint of the last published snapshot

	subs map[chan sseEvent]struct{}
}

type overlay struct {
	Manual       int
	Reviewed     bool
	Dismissed    bool
	Sent         bool
	SentAt       *time.Time
	Decision     string
	DecisionNote string
	DecidedAt    *time.Time
	FullRes      bool
	FullResBy    string
	FullResAt    *time.Time
}

// LiveStatus is the shore-side block the console adds to every snapshot: the
// bandwidth story (link, queue, bytes), the standing policy, the per-camera
// heartbeats and the hook for the edge's selectable prioritization mode.
type LiveStatus struct {
	TS             int64                    `json:"ts"`
	Shore          NodeStatus               `json:"shore"`
	Edge           NodeStatus               `json:"edge"`
	Link           LinkStatus               `json:"link"`
	Counts         Counts                   `json:"counts"`
	Policy         LivePolicy               `json:"policy"`
	Cameras        []Camera                 `json:"cameras"`
	Prioritization Prioritization           `json:"prioritization"`
	Classes        map[string]catalog.Class `json:"classes"`
	Sources        []catalog.Source         `json:"sources"`
}

// Prioritization names the scorer ranking the feed on the edge. The edge's
// selectable mode (algorithm/ scorer vs built-in) reports here once it exists.
type Prioritization struct {
	Mode   string `json:"mode"`
	Label  string `json:"label"`
	Source string `json:"source"` // "edge" when reported by the edge, else "default"
}

// LivePolicy is the standing shore policy as the console shows it. The
// emcon/locks/conflicts fields mirror the simulation's policy summary so the
// policy panels render from either backend.
type LivePolicy struct {
	ID         string           `json:"id,omitempty"`
	Version    int64            `json:"version"`
	Text       string           `json:"text"`
	Suppress   []string         `json:"suppress"`
	Boost      []string         `json:"boost"`
	TierLabels map[string]int32 `json:"tierLabels,omitempty"`
	Readback   string           `json:"readback"`
	Admin      bool             `json:"admin"`
	NodeID     string           `json:"nodeId"`
	EMCON      EmconSummary     `json:"emcon"`
	Locks      []PolicyLock     `json:"locks"`
	Conflicts  []PolicyConflict `json:"conflicts"`
	// ExpiresAtUnixMs is 0 for an open-ended policy (the sim fills a default TTL; the shore keeps it until cleared).
	ExpiresAtUnixMs int64 `json:"expiresAtUnixMs"`
}

type EmconSummary struct {
	Mode     string `json:"mode"`
	SetBy    string `json:"setBy"`
	Locked   bool   `json:"locked"`
	LockedBy string `json:"lockedBy"`
}

// Camera is one entry of the Cameras strip: the edge's per-source heartbeat
// (object-free scene frame) or, failing that, the newest contact's image.
type Camera struct {
	Source    string      `json:"source"`
	Name      string      `json:"name"`
	LastMs    int64       `json:"lastMs"`
	Contacts  int         `json:"contacts"`
	Heartbeat bool        `json:"heartbeat"` // view is an object-free scene frame
	View      *CameraView `json:"view,omitempty"`
}

type CameraView struct {
	ItemID    string           `json:"itemId"`
	Image     string           `json:"image,omitempty"`
	ImageForm string           `json:"imageForm,omitempty"`
	LastMs    int64            `json:"lastMs"`
	Full      command.LiveForm `json:"full"`
	Pull      command.LivePull `json:"pull"`
}

// Frame size the console's boxes are expressed in (web/static/render.js W,H).
const frameW, frameH = 480.0, 300.0

const liveModel = "shore"

var liveModels = map[string]catalog.Model{
	liveModel: {Name: "Ship score", Desc: "The ship ranks objects using its scoring model and your priorities, with unfamiliar objects first. P1 Critical, P2 High, P3 Routine and P4 Low are priority groups within the current shore feed. Lowering an object's priority does not delete it.", W: map[string]float64{}, Lift: map[string]float64{}},
}

var liveExamples = []string{"ignore cargo ships", "prioritize submarines", "ignore birds and buoys", "prioritize unknown contacts"}

func NewConsole(b *Bridge) *Console {
	c := &Console{
		b:       b,
		base:    map[string]*command.Item{},
		overlay: map[string]*overlay{},
		dbIDs:   map[string]int{},
		classes: map[string]catalog.Class{},
		sources: map[string]catalog.Source{},
		subs:    map[chan sseEvent]struct{}{},
	}
	c.live.Prioritization = Prioritization{Mode: "edge", Label: "Ship score", Source: "default"}
	c.live.Policy.Readback = Readback(nil, nil)
	b.onSnapshot = c.onSnapshot
	return c
}
