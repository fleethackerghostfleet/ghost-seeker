// Package shorebridge backs the operator web app with a live shore node:
// it polls the shore ConsoleService (ListItems/GetStatus/GetPolicy), groups
// items into objects by features.tags.sighting_id, and publishes snapshots
// over SSE. Operator actions are forwarded to shore (SetPolicy, RequestPull)
// and the link rate to the in-process shaper.
package shorebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

const consolePath = "/ghostseeker.v1.ConsoleService/"

// maxRecordFetchesPerRefresh bounds the extra /blob/{id}/fact reads one poll
// may do for closed sightings' contact reports, so a burst of closes cannot
// stall the snapshot.
const maxRecordFetchesPerRefresh = 25

// LinkControl is what the UI needs from a link: the current rate and a way
// to change it. *shaper.Shaper satisfies it; nil means "read-only".
type LinkControl interface {
	Bps() int64
	Bytes() int64
	SetBps(int64)
}

type Bridge struct {
	token       string
	shore, edge string
	link        LinkControl
	client      *http.Client
	// Compiler, when set, turns a free-text directive into rules (local LLM
	// with the rules parser as fallback) and gives the operator a readback;
	// nil keeps the simple Interpret path.
	Compiler *command.Compiler

	mu         sync.Mutex
	snap       []byte
	subs       map[chan []byte]struct{}
	records    map[string]contactReport // RECORD item id -> its contact report (immutable, fetched once)
	operator   string
	edgeNodeID string

	poke       chan struct{}               // asks Run for an immediate refresh (after an operator action)
	nextExpiry time.Time                   // earliest timed rule/EMCON/policy deadline seen by the poller
	onSnapshot func(*Snapshot, *pb.Policy) // Console hook, called with every fresh snapshot and the policy it was built from
}

func New(shoreURL, edgeURL string, link LinkControl) *Bridge {
	return &Bridge{
		shore:    strings.TrimRight(shoreURL, "/"),
		edge:     strings.TrimRight(edgeURL, "/"),
		link:     link,
		client:   &http.Client{Timeout: 10 * time.Second},
		subs:     map[chan []byte]struct{}{},
		records:  map[string]contactReport{},
		poke:     make(chan struct{}, 1),
		operator: DefaultOperator,
	}
}

// Poke asks the poller to refresh now instead of at the next tick.
func (b *Bridge) Poke() {
	select {
	case b.poke <- struct{}{}:
	default:
	}
}

func (b *Bridge) issuer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.operator
}

func (b *Bridge) echelon() pb.Echelon {
	return pb.Echelon_ECHELON_SHIP
}

// DefaultOperator is the issuer recorded on policies this console edits when
// no -operator-id is configured.
const DefaultOperator = "operator-ui"

// WithOperator sets the identity recorded as IssuedBy / rule Author / EMCON
// SetBy on policies this console submits. It is server configuration (the
// authenticated console instance), never taken from the browser request.
func (b *Bridge) WithOperator(id string) *Bridge {
	if id = strings.TrimSpace(id); id != "" {
		b.mu.Lock()
		b.operator = id
		b.mu.Unlock()
	}
	return b
}

// ---- wire model (what the browser sees) ----

type FormStatus struct {
	State    string `json:"state"` // none | offered | pending | partial | received
	Bytes    int64  `json:"bytes"`
	Received int64  `json:"received"`
}

type Card struct {
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
	Key         string  `json:"key"`
	SightingID  string  `json:"sightingId,omitempty"`
	Subtype     string  `json:"subtype,omitempty"`
	Event       string  `json:"event,omitempty"`
	Label       string  `json:"label"`
	Frames      int     `json:"frames"` // reports on shore, or n_frames from the edge's contact report when known
	FramesKnown bool    `json:"framesKnown"`
	Reports     int     `json:"reports"`
	FirstMs     int64   `json:"firstMs"`
	LastMs      int64   `json:"lastMs"`
	Priority    string  `json:"priority"`
	Best        *Card   `json:"best"`
	Items       []*Card `json:"items"`
	HasImage    bool    `json:"hasImage"`
	Unknown     bool    `json:"unknown"`
	Suppressed  bool    `json:"suppressed"`
	Scene       bool    `json:"scene"` // object-free context frame (tags.event == "scene"), not an object
}

type NodeStatus struct {
	OK          bool    `json:"ok"`
	ItemsTotal  int64   `json:"itemsTotal"`
	Deduped     int64   `json:"itemsDeduped"`          // edge: captures folded into an existing item (hash/sighting duplicates)
	Prioritizer string  `json:"prioritizer,omitempty"` // edge: scoring backend driving the queue (edgeml | tomer)
	Evicted     int64   `json:"itemsEvicted"`
	ItemsUnique int64   `json:"itemsComplete"`
	Unknown     int64   `json:"itemsUnknown"`
	Bytes       int64   `json:"bytes"`
	Queued      int64   `json:"queuedItems"`
	QueuedBytes int64   `json:"queuedBytes"`
	LinkUp      bool    `json:"linkUp"`
	Failures    int64   `json:"consecutiveFailures"`
	EstimateBps float64 `json:"estimateBps"`
	PolicyID    string  `json:"policyId"`
	LastSeenMs  int64   `json:"lastSeenMs"`
	Error       string  `json:"error,omitempty"`
}

