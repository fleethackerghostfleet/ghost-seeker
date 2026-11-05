package shorebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

// Console serves the operator console's API (what web/static/app.js and
// cdr.js speak: /api/config, /api/stream, /api/items/{id}, /api/directives*)
// on live shore data, so the same console runs on the simulation and on a
// shore node. Items are command.Item like the simulation's, one per grouped
// object (sighting); the shore-only facts travel in Item.Live. Operator state
// the shore does not keep (manual tier, reviewed, dismissed, sent to the
// commander, the commander's decision) is an in-memory overlay keyed by object.
type Console struct {
	b *Bridge

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

type sseEvent struct {
	name string
	data []byte
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
	ID        string           `json:"id,omitempty"`
	Version   int64            `json:"version"`
	Text      string           `json:"text"`
	Suppress  []string         `json:"suppress"`
	Boost     []string         `json:"boost"`
	Readback  string           `json:"readback"`
	Admin     bool             `json:"admin"`
	NodeID    string           `json:"nodeId"`
	EMCON     EmconSummary     `json:"emcon"`
	Locks     []PolicyLock     `json:"locks"`
	Conflicts []PolicyConflict `json:"conflicts"`
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
	liveModel: {Name: "Edge priority score", Desc: "Ranked on the ship by the edge scorer (unknown-first, policy boost/suppress). P1–P4 are rank bands within the current shore picture; the standing directive deprioritizes, it never deletes.", W: map[string]float64{}, Lift: map[string]float64{}},
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
	c.live.Prioritization = Prioritization{Mode: "edge", Label: "Edge priority score", Source: "default"}
	c.live.Policy.Readback = Readback(nil, nil)
	b.onSnapshot = c.onSnapshot
	return c
}

// ---- building items ----

func (c *Console) onSnapshot(snap *Snapshot, pol *pb.Policy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.base
	c.base, c.order = map[string]*command.Item{}, nil
	c.policy = c.policyFrom(snap, pol)
	// Once the edge tracks sightings, per-frame items without a sighting_id
	// (the first frames before the tracker locked on) are duplicates of a
	// sighting card and are folded away; they still count in Counts.Objects.
	sightingMode := snap.Counts.Grouped > 0
	objects := make([]*Object, 0, len(snap.Objects))
	for _, o := range snap.Objects {
		if o.Best == nil || o.Scene || (sightingMode && o.SightingID == "") {
			continue
		}
		objects = append(objects, o)
	}
	scores := scoreObjects(objects)
	for _, o := range objects {
		item := c.buildItem(o, scores[o])
		c.base[item.ID] = item
		c.order = append(c.order, item.ID)
	}
	c.live = LiveStatus{
		TS: snap.TS, Shore: snap.Shore, Edge: snap.Edge, Link: snap.Link, Counts: snap.Counts, Policy: c.policy,
		Cameras: c.cameras(snap.Objects), Prioritization: prioritizationMode(snap), Classes: c.classes, Sources: c.sourceList(),
	}
	c.ready = true

	key := strings.Join(c.order, ",") + "|" + c.policy.Readback + c.policy.Text + "|" + fmt.Sprint(len(c.classes), len(c.sources))
	if key != c.lastKey {
		c.lastKey = key
		c.publish("snapshot", c.snapshotLocked())
		return
	}
	cdr := c.cdrLocked()
	for _, id := range c.order {
		before, after := prev[id], c.base[id]
		if before == nil || !sameItem(before, after) {
			c.publish("update", map[string]any{"item": c.withOverlay(after), "cdr": cdr})
		}
	}
	c.publish("live", c.live)
}

func sameItem(a, b *command.Item) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

type objectScore struct {
	edge, central, base   float64
	centralTier, baseTier int
}

// scoreObjects turns the shore's unbounded priority scores into the console's
// 0..1 scores by rank: Edge is the raw score normalized, Base the rank with
// suppression ignored, Central the rank the shore actually uses (suppressed
// objects sink to the bottom band but stay on the board).
func scoreObjects(objs []*Object) map[*Object]objectScore {
	out := make(map[*Object]objectScore, len(objs))
	maxScore := 0.0
	for _, o := range objs {
		maxScore = math.Max(maxScore, o.Best.Score)
	}
	byScore := append([]*Object(nil), objs...)
	sort.SliceStable(byScore, func(i, j int) bool { return byScore[i].Best.Score > byScore[j].Best.Score })
	ranked := make([]*Object, 0, len(objs))
	for _, o := range byScore {
		if !o.Suppressed {
			ranked = append(ranked, o)
		}
	}
	for i, o := range byScore {
		s := out[o]
		if maxScore > 0 {
			s.edge = o.Best.Score / maxScore
		}
		s.base = rankScore(i, len(byScore))
		s.baseTier = rankTier(i, len(byScore))
		s.central, s.centralTier = 0.05+0.1*s.edge, 4
		out[o] = s
	}
	for i, o := range ranked {
		s := out[o]
		s.central, s.centralTier = rankScore(i, len(ranked)), priorityTier(o.Priority, rankTier(i, len(ranked)))
		out[o] = s
	}
	return out
}

// priorityTier reads the P1..P4 band the bridge assigned over the whole
// shore picture (assignTiers), so a card shows the same tier the shore
// reports even when untracked contacts are folded out of the feed.
func priorityTier(priority string, fallback int) int {
	if len(priority) == 2 && priority[0] == 'P' && priority[1] >= '1' && priority[1] <= '4' {
		return int(priority[1] - '0')
	}
	return fallback
}

func rankScore(i, n int) float64 {
	if n <= 1 {
		return 0.96
	}
	return 0.96 - 0.86*float64(i)/float64(n-1)
}

// rankTier mirrors assignTiers' bands (top 10% P1, next 25% P2, next 35% P3).
func rankTier(i, n int) int {
	pos := float64(i+1) / float64(n)
	switch {
	case pos <= 0.10 || i == 0:
		return 1
	case pos <= 0.35:
		return 2
	case pos <= 0.70:
		return 3
	default:
		return 4
	}
}

func (c *Console) buildItem(o *Object, s objectScore) *command.Item {
	b := o.Best
	cls := b.Override
	if cls == "" {
		cls = o.Subtype
	}
	if cls == "" {
		cls = o.Label
	}
	if cls == "" {
		cls = "unknown"
	}
	cls = strings.ToLower(cls)
	c.ensureClass(cls)
	if o.Label != "" {
		c.ensureClass(strings.ToLower(o.Label))
	}
	c.ensureSource(b.Source)

	item := &command.Item{
		ID:          o.Key,
		DBID:        c.dbID(o.Key),
		Frame:       o.Frames,
		SourceID:    b.Source,
		TS:          time.UnixMilli(o.LastMs).UTC(),
		HZ:          .5,
		Edge:        round2(s.edge),
		Central:     round2(s.central),
		Base:        round2(s.base),
		CentralTier: s.centralTier, BaseTier: s.baseTier, Tier: s.centralTier,
		Lat:  command.Latency{},
		Dets: []command.Detection{},
		Det:  []command.DetectionScore{},
	}
	forms := map[string]command.LiveForm{}
	var received int64
	for name, f := range b.Forms {
		forms[name] = command.LiveForm{State: f.State, Bytes: f.Bytes, Received: f.Received}
		if f.State == "received" {
			received += f.Bytes
		} else if f.State == "partial" {
			received += f.Received
		}
	}
	item.KB = int(math.Ceil(float64(received) / 1024))
	if b.Image != "" {
		item.Img = strings.TrimPrefix(b.Image, "/") + fmt.Sprintf("?v=%d", b.Forms[b.ImageForm].Bytes)
	}
	live := &command.LiveMeta{
		ItemID: b.ID, SightingID: o.SightingID, Subtype: o.Subtype, Label: o.Label, Override: b.Override, Event: o.Event,
		Unknownness: b.Unknownness, Unknown: o.Unknown, Score: b.Score, Priority: o.Priority,
		Suppressed: o.Suppressed, Frames: o.Frames, FramesKnown: o.FramesKnown, Reports: o.Reports,
		FirstMs: o.FirstMs, LastMs: o.LastMs, Forms: forms, ImageForm: b.ImageForm, FactURL: b.FactURL,
		Pull: pullState(b.Forms["full"], false),
	}
	for _, card := range o.Items {
		if card.Suppressed && card.SuppressedBy != "" {
			live.SuppressedBy = card.SuppressedBy
			break
		}
	}
	item.Live = live

	det := command.Detection{Class: cls, Conf: b.Conf, Attrs: command.Attributes{}}
	if b.Box != nil && b.ImageForm != "crop" { // a crop has no frame to place the box in
		det.Box = command.Box{X: float64(b.Box.GetX()) * frameW, Y: float64(b.Box.GetY()) * frameH, W: float64(b.Box.GetW()) * frameW, H: float64(b.Box.GetH()) * frameH}
	}
	score := command.DetectionScore{Score: item.Central, Sup: o.Suppressed, Notes: []command.Rule{}}
	if o.Suppressed {
		label := live.SuppressedBy
		if label == "" {
			label = cls
		}
		score.Notes = append(score.Notes, command.Rule{Type: "suppress", Classes: []string{strings.ToLower(label)}, Attrs: []string{}, DirectiveID: "policy"})
	}
	if l := matchLabel(c.policy.Boost, b.Override, o.Subtype, o.Label); l != "" {
		score.Matched = true
		score.Notes = append(score.Notes, command.Rule{Type: "boost", Classes: []string{l}, Attrs: []string{}, DirectiveID: "policy"})
	}
	item.Dets = append(item.Dets, det)
	item.Det = append(item.Det, score)
	return item
}

func matchLabel(labels []string, candidates ...string) string {
	for _, l := range labels {
		for _, c := range candidates {
			if c != "" && strings.EqualFold(l, c) {
				return strings.ToLower(l)
			}
		}
	}
	return ""
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// pullState explains the "Pull full image" button for a form ladder.
func pullState(full FormStatus, scene bool) command.LivePull {
	what := "image"
	if scene {
		what = "frame"
	}
	switch full.State {
	case "received":
		return command.LivePull{Enabled: false, Label: "Full " + what + " received", Reason: fmt.Sprintf("FULL (%s) is on shore; nothing left to pull.", fmtBytes(full.Bytes))}
	case "partial":
		pct := 0
		if full.Bytes > 0 {
			pct = int(100 * full.Received / full.Bytes)
		}
		return command.LivePull{Enabled: false, Label: fmt.Sprintf("Full %s %d%%", what, pct), Reason: fmt.Sprintf("FULL is streaming over the link (%s of %s, resumable).", fmtBytes(full.Received), fmtBytes(full.Bytes))}
	case "pending":
		return command.LivePull{Enabled: false, Label: "Full " + what + " queued", Reason: "FULL is already queued on the edge; it lands when the link allows."}
	case "offered":
		if scene {
			return command.LivePull{Enabled: true, Label: "Pull full frame", Reason: fmt.Sprintf("The edge holds the FULL frame (%s) but scene frames never ship unless asked for; pulling requests it now.", fmtBytes(full.Bytes))}
		}
		return command.LivePull{Enabled: true, Label: "Pull full " + what, Reason: fmt.Sprintf("The edge offered FULL (%s) in its manifest but has not sent it; pulling asks the edge to send it now, ahead of the queue.", fmtBytes(full.Bytes))}
	default:
		return command.LivePull{Enabled: true, Label: "Request full " + what, Reason: "Shore has no FULL form for this item yet; pulling asks the edge to produce and send it."}
	}
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func (c *Console) dbID(key string) int {
	if id, ok := c.dbIDs[key]; ok {
		return id
	}
	id := 100000 + len(c.dbIDs) + 1
	c.dbIDs[key] = id
	return id
}

func (c *Console) ensureClass(label string) {
	if label == "" {
		return
	}
	if _, ok := catalog.Classes[label]; ok {
		return
	}
	if _, ok := c.classes[label]; ok {
		return
	}
	c.classes[label] = catalog.Class{Group: guessGroup(label), Label: titleCase(label)}
}

func guessGroup(label string) string {
	switch {
	case strings.Contains(label, "bird"), strings.Contains(label, "buoy"):
		return "clutter"
	case strings.Contains(label, "aircraft"), strings.Contains(label, "helicopter"), strings.Contains(label, "drone"), strings.Contains(label, "plane"):
		return "aircraft"
	case strings.Contains(label, "person"), strings.Contains(label, "people"), strings.Contains(label, "mammal"), strings.Contains(label, "whale"), strings.Contains(label, "dolphin"), strings.Contains(label, "debris"):
		return "other"
	case label == "unknown":
		return "unknown"
	default:
		return "vessel"
	}
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r, size := utf8.DecodeRuneInString(w)
		words[i] = strings.ToUpper(string(r)) + w[size:]
	}
	return strings.Join(words, " ")
}

func (c *Console) ensureSource(id string) {
	if id == "" {
		id = "camera"
	}
	if _, ok := c.sources[id]; ok {
		return
	}
	c.sources[id] = catalog.Source{
		ID: id, Name: strings.ReplaceAll(id, "_", " "), Scene: "open", Mode: "eo",
		Loc: "Ship camera · " + id, HW: "Edge node + inference sidecar", EM: "YOLO11n detector · MobileCLIP zero-shot subtype · sighting tracker",
		Link: "Ship→shore link (shaped)", Rate: "5 fps replay, one item per sighting",
	}
}

func (c *Console) sourceList() []catalog.Source {
	out := make([]catalog.Source, 0, len(c.sources))
	for _, s := range c.sources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// cameras builds the Cameras strip: one entry per source, showing the latest
// object-free heartbeat frame, else the newest contact's image.
func (c *Console) cameras(objs []*Object) []Camera {
	type agg struct {
		cam           Camera
		scene, latest *Object
	}
	cams := map[string]*agg{}
	for _, o := range objs {
		if o.Best == nil {
			continue
		}
		src := o.Best.Source
		if src == "" {
			src = "camera"
		}
		c.ensureSource(src)
		a := cams[src]
		if a == nil {
			a = &agg{cam: Camera{Source: src, Name: c.sources[src].Name}}
			cams[src] = a
		}
		if o.LastMs > a.cam.LastMs {
			a.cam.LastMs = o.LastMs
		}
		if o.Scene {
			if a.scene == nil || o.LastMs > a.scene.LastMs {
				a.scene = o
			}
		} else {
			a.cam.Contacts++
			if a.latest == nil || o.LastMs > a.latest.LastMs {
				a.latest = o
			}
		}
	}
	out := make([]Camera, 0, len(cams))
	for _, a := range cams {
		v := a.scene
		a.cam.Heartbeat = v != nil
		if v == nil {
			v = a.latest
		}
		if v != nil {
			b := v.Best
			view := &CameraView{ItemID: b.ID, ImageForm: b.ImageForm, LastMs: v.LastMs, Pull: pullState(b.Forms["full"], a.cam.Heartbeat)}
			if f, ok := b.Forms["full"]; ok {
				view.Full = command.LiveForm{State: f.State, Bytes: f.Bytes, Received: f.Received}
			}
			if b.Image != "" {
				view.Image = fmt.Sprintf("%s?v=%d", b.Image, b.Forms[b.ImageForm].Bytes)
			}
			a.cam.View = view
		}
		out = append(out, a.cam)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

func prioritizationMode(snap *Snapshot) Prioritization {
	mode := strings.ToLower(strings.TrimSpace(snap.Edge.Prioritizer))
	switch mode {
	case "tomer":
		return Prioritization{Mode: mode, Label: "Tomer prioritizer (algorithm/)", Source: "edge"}
	case "both":
		return Prioritization{Mode: mode, Label: "Edge priority score + Tomer prioritizer", Source: "edge"}
	case "edgeml":
		return Prioritization{Mode: mode, Label: "Edge priority score (edgeml)", Source: "edge"}
	case "":
		return Prioritization{Mode: "edge", Label: "Edge priority score", Source: "default"}
	}
	return Prioritization{Mode: mode, Label: "Edge prioritizer: " + mode, Source: "edge"}
}

func (c *Console) policyFrom(snap *Snapshot, pol *pb.Policy) LivePolicy {
	p := LivePolicy{
		ID: snap.PolicyID, Text: snap.Policy, Suppress: dedupe(snap.Suppress), Boost: dedupe(snap.Boost),
		NodeID: "shore", EMCON: EmconSummary{Mode: "NORMAL"}, Locks: []PolicyLock{}, Conflicts: []PolicyConflict{},
	}
	p.Readback = Readback(p.Suppress, p.Boost)
	if pol == nil {
		return p
	}
	info := policyInfo(pol) // same merge/lock view as the F2 strip (#57)
	p.Version, p.ExpiresAtUnixMs = info.Version, pol.GetExpiresAtUnixMs()
	p.EMCON = EmconSummary{Mode: info.Emcon, SetBy: info.EmconSetBy, Locked: info.EmconLocked, LockedBy: info.EmconLockedBy}
	p.Locks, p.Conflicts = info.Locks, info.Conflicts
	if p.ExpiresAtUnixMs > 0 {
		until := time.UnixMilli(p.ExpiresAtUnixMs).UTC().Format("15:04Z")
		p.Readback = strings.Replace(p.Readback, "Until cleared.", "Until "+until+".", 1)
	}
	return p
}

func emconLabel(mode pb.EmconMode) string {
	switch mode {
	case pb.EmconMode_EMCON_MODE_BURST:
		return "BURST"
	case pb.EmconMode_EMCON_MODE_SILENT:
		return "SILENT"
	default:
		return "NORMAL"
	}
}

func dedupe(labels []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range labels {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// ---- snapshot / overlay ----

func (c *Console) withOverlay(base *command.Item) command.Item {
	item := *base
	if o := c.overlay[item.ID]; o != nil {
		item.Manual, item.Reviewed, item.Dismissed, item.Sent, item.SentAt = o.Manual, o.Reviewed, o.Dismissed, o.Sent, o.SentAt
		item.Decision, item.DecisionNote, item.DecidedAt = o.Decision, o.DecisionNote, o.DecidedAt
		item.FullRes, item.FullResBy, item.FullResAt = o.FullRes, o.FullResBy, o.FullResAt
	}
	if item.Manual > 0 {
		item.Tier = item.Manual
	}
	return item
}

func (c *Console) itemsLocked() []command.Item {
	out := make([]command.Item, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.withOverlay(c.base[id]))
	}
	return out
}

func (c *Console) cdrLocked() int {
	n := 0
	for id, o := range c.overlay {
		if o.Sent && c.base[id] != nil {
			n++
		}
	}
	return n
}

func (c *Console) directivesLocked() []command.Directive {
	rules := policyRules(c.policy.Suppress, c.policy.Boost)
	if len(rules) == 0 {
		return []command.Directive{}
	}
	text := c.policy.Text
	if strings.TrimSpace(text) == "" {
		text = c.policy.Readback
	}
	return []command.Directive{{ID: "policy", Text: text, Rules: rules}}
}

func policyRules(suppress, boost []string) []command.Rule {
	rules := []command.Rule{}
	for _, l := range boost {
		rules = append(rules, command.Rule{Type: "boost", Classes: []string{l}, Attrs: []string{}, DirectiveID: "policy"})
	}
	for _, l := range suppress {
		rules = append(rules, command.Rule{Type: "suppress", Classes: []string{l}, Attrs: []string{}, DirectiveID: "policy"})
	}
	return rules
}

func (c *Console) statsLocked() command.Stats {
	e, s := c.live.Edge, c.live.Shore
	bytes := s.Bytes
	if c.live.Link.Controllable {
		bytes = c.live.Link.Bytes
	}
	return command.Stats{Cap: int(e.ItemsTotal + e.Deduped), Keep: int(e.ItemsTotal), Drop: int(e.Deduped), Bytes: int(bytes), DB: int(s.ItemsTotal)}
}

func (c *Console) snapshotLocked() map[string]any {
	return map[string]any{
		"state":   store.State{Model: liveModel, Directives: c.directivesLocked()},
		"stats":   c.statsLocked(),
		"cdr":     c.cdrLocked(),
		"items":   c.itemsLocked(),
		"weights": map[string]any{},
		"policy":  c.policy,
		"live":    c.live,
	}
}

func (c *Console) publish(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("console: marshal %s: %v", name, err)
		return
	}
	for ch := range c.subs {
		select {
		case ch <- sseEvent{name, data}:
		default:
		}
	}
}

// ---- HTTP ----

func (c *Console) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", c.config)
	mux.HandleFunc("GET /api/stream", c.stream)
	mux.HandleFunc("PATCH /api/items/{id}", c.patchItem)
	mux.HandleFunc("POST /api/items/{id}/decision", c.decide)
	mux.HandleFunc("POST /api/items/{id}/full-res", c.fullRes)
	mux.HandleFunc("POST /api/items/{id}/pull", c.pull)
	mux.HandleFunc("GET /api/directives", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		d := c.directivesLocked()
		c.mu.Unlock()
		writeJSON(w, d)
	})
	mux.HandleFunc("POST /api/directives", c.addDirective)
	mux.HandleFunc("DELETE /api/directives", c.clearDirectives)
	mux.HandleFunc("DELETE /api/directives/{id}", c.clearDirectives)
	mux.HandleFunc("POST /api/directives/preview", c.preview)
	mux.HandleFunc("POST /api/directives/compile", c.compile)
	mux.HandleFunc("PUT /api/model", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusConflict, "Live shore mode: the edge scorer ranks the feed; there is no base model to switch.")
	})
	mux.HandleFunc("GET /api/policy", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		p := c.policy
		c.mu.Unlock()
		writeJSON(w, map[string]any{"policy": p})
	})
	mux.HandleFunc("POST /api/policy/emcon", c.b.setEMCON)
	unsupported := func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusConflict, "Live shore mode: raw policy editing is done on the shore node (policy-keygen / shore admin), not from the console.")
	}
	mux.HandleFunc("POST /api/policy/validate", unsupported)
	mux.HandleFunc("PUT /api/policy", unsupported)
	mux.HandleFunc("GET /api/policy/audit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"entries": []any{}})
	})
	return mux
}

