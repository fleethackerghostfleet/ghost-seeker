package shorebridge

import (
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

// ---- wire model (what the browser sees) ----

type FormStatus struct {
	State    string `json:"state"` // none | offered | pending | partial | received
	Bytes    int64  `json:"bytes"`
	Received int64  `json:"received"`
}

type Card struct {
	item         *pb.Item
	ID           string                `json:"id"`
	Type         string                `json:"type"` // image | record | telemetry
	CapturedMs   int64                 `json:"capturedMs"`
	Source       string                `json:"source"`
	Label        string                `json:"label"`
	Conf         float64               `json:"conf"`
	Override     string                `json:"override,omitempty"`
	Unknownness  float64               `json:"unknownness"`
	Priority     string                `json:"priority"` // P1..P4, relative rank in the current shore picture (assignTiers)
	Score        float64               `json:"score"`
	Suppressed   bool                  `json:"suppressed"`
	SuppressedBy string                `json:"suppressedBy,omitempty"` // policy label that matched
	Tags         map[string]string     `json:"tags,omitempty"`
	Forms        map[string]FormStatus `json:"forms"`
	Image        string                `json:"image,omitempty"` // best available form URL
	ImageForm    string                `json:"imageForm,omitempty"`
	FactURL      string                `json:"factUrl"`
	Box          *pb.Box               `json:"box,omitempty"`
}

type Object struct {
	detectionMin float32
	Key          string  `json:"key"`
	SightingID   string  `json:"sightingId,omitempty"`
	Subtype      string  `json:"subtype,omitempty"`
	Event        string  `json:"event,omitempty"`
	Label        string  `json:"label"`
	Frames       int     `json:"frames"` // reports on shore, or n_frames from the edge's contact report when known
	FramesKnown  bool    `json:"framesKnown"`
	Reports      int     `json:"reports"`
	FirstMs      int64   `json:"firstMs"`
	LastMs       int64   `json:"lastMs"`
	Score        float64 `json:"score"`
	Priority     string  `json:"priority"`
	Best         *Card   `json:"best"`
	Items        []*Card `json:"items"`
	HasImage     bool    `json:"hasImage"`
	Unknown      bool    `json:"unknown"`
	Suppressed   bool    `json:"suppressed"`
	Scene        bool    `json:"scene"` // object-free context frame (tags.event == "scene"), not an object
}

type NodeStatus struct {
	MetaOnly       bool    `json:"metaOnly"`
	MetaOnlySince  int64   `json:"metaOnlySince,omitempty"`
	MetaOnlyReason string  `json:"metaOnlyReason,omitempty"`
	OK             bool    `json:"ok"`
	ItemsTotal     int64   `json:"itemsTotal"`
	Deduped        int64   `json:"itemsDeduped"`          // edge: captures folded into an existing item (hash/sighting duplicates)
	Prioritizer    string  `json:"prioritizer,omitempty"` // edge: scoring backend driving the queue (edgeml | tomer)
	Evicted        int64   `json:"itemsEvicted"`
	ItemsUnique    int64   `json:"itemsComplete"`
	Unknown        int64   `json:"itemsUnknown"`
	Bytes          int64   `json:"bytes"`
	Queued         int64   `json:"queuedItems"`
	QueuedBytes    int64   `json:"queuedBytes"`
	LinkUp         bool    `json:"linkUp"`
	Failures       int64   `json:"consecutiveFailures"`
	EstimateBps    float64 `json:"estimateBps"`
	PolicyID       string  `json:"policyId"`
	LastSeenMs     int64   `json:"lastSeenMs"`
	Error          string  `json:"error,omitempty"`
}

type Snapshot struct {
	TS         int64            `json:"ts"`
	Shore      NodeStatus       `json:"shore"`
	Edge       NodeStatus       `json:"edge"`
	Link       LinkStatus       `json:"link"`
	Policy     string           `json:"policy"`
	PolicyID   string           `json:"policyId,omitempty"`
	Emcon      string           `json:"emcon,omitempty"`
	Suppress   []string         `json:"suppress,omitempty"`
	Boost      []string         `json:"boost,omitempty"`
	TierLabels map[string]int32 `json:"tierLabels,omitempty"`
	PolicyInfo *PolicyInfo      `json:"policyInfo,omitempty"`
	Objects    []*Object        `json:"objects"`
	Counts     Counts           `json:"counts"`
}

type PolicyInfo struct {
	Version       int64            `json:"version"`
	Emcon         string           `json:"emcon"`
	EmconSetBy    string           `json:"emconSetBy,omitempty"`
	EmconLocked   bool             `json:"emconLocked"`
	EmconLockedBy string           `json:"emconLockedBy,omitempty"`
	Locks         []PolicyLock     `json:"locks"`
	Conflicts     []PolicyConflict `json:"conflicts"`
}

type PolicyLock struct {
	RuleID   string   `json:"ruleId"`
	Labels   []string `json:"labels"`
	LockedBy string   `json:"lockedBy"`
}

type PolicyConflict struct {
	RuleID    string `json:"ruleId"`
	Reason    string `json:"reason"`
	BlockedBy string `json:"blockedBy"`
}

type LinkStatus struct {
	Controllable bool                       `json:"controllable"`
	Bps          int64                      `json:"bps"`
	Bytes        int64                      `json:"bytes"`
	Distribution *shaper.NormalDistribution `json:"distribution,omitempty"`
	Fixed        bool                       `json:"fixed,omitempty"`
}

type Counts struct {
	Items              int `json:"items"`
	ItemsWithImage     int `json:"itemsWithImage"`
	Objects            int `json:"objects"`
	ObjectsWithImage   int `json:"objectsWithImage"`
	Grouped            int `json:"grouped"`   // items carrying a sighting_id
	Scenes             int `json:"scenes"`    // object-free "scene" context tiles
	Sightings          int `json:"sightings"` // objects with a sighting_id
	SightingsWithImage int `json:"sightingsWithImage"`
}