type Snapshot struct {
	TS         int64       `json:"ts"`
	Shore      NodeStatus  `json:"shore"`
	Edge       NodeStatus  `json:"edge"`
	Link       LinkStatus  `json:"link"`
	Policy     string      `json:"policy"`
	PolicyID   string      `json:"policyId,omitempty"`
	Emcon      string      `json:"emcon,omitempty"`
	Suppress   []string    `json:"suppress,omitempty"`
	Boost      []string    `json:"boost,omitempty"`
	PolicyInfo *PolicyInfo `json:"policyInfo,omitempty"`
	Objects    []*Object   `json:"objects"`
	Counts     Counts      `json:"counts"`
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
	Controllable bool    `json:"controllable"`
	Bps          int64   `json:"bps"`
	Bytes        int64   `json:"bytes"`
	Presets      []int64 `json:"presets"`
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

// ---- console RPC helpers ----

// WithToken sets the operator bearer token the bridge presents to the shore
// and edge consoles (and to shore blobs it proxies).
func (b *Bridge) WithToken(token string) *Bridge {
	b.token = token
	return b
}

func (b *Bridge) authorize(r *http.Request) {
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
}

func (b *Bridge) call(ctx context.Context, base, method string, req, resp proto.Message) error {
	body, err := protojson.Marshal(req)
	if err != nil {
		return err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, base+consolePath+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hr.Header.Set("Content-Type", "application/json")
	b.authorize(hr)
	res, err := b.client.Do(hr)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return &consoleCallError{method: method, statusCode: res.StatusCode, status: res.Status,
			body: strings.TrimSpace(string(raw))}
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, resp)
}

type consoleCallError struct {
	method     string
	statusCode int
	status     string
	body       string
}

func (e *consoleCallError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.method, e.status, e.body)
}

func (e *consoleCallError) message() string {
	var response struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(e.body), &response); err == nil {
		if response.Message != "" {
			return response.Message
		}
		if response.Error != "" {
			return response.Error
		}
	}
	if e.body != "" {
		return e.body
	}
	return http.StatusText(e.statusCode)
}

func writeShoreAuthError(w http.ResponseWriter, err error) bool {
	var upstream *consoleCallError
	if !errors.As(err, &upstream) || (upstream.statusCode != http.StatusUnauthorized &&
		upstream.statusCode != http.StatusForbidden) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(upstream.statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": upstream.message()})
	return true
}

// ---- polling ----

func (b *Bridge) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		b.refresh(ctx)
		// A timed directive or EMCON lapsing is refreshed as it happens, so the
		// console (and its SSE stream) drops it without waiting for the next
		// tick or an operator action.
		var lapse <-chan time.Time
		var timer *time.Timer
		b.mu.Lock()
		next := b.nextExpiry
		b.mu.Unlock()
		if !next.IsZero() {
			timer = time.NewTimer(time.Until(next) + 100*time.Millisecond)
			lapse = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-t.C:
		case <-b.poke:
		case <-lapse:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (b *Bridge) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	snap := &Snapshot{TS: time.Now().UnixMilli()}
	snap.Link.Presets = shaper.Presets
	if b.link != nil {
		snap.Link = LinkStatus{Controllable: true, Bps: b.link.Bps(), Bytes: b.link.Bytes(), Presets: shaper.Presets}
	}

	var st pb.GetStatusResponse
	if err := b.call(ctx, b.shore, "GetStatus", &pb.GetStatusRequest{}, &st); err != nil {
		log.Printf("shorebridge: shore status: %v", err)
		snap.Shore.Error = "upstream shore error"
	} else {
		snap.Shore = nodeStatus(&st)
		if b.link == nil {
			snap.Link.Bps = int64(st.GetLink().GetEstimateBps())
			snap.Link.Bytes = st.GetBytesSent()
		}
	}
	var pol pb.GetPolicyResponse
	var suppressed []string
	var storedPolicy *pb.Policy
	if err := b.call(ctx, b.shore, "GetPolicy", &pb.GetPolicyRequest{}, &pol); err == nil {
		storedPolicy = pol.GetPolicy()
	}
	if b.edge != "" {
		var est pb.GetStatusResponse
		if err := b.call(ctx, b.edge, "GetStatus", &pb.GetStatusRequest{}, &est); err != nil {
			log.Printf("shorebridge: edge status: %v", err)
			snap.Edge.Error = "upstream edge error"
		} else {
			snap.Edge = nodeStatus(&est)
			if nodeID := est.GetNodeId(); nodeID != "" {
				b.mu.Lock()
				b.edgeNodeID = nodeID
				b.mu.Unlock()
			}
		}
	}
	now := time.Now()
	currentPolicy := viewPolicy(storedPolicy, b.currentEdgeID(), now)
	b.mu.Lock()
	b.nextExpiry = nextPolicyExpiry(storedPolicy, now)
	b.mu.Unlock()
	if currentPolicy != nil {
		snap.Policy = currentPolicy.GetText()
		if len(currentPolicy.Layers) > 0 {
			snap.Policy = directivesText(b.directives(currentPolicy, now))
		}
		snap.Emcon = emconName(currentPolicy.GetEmcon())
		snap.PolicyID = currentPolicy.GetId()
		suppressed = currentPolicy.GetInterest().GetSuppressLabels()
		snap.Suppress = suppressed
		snap.Boost = currentPolicy.GetInterest().GetBoostLabels()
		snap.PolicyInfo = policyInfo(currentPolicy, b.echelon())
	}

	var items []*pb.ItemView
	token := ""
	for page := 0; page < 20; page++ {
		var lr pb.ListItemsResponse
		if err := b.call(ctx, b.shore, "ListItems", &pb.ListItemsRequest{Limit: 500, PageToken: token}, &lr); err != nil {
			log.Printf("shorebridge: shore list items: %v", err)
			if snap.Shore.Error == "" {
				snap.Shore.Error = "upstream shore error"
			}
			break
		}
		items = append(items, lr.Items...)
		token = lr.NextPageToken
		if token == "" || len(lr.Items) == 0 {
			break
		}
	}
	snap.Objects, snap.Counts = Group(items, suppressed, b.recordReports(ctx, items))
	if b.onSnapshot != nil {
		b.onSnapshot(snap, currentPolicy)
	}

	buf, err := json.Marshal(snap)
	if err != nil {
		log.Printf("shorebridge: marshal: %v", err)
		return
	}
	b.mu.Lock()
	b.snap = buf
	for ch := range b.subs {
		select {
		case ch <- buf:
		default:
		}
	}
	b.mu.Unlock()
}

// recordReports reads each closed sighting's contact report (RECORD items,
// FACT complete): n_frames, and the sighting_id/label/subtype that let Group
// fold the report into its sighting when the item carries no tags itself.
// Facts are immutable, so they are fetched once per item.
func (b *Bridge) recordReports(ctx context.Context, items []*pb.ItemView) map[string]contactReport {
	out := map[string]contactReport{}
	fetched := 0
	for _, v := range items {
		it := v.GetItem()
		if it.GetType() != pb.ItemType_ITEM_TYPE_RECORD {
			continue
		}
		done := false
		for _, fs := range v.GetFormStates() {
			if fs.GetKind() == pb.FormKind_FORM_KIND_FACT && fs.GetComplete() {
				done = true
			}
		}
		if !done {
			continue
		}
		b.mu.Lock()
		rep, ok := b.records[it.GetId()]
		b.mu.Unlock()
		if !ok {
			if fetched >= maxRecordFetchesPerRefresh { // the rest are picked up by later polls
				continue
			}
			fetched++
			// Cache failures too (empty) so an unreadable report is tried once
			// and cannot starve later ones of their fetch budget.
			rep = b.fetchRecord(ctx, it.GetId())
			b.mu.Lock()
			b.records[it.GetId()] = rep
			b.mu.Unlock()
		}
		if rep.NFrames > 0 || rep.SightingID != "" {
			out[it.GetId()] = rep
		}
	}
	return out
}

func (b *Bridge) fetchRecord(ctx context.Context, id string) contactReport {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, b.shore+"/blob/"+id+"/fact", nil)
	if err != nil {
		return contactReport{}
	}
	b.authorize(hr)
	res, err := b.client.Do(hr)
	if err != nil {
		return contactReport{}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return contactReport{}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return contactReport{}
	}
	var rep contactReport
	// The fact may be the report itself or a fact envelope carrying it; try both.
	if json.Unmarshal(raw, &rep) != nil || (rep.NFrames == 0 && rep.SightingID == "") {
		var env struct {
			Record json.RawMessage `json:"record"`
			Data   json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) == nil {
			for _, r := range [][]byte{env.Record, env.Data} {
				if len(r) > 0 && json.Unmarshal(r, &rep) == nil && (rep.NFrames > 0 || rep.SightingID != "") {
					break
				}
			}
		}
	}
	return rep
}