func (c *Console) config(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	writeJSON(w, map[string]any{
		"sources": c.sourceList(), "classes": c.allClasses(), "models": liveModels,
		"attrLabels": catalog.AttrLabels, "examples": liveExamples, "live": true,
	})
}

func (c *Console) allClasses() map[string]catalog.Class {
	out := make(map[string]catalog.Class, len(catalog.Classes)+len(c.classes))
	for k, v := range catalog.Classes {
		out[k] = v
	}
	for k, v := range c.classes {
		out[k] = v
	}
	return out
}

func (c *Console) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan sseEvent, 64)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	first, _ := json.Marshal(c.snapshotLocked())
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.subs, ch)
		c.mu.Unlock()
	}()
	send := func(name string, data []byte) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send("snapshot", first) {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if !send(ev.name, ev.data) {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func (c *Console) patchItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Manual    *int  `json:"manual"`
		Reviewed  *bool `json:"reviewed"`
		Dismissed *bool `json:"dismissed"`
		Sent      *bool `json:"sent"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Manual != nil && (*body.Manual < 0 || *body.Manual > 4) {
		writeError(w, http.StatusBadRequest, "manual must be 0..4")
		return
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	if base == nil {
		c.mu.Unlock()
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	o := c.overlayFor(id)
	if body.Manual != nil {
		o.Manual = *body.Manual
	}
	if body.Reviewed != nil {
		o.Reviewed = *body.Reviewed
	}
	if body.Dismissed != nil {
		o.Dismissed = *body.Dismissed
	}
	if body.Sent != nil && o.Sent != *body.Sent {
		if *body.Sent {
			now := time.Now()
			o.SentAt, o.Decision, o.DecisionNote, o.DecidedAt = &now, "", "", nil
		} else {
			o.SentAt = nil
		}
		o.Sent = *body.Sent
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	c.mu.Unlock()
	writeJSON(w, map[string]any{"item": item, "cdr": cdr})
}

func (c *Console) overlayFor(id string) *overlay {
	o := c.overlay[id]
	if o == nil {
		o = &overlay{}
		c.overlay[id] = o
	}
	return o
}

func (c *Console) decide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Decision != "" && !store.Decisions[body.Decision] {
		writeError(w, http.StatusBadRequest, store.ErrUnknownDecision.Error())
		return
	}
	note := []rune(strings.TrimSpace(body.Note))
	if len(note) > 280 {
		note = note[:280]
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	if base == nil {
		c.mu.Unlock()
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	o := c.overlayFor(id)
	if !o.Sent && o.Decision != store.DecisionReturn {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, store.ErrNotEscalated.Error())
		return
	}
	now := time.Now()
	switch {
	case body.Decision == store.DecisionReturn && o.Sent:
		o.Sent, o.SentAt, o.Reviewed, o.Dismissed = false, nil, false, false
	case body.Decision != store.DecisionReturn && !o.Sent:
		o.Sent, o.SentAt, o.Reviewed = true, &now, true
	}
	o.Decision, o.DecisionNote, o.DecidedAt = body.Decision, string(note), &now
	if body.Decision == "" {
		o.DecisionNote, o.DecidedAt = "", nil
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	c.mu.Unlock()
	writeJSON(w, map[string]any{"item": item, "cdr": cdr})
}

// fullRes is the operator override to send the original image at full size,
// no re-encoding: RequestPull{original:true} to the shore for the card's item.
func (c *Console) fullRes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		By string `json:"by"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	by := strings.TrimSpace(body.By)
	if by == "" {
		writeError(w, http.StatusBadRequest, store.ErrOperatorRequired.Error())
		return
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	c.mu.Unlock()
	if base == nil || base.Live == nil {
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: base.Live.ItemID, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: by, Original: true}}
	var out pb.RequestPullResponse
	if err := c.b.call(r.Context(), c.b.shore, "RequestPull", req, &out); err != nil {
		log.Printf("console: full-res pull upstream: %v", err)
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	c.b.Poke()
	c.mu.Lock()
	o := c.overlayFor(id)
	if !o.FullRes {
		now := time.Now()
		o.FullRes, o.FullResBy, o.FullResAt = true, by, &now
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	c.mu.Unlock()
	writeJSON(w, map[string]any{"item": item, "cdr": cdr})
}

// pull asks the edge for one more form of the card's shore item (FULL by
// default): the fidelity ladder's "give me the rest" action.
func (c *Console) pull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Form string `json:"form"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := r.PathValue("id")
	c.mu.Lock()
	base := c.base[id]
	c.mu.Unlock()
	if base == nil || base.Live == nil {
		writeError(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	kind := pb.FormKind_FORM_KIND_FULL
	for k, n := range formNames {
		if n == body.Form {
			kind = k
		}
	}
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: base.Live.ItemID, Form: kind, RequestedBy: "operator-ui"}}
	var out pb.RequestPullResponse
	if err := c.b.call(r.Context(), c.b.shore, "RequestPull", req, &out); err != nil {
		log.Printf("console: pull upstream: %v", err)
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	c.b.Poke()
	writeJSON(w, map[string]any{"ok": true, "itemId": base.Live.ItemID, "form": formNames[kind]})
}

// rulesFromBody turns a directive (free text or explicit rules) into the
// shore's suppress/boost label lists.
func rulesFromBody(text string, rules *[]command.Rule) (suppress, boost []string, parsed []command.Rule) {
	if rules != nil && len(*rules) > 0 {
		for _, r := range *rules {
			labels := r.Classes
			if len(labels) == 0 {
				labels = r.Attrs
			}
			switch r.Type {
			case "suppress":
				suppress = append(suppress, labels...)
			default:
				boost = append(boost, labels...)
			}
		}
	} else if strings.TrimSpace(text) != "" {
		suppress, boost = Interpret(text)
	}
	suppress, boost = dedupe(suppress), dedupe(boost)
	return suppress, boost, policyRules(suppress, boost)
}

// compiled runs the shared NL compiler (#54) when the bridge has one, so the
// console gets the same readback/warnings/guardrails as the simulation. The
// edge scorer only knows flat class labels, so scoped rules are dropped with a
// warning; EMCON phrases become Policy.Emcon. Without a compiler it falls
// back to the shore interpreter.
type compiledDirective struct {
	Suppress, Boost []string
	Rules           []command.Rule
	Compiled        *command.Compiled
	Emcon           pb.EmconMode
	DurationMin     int
	Readback        string
	Warnings        []string
	Blocked         bool
	Path            string
}

const scopeWarning = "Not applied in shore mode: source, attribute and time scopes are not supported by the edge scorer."

// flatRules keeps the rules the edge scorer can honour (plain class labels)
// and reports whether any scoped rule was dropped.
func flatRules(rules []command.Rule) (kept []command.Rule, dropped bool) {
	kept = make([]command.Rule, 0, len(rules))
	for _, rule := range rules {
		if len(rule.Sources) == 0 && len(rule.Attrs) == 0 && rule.WithinMin == 0 {
			kept = append(kept, rule)
		} else {
			dropped = true
		}
	}
	return kept, dropped
}

func (c *Console) compileDirective(ctx context.Context, text string, rules *[]command.Rule, durationMin int) compiledDirective {
	var out compiledDirective
	out.Path = "rules"
	out.Warnings = []string{}
	mode, ruleText := command.SplitEmcon(text)
	if mode != "" {
		out.Emcon = pb.EmconMode(pb.EmconMode_value["EMCON_MODE_"+mode])
	}
	if rules != nil && len(*rules) > 0 {
		kept, dropped := flatRules(*rules)
		out.Suppress, out.Boost, out.Rules = rulesFromBody("", &kept)
		out.Path = "edited"
		out.DurationMin = durationMin
		if dropped {
			out.Warnings = append(out.Warnings, scopeWarning)
		}
		out.Readback = Readback(out.Suppress, out.Boost)
	} else if c.b.Compiler != nil && strings.TrimSpace(ruleText) != "" {
		comp := c.b.Compiler.Compile(ctx, ruleText)
		if !comp.Blocked {
			kept, dropped := flatRules(comp.Rules)
			if dropped {
				prior, path := comp.Warnings, comp.Path
				comp = c.b.Compiler.Review(ruleText, kept, comp.DurationMin)
				comp.Path = path
				comp.Warnings = mergeWarnings(prior, comp.Warnings, []string{scopeWarning})
			}
		}
		out.Suppress, out.Boost = rulesToLabels(comp.Rules)
		out.Suppress, out.Boost = dedupe(out.Suppress), dedupe(out.Boost)
		out.Rules = policyRules(out.Suppress, out.Boost)
		out.Compiled = &comp
		out.Path, out.Warnings, out.Blocked, out.DurationMin = comp.Path, comp.Warnings, comp.Blocked, comp.DurationMin
		out.Readback = strings.TrimSpace(comp.Readback)
		if len(out.Rules) == 0 && !out.Blocked && mode == "" && !strings.Contains(strings.Join(comp.Warnings, "\n"), "Not applied") {
			out.Suppress, out.Boost = Interpret(ruleText)
			out.Suppress, out.Boost = dedupe(out.Suppress), dedupe(out.Boost)
			out.Rules = policyRules(out.Suppress, out.Boost)
			if len(out.Rules) > 0 {
				out.Readback = Readback(out.Suppress, out.Boost)
			}
		}
	} else {
		out.Suppress, out.Boost, out.Rules = rulesFromBody(ruleText, nil)
		out.Readback = Readback(out.Suppress, out.Boost)
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if out.DurationMin == 0 && durationMin > 0 {
		out.DurationMin = durationMin
	}
	out.Readback = withDuration(out.Readback, out.DurationMin)
	if mode != "" {
		out.Readback = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(out.Readback, "No rules recognized."), "Feed ranked by the edge priority score only.") + " EMCON " + mode + ".")
	}
	return out
}

// withDuration swaps the interpreter's open-ended "Until cleared." for the
// requested expiry so the readback says what the policy will actually do.
func withDuration(readback string, durationMin int) string {
	if durationMin <= 0 || !strings.Contains(readback, "Until cleared.") {
		return readback
	}
	return strings.Replace(readback, "Until cleared.", fmt.Sprintf("For %dm.", durationMin), 1)
}

func (c *Console) addDirective(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text        string          `json:"text"`
		Rules       *[]command.Rule `json:"rules"`
		DurationMin int             `json:"durationMin"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if utf8.RuneCountInString(body.Text) > 500 {
		writeError(w, http.StatusBadRequest, "text exceeds 500 characters")
		return
	}
	if body.DurationMin < 0 || body.DurationMin > 10080 {
		writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
		return
	}
	cd := c.compileDirective(r.Context(), body.Text, body.Rules, body.DurationMin)
	if cd.Blocked {
		msg := "directive is blocked"
		if cd.Compiled != nil {
			msg = blockedMessage(*cd.Compiled)
		}
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	suppress, boost, rules := cd.Suppress, cd.Boost, cd.Rules
	if len(rules) == 0 && cd.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		writeError(w, http.StatusUnprocessableEntity, "Could not map that to known objects — try \"ignore cargo ships\" or \"prioritize submarines\".")
		return
	}
	c.mu.Lock()
	cur := c.policy
	c.mu.Unlock()
	text := body.Text
	if cur.Text != "" && text != "" {
		text = cur.Text + "; " + text
	} else if text == "" {
		text = cur.Text
	}
	merged := mergePolicy(cur, suppress, boost)
	pol := &pb.Policy{IssuedBy: "operator-ui", Text: text, Interest: &pb.Interest{SuppressLabels: merged.Suppress, BoostLabels: merged.Boost}, Emcon: cd.Emcon}
	if cd.DurationMin > 0 {
		pol.ExpiresAtUnixMs = time.Now().Add(time.Duration(cd.DurationMin) * time.Minute).UnixMilli()
	}
	out, err := c.b.applyPolicyProto(r.Context(), pol)
	if err != nil {
		log.Printf("console: set policy upstream: %v", err)
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	readback := withDuration(Readback(merged.Suppress, merged.Boost), cd.DurationMin)
	if cd.Compiled != nil || cd.Emcon != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		readback = cd.Readback
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"id": "policy", "text": text, "rules": rules, "readback": readback, "warnings": cd.Warnings, "path": cd.Path, "compiled": cd.Compiled,
		"suppressedCount": out.GetSuppressedCount(), "boostedCount": out.GetBoostedCount(), "conflicts": conflictList(out.GetConflicts()),
	})
}

func conflictList(conflicts []*pb.MergeConflict) []PolicyConflict {
	out := []PolicyConflict{}
	for _, c := range conflicts {
		out = append(out, PolicyConflict{RuleID: c.GetRuleId(), Reason: c.GetReason(), BlockedBy: c.GetBlockedBy()})
	}
	return out
}

// mergePolicy adds labels to the standing policy; a label cannot be both
// boosted and suppressed, the newest directive wins.
func mergePolicy(cur LivePolicy, suppress, boost []string) LivePolicy {
	drop := func(from []string, remove []string) []string {
		out := []string{}
		for _, l := range from {
			if matchLabel(remove, l) == "" {
				out = append(out, l)
			}
		}
		return out
	}
	next := LivePolicy{}
	next.Suppress = dedupe(append(drop(cur.Suppress, boost), suppress...))
	next.Boost = dedupe(append(drop(cur.Boost, suppress), boost...))
	return next
}

func (c *Console) clearDirectives(w http.ResponseWriter, r *http.Request) {
	if _, err := c.b.applyPolicy(r.Context(), "", nil, nil); err != nil {
		log.Printf("console: clear policy upstream: %v", err)
		writeError(w, http.StatusBadGateway, "upstream shore error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type previewItem struct {
	ID      string  `json:"id"`
	Central float64 `json:"central"`
	Tier    int     `json:"tier"`
	Contrib []any   `json:"contrib"`
}

type candidateResult struct {
	Key     string `json:"key"`
	Counts  [4]int `json:"counts"`
	Changed int    `json:"changed"`
}

// predict applies draft rules on top of the standing policy to the current
// items: a newly suppressed object sinks to P4 (still listed), a newly boosted
// one rises a band. It is the live stand-in for the simulation's rescoring.
func (c *Console) predictLocked(suppress, boost []string) ([]previewItem, [4]int, int) {
	var counts [4]int
	changed := 0
	out := make([]previewItem, 0, len(c.order))
	for _, id := range c.order {
		item := c.withOverlay(c.base[id])
		live := item.Live
		tier, central := item.Tier, item.Central
		if item.Manual == 0 {
			switch {
			case !live.Unknown && matchLabel(suppress, live.Override, live.Subtype, live.Label) != "":
				tier, central = 4, round2(0.05+0.1*item.Edge)
			case live.Suppressed && matchLabel(suppress, live.Override, live.Subtype, live.Label) == "" && matchLabel(c.policy.Suppress, live.Override, live.Subtype, live.Label) != "":
				tier, central = item.BaseTier, item.Base // the draft lifts the standing suppression
			}
			if matchLabel(boost, live.Override, live.Subtype, live.Label) != "" && tier > 1 && (!live.Suppressed || matchLabel(suppress, live.Override, live.Subtype, live.Label) == "") {
				tier--
				central = round2(math.Min(0.98, central+0.2))
			}
		}
		if tier != item.Tier {
			changed++
		}
		counts[tier-1]++
		out = append(out, previewItem{ID: id, Central: central, Tier: tier, Contrib: []any{}})
	}
	return out, counts, changed
}

func (c *Console) preview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text       *string         `json:"text"`
		Rules      *[]command.Rule `json:"rules"`
		Candidates []struct {
			Key   string         `json:"key"`
			Rules []command.Rule `json:"rules"`
		} `json:"candidates"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	text := ""
	if body.Text != nil {
		text = *body.Text
	}
	suppress, boost, rules := rulesFromBody(text, body.Rules)
	c.mu.Lock()
	defer c.mu.Unlock()
	items, counts, _ := c.predictLocked(suppress, boost)
	cands := []candidateResult{}
	for _, cand := range body.Candidates {
		rs := cand.Rules
		s, b, _ := rulesFromBody("", &rs)
		_, cc, changed := c.predictLocked(dedupe(append(append([]string{}, suppress...), s...)), dedupe(append(append([]string{}, boost...), b...)))
		cands = append(cands, candidateResult{Key: cand.Key, Counts: cc, Changed: changed})
	}
	writeJSON(w, map[string]any{"rules": rules, "unknown": unknownWords(text, suppress, boost), "items": items, "counts": counts, "candidates": cands})
}

// unknownWords lists the directive's nouns the shore interpreter did not map.
func unknownWords(text string, suppress, boost []string) []string {
	out := []string{}
	if strings.TrimSpace(text) == "" || len(suppress)+len(boost) > 0 {
		return out
	}
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ",.;:!?\"'")
		switch w {
		case "", "ignore", "suppress", "prioritize", "prioritise", "boost", "focus", "on", "and", "the", "all", "any", "ships", "ship", "contacts":
			continue
		}
		out = append(out, w)
	}
	return out
}

func (c *Console) compile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text        string          `json:"text"`
		Rules       *[]command.Rule `json:"rules"`
		DurationMin int             `json:"durationMin"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.DurationMin < 0 || body.DurationMin > 10080 {
		writeError(w, http.StatusBadRequest, "durationMin must be between 0 and 10080")
		return
	}
	cd := c.compileDirective(r.Context(), body.Text, body.Rules, body.DurationMin)
	warnings := cd.Warnings
	if len(cd.Rules) == 0 && !cd.Blocked && cd.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		warnings = mergeWarnings(warnings, []string{"Nothing recognized — the shore interpreter maps object names after ignore/suppress or prioritize/boost."})
	}
	unknown := unknownWords(body.Text, cd.Suppress, cd.Boost)
	if cd.Compiled != nil && cd.Compiled.Unknown != nil {
		unknown = cd.Compiled.Unknown
	}
	writeJSON(w, map[string]any{
		"rules": cd.Rules, "unknown": unknown, "durationMin": cd.DurationMin, "path": cd.Path,
		"readback": cd.Readback, "warnings": warnings, "blocked": cd.Blocked,
	})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