func nodeStatus(st *pb.GetStatusResponse) NodeStatus {
	n := NodeStatus{
		OK:          true,
		ItemsTotal:  st.GetItemsTotal(),
		Deduped:     st.GetItemsDeduped(),
		Prioritizer: st.GetPrioritizer(),
		Evicted:     st.GetItemsEvicted(),
		ItemsUnique: st.GetItemsComplete(),
		Unknown:     st.GetItemsUnknown(),
		Bytes:       st.GetBytesSent(),
		Queued:      st.GetHeartbeat().GetQueuedItems(),
		QueuedBytes: st.GetHeartbeat().GetQueuedBytes(),
		LinkUp:      st.GetLink().GetUp(),
		Failures:    st.GetLink().GetConsecutiveFailures(),
		EstimateBps: st.GetLink().GetEstimateBps(),
		PolicyID:    st.GetPolicyId(),
		LastSeenMs:  st.GetLink().GetLastSuccessUnixMs(),
	}
	if n.EstimateBps == 0 {
		n.EstimateBps = float64(st.GetHeartbeat().GetLinkEstimateBps())
	}
	return n
}

// ---- grouping ----

var formNames = map[pb.FormKind]string{
	pb.FormKind_FORM_KIND_FACT:  "fact",
	pb.FormKind_FORM_KIND_THUMB: "thumb",
	pb.FormKind_FORM_KIND_CROP:  "crop",
	pb.FormKind_FORM_KIND_FULL:  "full",
}

// assignTiers maps the edge's unbounded priority score onto the P1..P4
// tiers the operator UI uses by rank within the current shore picture:
// top 10% P1, next 25% P2, next 35% P3, rest P4. Suppressed objects are P4.
func assignTiers(objs []*Object) {
	ranked := make([]*Object, 0, len(objs))
	for _, o := range objs {
		if o.Best != nil && !o.Suppressed && !o.Scene {
			ranked = append(ranked, o)
		} else {
			o.Priority = "P4"
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Best.Score > ranked[j].Best.Score })
	n := float64(len(ranked))
	for i, o := range ranked {
		pos := float64(i+1) / n
		switch {
		case pos <= 0.10 || i == 0:
			o.Priority = "P1"
		case pos <= 0.35:
			o.Priority = "P2"
		case pos <= 0.70:
			o.Priority = "P3"
		default:
			o.Priority = "P4"
		}
	}
	for _, o := range objs {
		for _, c := range o.Items {
			c.Priority = o.Priority
		}
	}
}

// ToCard flattens an ItemView into what a card needs, picking the best
// received image form (crop > thumb > full).
func ToCard(v *pb.ItemView) *Card {
	it := v.GetItem()
	c := &Card{
		ID:          it.GetId(),
		Type:        itemTypeName(it.GetType()),
		CapturedMs:  it.GetCapturedAtUnixMs(),
		Source:      it.GetSource(),
		Unknownness: float64(it.GetFeatures().GetUnknownness()),
		Override:    it.GetFeatures().GetLabelOverride(),
		Score:       v.GetScore().GetPriority(),
		Suppressed:  v.GetScore().GetSuppressed(),
		Tags:        it.GetFeatures().GetTags(),
		Forms:       map[string]FormStatus{},
		FactURL:     "/blob/" + it.GetId() + "/fact",
	}
	if !ids.ValidName(c.Source) {
		c.Source = ""
	}
	for _, n := range formNames {
		c.Forms[n] = FormStatus{State: "none"}
	}
	for _, f := range it.GetForms() {
		if n, ok := formNames[f.GetKind()]; ok {
			c.Forms[n] = FormStatus{State: "offered", Bytes: f.GetBytes()}
		}
	}
	for _, fs := range v.GetFormStates() {
		n, ok := formNames[fs.GetKind()]
		if !ok {
			continue
		}
		st := FormStatus{Bytes: fs.GetBytes(), Received: fs.GetAckedThrough()}
		switch {
		case fs.GetComplete():
			st.State = "received"
		case fs.GetAckedThrough() > 0:
			st.State = "partial"
		default:
			st.State = "pending"
		}
		c.Forms[n] = st
	}
	if dets := it.GetFeatures().GetDetections(); len(dets) > 0 {
		best := dets[0]
		for _, d := range dets[1:] {
			if d.GetConfidence() > best.GetConfidence() {
				best = d
			}
		}
		c.Label, c.Conf, c.Box = best.GetLabel(), float64(best.GetConfidence()), best.GetBox()
	}
	if c.Label == "" && c.Unknownness > 0.5 {
		c.Label = "unknown"
	}
	prefer := []string{"crop", "thumb", "full"}
	if c.Tags["event"] == "scene" { // no object to crop: a pulled FULL beats the thumb
		prefer = []string{"full", "thumb"}
	}
	for _, n := range prefer {
		if c.Forms[n].State == "received" {
			c.Image, c.ImageForm = "/blob/"+c.ID+"/"+n, n
			break
		}
	}
	return c
}

func itemTypeName(t pb.ItemType) string {
	switch t {
	case pb.ItemType_ITEM_TYPE_RECORD:
		return "record"
	case pb.ItemType_ITEM_TYPE_TELEMETRY:
		return "telemetry"
	default:
		return "image"
	}
}

// contactReport is the edge's CLOSE record for a sighting (edge/ingest.ContactReport).
type contactReport struct {
	SightingID string `json:"sighting_id"`
	Label      string `json:"label"`
	Subtype    string `json:"subtype"`
	NFrames    int    `json:"n_frames"`
}

// Group buckets cards by sighting_id (one object per sighting; items without
// a tag are their own object), newest first. Items whose label is in the
// current policy's suppress list are marked suppressed even if they were
// scored before the policy reached the edge.
// reports maps RECORD item id -> its contact report (may be nil); a report
// whose item carries no sighting_id tag is folded into the sighting the
// report names, so a closed sighting never shows up as a second, empty card.
func Group(views []*pb.ItemView, suppressLabels []string, reports map[string]contactReport) ([]*Object, Counts) {
	var cnt Counts
	byKey := map[string]*Object{}
	var order []*Object
	for _, v := range views {
		c := ToCard(v)
		if rep, ok := reports[c.ID]; ok && c.Type == "record" && c.Tags["sighting_id"] == "" && ids.ValidSightingID(rep.SightingID) {
			tags := map[string]string{"sighting_id": rep.SightingID}
			for k, val := range c.Tags {
				tags[k] = val
			}
			if rep.Subtype != "" {
				tags["subtype"] = rep.Subtype
			}
			c.Tags = tags
			if c.Label == "" {
				c.Label = rep.Label
			}
		}
		for _, l := range suppressLabels {
			// Mirrors edge/scoring: a negative interest never suppresses an unknown.
			if c.Unknownness >= unknownThreshold {
				continue
			}
			// Detector class, operator override or the sidecar's zero-shot subtype.
			if strings.EqualFold(l, c.Label) || (c.Override != "" && strings.EqualFold(l, c.Override)) || strings.EqualFold(l, c.Tags["subtype"]) {
				c.Suppressed, c.SuppressedBy = true, l
			}
		}
		cnt.Items++
		if c.Image != "" {
			cnt.ItemsWithImage++
		}
		key := "item:" + c.ID
		sid := c.Tags["sighting_id"]
		if !ids.ValidSightingID(sid) {
			sid = ""
		}
		if sid != "" {
			key = "sighting:" + sid
			cnt.Grouped++
		}
		o := byKey[key]
		if o == nil {
			o = &Object{Key: key, SightingID: sid, FirstMs: c.CapturedMs, LastMs: c.CapturedMs}
			byKey[key] = o
			order = append(order, o)
		}
		o.Items = append(o.Items, c)
		o.Scene = o.Scene || c.Tags["event"] == "scene"
		o.Reports++
		if !o.FramesKnown {
			o.Frames = o.Reports
		}
		if c.Type == "record" && c.Forms["fact"].State == "received" && reports != nil {
			if rep, ok := reports[c.ID]; ok && rep.NFrames >= o.Frames && rep.NFrames > 0 {
				o.Frames, o.FramesKnown = rep.NFrames, true
			}
		}
		if c.CapturedMs < o.FirstMs {
			o.FirstMs = c.CapturedMs
		}
		if c.CapturedMs >= o.LastMs {
			o.LastMs = c.CapturedMs
			if st := c.Tags["subtype"]; st != "" {
				o.Subtype = st
			}
			if ev := c.Tags["event"]; ev != "" {
				o.Event = ev
			}
		}
		if n, ok := c.Tags["frames"]; ok {
			var fr int
			if _, err := fmt.Sscanf(n, "%d", &fr); err == nil && fr > o.Frames {
				o.Frames, o.FramesKnown = fr, true
			}
		}
		o.Suppressed = o.Suppressed || c.Suppressed
		o.Unknown = o.Unknown || c.Unknownness >= unknownThreshold
		if o.Unknown { // an object the edge flagged unknown is never deprioritized by a directive
			o.Suppressed = false
			for _, it := range o.Items {
				it.Suppressed, it.SuppressedBy = false, ""
			}
		}
		if o.Best == nil || better(c, o.Best) {
			o.Best = c
		}
	}
	for _, o := range order {
		if o.Best != nil {
			o.Label = o.Best.Label
			if o.Best.Override != "" {
				o.Label = o.Best.Override
			}
			o.HasImage = o.Best.Image != ""
		}
		sort.Slice(o.Items, func(i, j int) bool { return o.Items[i].CapturedMs > o.Items[j].CapturedMs })
		if o.Scene {
			cnt.Scenes++
			continue
		}
		cnt.Objects++
		if o.HasImage {
			cnt.ObjectsWithImage++
		}
		if o.SightingID != "" {
			cnt.Sightings++
			if o.HasImage {
				cnt.SightingsWithImage++
			}
		}
	}
	assignTiers(order)
	sort.SliceStable(order, func(i, j int) bool { return order[i].LastMs > order[j].LastMs })
	return order, cnt
}

// unknownThreshold matches the Object.Unknown flag and edge/scoring's notion of an unknown contact.
const unknownThreshold = 0.5

var formRank = map[string]int{"": 0, "thumb": 1, "full": 2, "crop": 3}

func better(a, b *Card) bool {
	if (a.Type == "record") != (b.Type == "record") {
		return b.Type == "record"
	}
	if formRank[a.ImageForm] != formRank[b.ImageForm] {
		return formRank[a.ImageForm] > formRank[b.ImageForm]
	}
	return a.CapturedMs > b.CapturedMs
}

// ---- HTTP ----

func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/shore/snapshot", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		snap := b.snap
		b.mu.Unlock()
		if snap == nil {
			snap = []byte(`{"objects":[],"shore":{"ok":false,"error":"no data yet"}}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(snap)
	})
	mux.HandleFunc("GET /api/shore/stream", b.stream)
	mux.HandleFunc("POST /api/shore/policy", b.setPolicy)
	mux.HandleFunc("POST /api/shore/emcon", b.setEMCON)
	mux.HandleFunc("POST /api/shore/pull", b.requestPull)
	mux.HandleFunc("POST /api/shore/override", b.override)
	mux.HandleFunc("GET /api/shore/link", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, b.linkStatus()) })
	mux.HandleFunc("POST /api/shore/link", b.setLink)
	if u, err := url.Parse(b.shore); err == nil {
		rp := httputil.NewSingleHostReverseProxy(u)
		director := rp.Director
		// The browser's session cookie stays on this origin; the shore sees
		// the operator bearer token instead.
		rp.Director = func(r *http.Request) {
			director(r)
			r.Host = u.Host
			r.Header.Del("Cookie")
			r.Header.Del("Authorization")
			b.authorize(r)
		}
		rp.ModifyResponse = func(res *http.Response) error {
			if res.Header.Get("Content-Security-Policy") == "" {
				res.Header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
			}
			res.Header.Set("X-Content-Type-Options", "nosniff")
			return nil
		}
		mux.Handle("GET /blob/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Outer middleware already set nosniff; the upstream copy would duplicate it.
			w.Header().Del("X-Content-Type-Options")
			rp.ServeHTTP(w, r)
		}))
	}
	return mux
}

func (b *Bridge) linkStatus() LinkStatus {
	ls := LinkStatus{Presets: shaper.Presets}
	if b.link != nil {
		ls.Controllable, ls.Bps, ls.Bytes = true, b.link.Bps(), b.link.Bytes()
	}
	return ls
}

func (b *Bridge) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan []byte, 4)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	last := b.snap
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}()
	send := func(buf []byte) {
		fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", buf)
		fl.Flush()
	}
	if last != nil {
		send(last)
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case buf := <-ch:
			send(buf)
		}
	}
}

type policyBody struct {
	Text     string   `json:"text"`
	Suppress []string `json:"suppress"`
	Boost    []string `json:"boost"`
}

// setPolicy forwards an operator directive to shore. A free-text directive is
// compiled with the NL compiler when one is configured (readback, warnings and
// path "llm"|"rules" are returned alongside the policy), otherwise parsed with
// Interpret (e.g. "ignore cargo ships" -> suppress cargo ship).
func (b *Bridge) setPolicy(w http.ResponseWriter, r *http.Request) {
	var in policyBody
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: policy request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(in.Text) > 500 {
		http.Error(w, "text exceeds 500 characters", http.StatusBadRequest)
		return
	}
	issuedBy := opauth.Actor(r.Context(), b.issuer())
	mode, ruleText := command.SplitEmcon(in.Text)
	pol := &pb.Policy{IssuedBy: issuedBy, Text: in.Text, Interest: &pb.Interest{SuppressLabels: in.Suppress, BoostLabels: in.Boost}}
	durationMin := command.ExtractDurationMin(in.Text)
	if mode != "" {
		pol.Emcon = pb.EmconMode(pb.EmconMode_value["EMCON_MODE_"+mode])
	}
	var compiled *command.Compiled
	if len(in.Suppress) == 0 && len(in.Boost) == 0 && in.Text != "" {
		droppedScopedRules := false
		if b.Compiler != nil {
			c := b.Compiler.Compile(r.Context(), ruleText)
			if c.Blocked {
				http.Error(w, blockedMessage(c), http.StatusUnprocessableEntity)
				return
			}
			if durationMin == 0 {
				durationMin = c.DurationMin
			}
			if c.DurationMin == 0 && durationMin > 0 {
				c.DurationMin = durationMin
			}
			kept := make([]command.Rule, 0, len(c.Rules))
			for _, rule := range c.Rules {
				if len(rule.Sources) == 0 && len(rule.Attrs) == 0 && rule.WithinMin == 0 {
					kept = append(kept, rule)
				} else {
					droppedScopedRules = true
				}
			}
			if droppedScopedRules {
				priorWarnings := c.Warnings
				path := c.Path
				c = b.Compiler.Review(ruleText, kept, c.DurationMin)
				c.Path = path
				c.Warnings = mergeWarnings(priorWarnings, c.Warnings, []string{
					"Not applied in shore mode: source, attribute and time scopes are not supported by the edge scorer.",
				})
				if c.Blocked {
					http.Error(w, blockedMessage(c), http.StatusUnprocessableEntity)
					return
				}
			}
			pol.Interest.SuppressLabels, pol.Interest.BoostLabels = rulesToLabels(c.Rules)
			compiled = &c
		}
		if len(pol.Interest.SuppressLabels) == 0 && len(pol.Interest.BoostLabels) == 0 {
			if !droppedScopedRules && pol.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
				pol.Interest.SuppressLabels, pol.Interest.BoostLabels = Interpret(ruleText)
				compiled = nil
			}
		}
	}
	if durationMin > 0 {
		pol.ExpiresAtUnixMs = time.Now().Add(time.Duration(durationMin) * time.Minute).UnixMilli()
	}
	directiveSuppress, directiveBoost := pol.Interest.SuppressLabels, pol.Interest.BoostLabels
	warnings, err := b.layerPolicy(r.Context(), pol, mode, true)
	if err != nil {
		log.Printf("shorebridge: set policy: %v", err)
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	if compiled != nil {
		compiled.Warnings = append(compiled.Warnings, warnings...)
		compiled.Readback = emconReadback(compiled.Readback, mode, durationMin)
	}
	pol.Id, pol.Version, pol.Signature, pol.Conflicts, pol.IssuedAtUnixMs = "", 0, nil, nil, 0
	var out pb.SetPolicyResponse
	if err := b.call(r.Context(), b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		log.Printf("shorebridge: set policy upstream: %v", err)
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	raw, _ := protojson.Marshal(&out)
	var policy map[string]any
	if json.Unmarshal(raw, &policy) != nil {
		policy = map[string]any{}
	}
	readback := Readback(directiveSuppress, directiveBoost)
	if compiled != nil && compiled.Readback != "" {
		readback = compiled.Readback
	}
	if rb, ok := policy["readback"].(string); ok && rb != "" { // a shore that compiles directives itself wins
		readback = rb
	}
	readback = emconReadback(readback, mode, durationMin)
	resp := struct {
		Policy   json.RawMessage   `json:"policy"`
		Compiled *command.Compiled `json:"compiled,omitempty"`
		Readback string            `json:"readback"`
	}{Policy: raw, Compiled: compiled, Readback: readback}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func emconReadback(readback, mode string, durationMin int) string {
	if mode == "" {
		return readback
	}
	phrase := "EMCON " + mode
	if durationMin > 0 {
		phrase += fmt.Sprintf(" for %d min", durationMin)
	}
	phrase += "."
	if strings.Contains(readback, phrase) {
		return readback
	}
	readback = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(readback), "No rules recognized."))
	if readback == "" {
		return phrase
	}
	return readback + " " + phrase
}

// rulesToLabels flattens compiled rules into the flat boost/suppress label
// lists the edge scorer matches against (classes only; attributes have no
// edge-side equivalent yet).
func rulesToLabels(rules []command.Rule) (suppress, boost []string) {
	seen := map[string]bool{}
	for _, rule := range rules {
		for _, class := range rule.Classes {
			key := rule.Type + "/" + class
			if class == "" || seen[key] {
				continue
			}
			seen[key] = true
			switch rule.Type {
			case "suppress":
				suppress = append(suppress, class)
			case "boost":
				boost = append(boost, class)
			}
		}
	}
	return suppress, boost
}

func mergeWarnings(groups ...[]string) []string {
	merged := make([]string, 0)
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, warning := range group {
			if !seen[warning] {
				seen[warning] = true
				merged = append(merged, warning)
			}
		}
	}
	return merged
}

func blockedMessage(c command.Compiled) string {
	for _, warning := range c.Warnings {
		if strings.HasPrefix(warning, "Blocked") {
			return warning
		}
	}
	if len(c.Warnings) > 0 {
		return c.Warnings[0]
	}
	return "directive is blocked"
}

// applyPolicy replaces the standing shore policy with the given interest and
// asks the poller to refresh so the console sees the change at once.
func (b *Bridge) applyPolicy(ctx context.Context, text string, suppress, boost []string) (*pb.SetPolicyResponse, error) {
	return b.applyPolicyAs(ctx, opauth.Actor(ctx, b.issuer()), text, suppress, boost)
}

func (b *Bridge) applyPolicyAs(ctx context.Context, issuedBy, text string, suppress, boost []string) (*pb.SetPolicyResponse, error) {
	if issuedBy == "" {
		issuedBy = opauth.Actor(ctx, b.issuer())
	}
	if suppress == nil {
		suppress = []string{}
	}
	if boost == nil {
		boost = []string{}
	}
	return b.applyPolicyProto(ctx, &pb.Policy{IssuedBy: issuedBy, Text: text, Interest: &pb.Interest{SuppressLabels: suppress, BoostLabels: boost}})
}

// applyPolicyProto sends a fully built policy (interest, EMCON, expiry) to the
// shore and pokes the poller.
func (b *Bridge) applyPolicyProto(ctx context.Context, pol *pb.Policy) (*pb.SetPolicyResponse, error) {
	mode := ""
	if pol.Emcon != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		mode = emconName(pol.Emcon)
	}
	if _, err := b.layerPolicy(ctx, pol, mode, true); err != nil {
		return nil, err
	}
	var out pb.SetPolicyResponse
	if err := b.call(ctx, b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		return nil, err
	}
	b.Poke()
	return &out, nil
}

// cachedEmcon is the EMCON label of the last snapshot the poller built ("" if none).
func (b *Bridge) cachedEmcon() string {
	b.mu.Lock()
	raw := b.snap
	b.mu.Unlock()
	var snap struct {
		Emcon string `json:"emcon"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &snap) != nil {
		return ""
	}
	return snap.Emcon
}

func (b *Bridge) currentEdgeID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.edgeNodeID
}

func (b *Bridge) setEMCON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	mode, ok := parseEMCON(in.Mode)
	if !ok {
		http.Error(w, "mode must be NORMAL, BURST, or SILENT", http.StatusBadRequest)
		return
	}
	issuedBy := opauth.Actor(r.Context(), b.issuer())
	current, err := b.getPolicy(r.Context())
	if err != nil {
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if current != nil {
		current = policy.Flatten(current, b.currentEdgeID(), time.Now())
	}
	if current == nil {
		current = &pb.Policy{Interest: &pb.Interest{}}
	}
	if setting := current.EmconSetting; setting != nil && setting.Locked && setting.Echelon > b.echelon() {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]string{"error": "EMCON locked by " + setting.SetBy})
		return
	}
	pol := proto.Clone(current).(*pb.Policy)
	pol.IssuedBy = issuedBy
	if len(pol.Layers) > 0 {
		pol.Layers = cloneLayers(pol.Layers)
		operatorLayer := findOperatorLayer(pol.Layers, issuedBy, b.echelon())
		if operatorLayer < 0 {
			for i, layer := range pol.Layers {
				if layer != nil && layer.Echelon == b.echelon() {
					operatorLayer = i
					break
				}
			}
			if operatorLayer < 0 {
				pol.Layers = append(pol.Layers, &pb.PolicyLayer{Echelon: b.echelon(), IssuedBy: issuedBy})
				operatorLayer = len(pol.Layers) - 1
			}
		}
		pol.Layers[operatorLayer].Rules = withoutEmconOnlyRules(pol.Layers[operatorLayer].Rules)
		pol.Layers[operatorLayer].Emcon = &pb.EmconSetting{Mode: mode, SetBy: issuedBy, Echelon: b.echelon()}
	} else {
		rules := make([]*pb.InterestRule, 0, 2)
		if pol.Interest != nil && len(pol.Interest.BoostLabels) > 0 {
			var weight float64
			if pol.Interest.BoostFactor > 0 {
				weight = pol.Interest.BoostFactor
			}
			rules = append(rules, &pb.InterestRule{Id: fmt.Sprintf("ui-%d-boost", time.Now().UnixMilli()), Author: issuedBy,
				BoostLabels: append([]string(nil), pol.Interest.BoostLabels...), Weight: weight})
		}
		if pol.Interest != nil && len(pol.Interest.SuppressLabels) > 0 {
			rules = append(rules, &pb.InterestRule{Id: fmt.Sprintf("ui-%d-suppress", time.Now().UnixMilli()), Author: issuedBy,
				SuppressLabels: append([]string(nil), pol.Interest.SuppressLabels...)})
		}
		pol.Layers = []*pb.PolicyLayer{{
			Echelon: b.echelon(), IssuedBy: issuedBy, Rules: rules,
			Emcon: &pb.EmconSetting{Mode: mode, SetBy: issuedBy, Echelon: b.echelon()},
		}}
	}
	pol.Id, pol.Version, pol.Signature, pol.Conflicts, pol.IssuedAtUnixMs = "", 0, nil, nil, 0
	var out pb.SetPolicyResponse
	if err := b.call(r.Context(), b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		if writeShoreAuthError(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	raw, _ := protojson.Marshal(&out)
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (b *Bridge) getPolicy(ctx context.Context) (*pb.Policy, error) {
	var response pb.GetPolicyResponse
	if err := b.call(ctx, b.shore, "GetPolicy", &pb.GetPolicyRequest{}, &response); err != nil {
		return nil, err
	}
	return response.Policy, nil
}

func findOperatorLayer(layers []*pb.PolicyLayer, issuedBy string, echelon pb.Echelon) int {
	for i, layer := range layers {
		if layer != nil && layer.Echelon == echelon && layer.IssuedBy == issuedBy {
			return i
		}
	}
	return -1
}

func cloneLayers(layers []*pb.PolicyLayer) []*pb.PolicyLayer {
	out := make([]*pb.PolicyLayer, 0, len(layers))
	for _, layer := range layers {
		if layer == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, proto.Clone(layer).(*pb.PolicyLayer))
	}
	return out
}

func parseEMCON(value string) (pb.EmconMode, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "NORMAL":
		return pb.EmconMode_EMCON_MODE_NORMAL, true
	case "BURST":
		return pb.EmconMode_EMCON_MODE_BURST, true
	case "SILENT":
		return pb.EmconMode_EMCON_MODE_SILENT, true
	default:
		return pb.EmconMode_EMCON_MODE_UNSPECIFIED, false
	}
}

func policyInfo(p *pb.Policy, operatorEchelon ...pb.Echelon) *PolicyInfo {
	echelon := pb.Echelon_ECHELON_SHIP
	if len(operatorEchelon) > 0 {
		echelon = operatorEchelon[0]
	}
	info := &PolicyInfo{Version: p.Version, Emcon: emconName(p.Emcon), Locks: []PolicyLock{}, Conflicts: []PolicyConflict{}}
	if p.EmconSetting != nil {
		info.EmconSetBy = p.EmconSetting.SetBy
		if p.EmconSetting.Mode != pb.EmconMode_EMCON_MODE_UNSPECIFIED {
			info.Emcon = emconName(p.EmconSetting.Mode)
		}
		if p.EmconSetting.Locked && p.EmconSetting.Echelon > echelon {
			info.EmconLocked = true
			info.EmconLockedBy = p.EmconSetting.SetBy
		}
	}
	for _, lock := range policy.Locks(p.Layers) {
		if !lock.Emcon {
			info.Locks = append(info.Locks, PolicyLock{RuleID: lock.RuleID, Labels: append([]string(nil), lock.BoostLabels...), LockedBy: lock.LockedBy})
		}
	}
	for _, conflict := range p.Conflicts {
		if conflict != nil {
			info.Conflicts = append(info.Conflicts, PolicyConflict{RuleID: conflict.RuleId, Reason: conflict.Reason, BlockedBy: conflict.BlockedBy})
		}
	}
	return info
}

func emconName(mode pb.EmconMode) string {
	switch mode {
	case pb.EmconMode_EMCON_MODE_BURST:
		return "BURST"
	case pb.EmconMode_EMCON_MODE_SILENT:
		return "SILENT"
	default:
		return "NORMAL"
	}
}

func (b *Bridge) requestPull(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"itemId"`
		Form   string `json:"form"`
		// Original is the operator override: FULL at full size, no
		// re-encoding. It needs the operator's name in By.
		Original bool   `json:"original"`
		By       string `json:"by"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: pull request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if in.ItemID == "" {
		http.Error(w, "itemId required", http.StatusBadRequest)
		return
	}
	if !ids.Valid(in.ItemID) {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	by := opauth.Actor(r.Context(), "operator-ui")
	if in.Original {
		if by = opauth.Actor(r.Context(), strings.TrimSpace(in.By)); by == "" {
			http.Error(w, "operator name (by) required for a full-size override", http.StatusBadRequest)
			return
		}
	}
	kind := pb.FormKind_FORM_KIND_FULL
	for k, n := range formNames {
		if n == in.Form {
			kind = k
		}
	}
	var out pb.RequestPullResponse
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: in.ItemID, Form: kind, RequestedBy: by, Original: in.Original}}
	if err := b.call(r.Context(), b.shore, "RequestPull", req, &out); err != nil {
		log.Printf("shorebridge: request pull upstream: %v", err)
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	b.Poke()
	writeJSON(w, map[string]any{"ok": true, "itemId": in.ItemID, "form": formNames[kind], "original": in.Original})
}

func (b *Bridge) override(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"itemId"`
		Label  string `json:"label"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: override request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if in.ItemID == "" {
		http.Error(w, "itemId required", http.StatusBadRequest)
		return
	}
	if !ids.Valid(in.ItemID) {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(in.Label) > 500 {
		http.Error(w, "label exceeds 500 characters", http.StatusBadRequest)
		return
	}
	var out pb.OverrideLabelResponse
	req := &pb.OverrideLabelRequest{Override: &pb.LabelOverride{ItemId: in.ItemID, Label: in.Label, By: opauth.Actor(r.Context(), "operator-ui")}}
	if err := b.call(r.Context(), b.shore, "OverrideLabel", req, &out); err != nil {
		log.Printf("shorebridge: override upstream: %v", err)
		http.Error(w, "upstream shore error", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (b *Bridge) setLink(w http.ResponseWriter, r *http.Request) {
	if b.link == nil {
		http.Error(w, "link is not controllable from this process (use deploy/compose/degrade.sh)", http.StatusNotImplemented)
		return
	}
	var in struct {
		Bps int64 `json:"bps"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONBody(r.Body, &in); err != nil {
		log.Printf("shorebridge: link request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	b.link.SetBps(in.Bps)
	writeJSON(w, b.linkStatus())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func decodeJSONBody(reader io.Reader, dst any) error {
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
