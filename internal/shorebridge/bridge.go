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

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
)

const consolePath = "/ghostseeker.v1.ConsoleService/"

// LinkControl is what the UI needs from a link: the current rate and a way
// to change it. *shaper.Shaper satisfies it; nil means "read-only".
type LinkControl interface {
	Bps() int64
	Bytes() int64
	SetBps(int64)
}

type Bridge struct {
	shore, edge string
	link        LinkControl
	client      *http.Client

	mu   sync.Mutex
	snap []byte
	subs map[chan []byte]struct{}
}

func New(shoreURL, edgeURL string, link LinkControl) *Bridge {
	return &Bridge{
		shore:  strings.TrimRight(shoreURL, "/"),
		edge:   strings.TrimRight(edgeURL, "/"),
		link:   link,
		client: &http.Client{Timeout: 10 * time.Second},
		subs:   map[chan []byte]struct{}{},
	}
}

// ---- wire model (what the browser sees) ----

type FormStatus struct {
	State    string `json:"state"` // none | offered | pending | partial | received
	Bytes    int64  `json:"bytes"`
	Received int64  `json:"received"`
}

type Card struct {
	ID          string                `json:"id"`
	CapturedMs  int64                 `json:"capturedMs"`
	Source      string                `json:"source"`
	Label       string                `json:"label"`
	Conf        float64               `json:"conf"`
	Override    string                `json:"override,omitempty"`
	Unknownness float64               `json:"unknownness"`
	Priority    string                `json:"priority"`
	Score       float64               `json:"score"`
	Suppressed  bool                  `json:"suppressed"`
	Tags        map[string]string     `json:"tags,omitempty"`
	Forms       map[string]FormStatus `json:"forms"`
	Image       string                `json:"image,omitempty"` // best available form URL
	ImageForm   string                `json:"imageForm,omitempty"`
	FactURL     string                `json:"factUrl"`
	Box         *pb.Box               `json:"box,omitempty"`
}

type Object struct {
	Key        string  `json:"key"`
	SightingID string  `json:"sightingId,omitempty"`
	Subtype    string  `json:"subtype,omitempty"`
	Event      string  `json:"event,omitempty"`
	Label      string  `json:"label"`
	Frames     int     `json:"frames"`
	FirstMs    int64   `json:"firstMs"`
	LastMs     int64   `json:"lastMs"`
	Priority   string  `json:"priority"`
	Best       *Card   `json:"best"`
	Items      []*Card `json:"items"`
	HasImage   bool    `json:"hasImage"`
	Unknown    bool    `json:"unknown"`
	Suppressed bool    `json:"suppressed"`
}

type NodeStatus struct {
	OK          bool    `json:"ok"`
	ItemsTotal  int64   `json:"itemsTotal"`
	ItemsUnique int64   `json:"itemsComplete"`
	Unknown     int64   `json:"itemsUnknown"`
	Bytes       int64   `json:"bytes"`
	Queued      int64   `json:"queuedItems"`
	QueuedBytes int64   `json:"queuedBytes"`
	LinkUp      bool    `json:"linkUp"`
	EstimateBps float64 `json:"estimateBps"`
	PolicyID    string  `json:"policyId"`
	LastSeenMs  int64   `json:"lastSeenMs"`
	Error       string  `json:"error,omitempty"`
}

type Snapshot struct {
	TS      int64      `json:"ts"`
	Shore   NodeStatus `json:"shore"`
	Edge    NodeStatus `json:"edge"`
	Link    LinkStatus `json:"link"`
	Policy  string     `json:"policy"`
	Objects []*Object  `json:"objects"`
	Counts  Counts     `json:"counts"`
}

type LinkStatus struct {
	Controllable bool    `json:"controllable"`
	Bps          int64   `json:"bps"`
	Bytes        int64   `json:"bytes"`
	Presets      []int64 `json:"presets"`
}

type Counts struct {
	Items            int `json:"items"`
	ItemsWithImage   int `json:"itemsWithImage"`
	Objects          int `json:"objects"`
	ObjectsWithImage int `json:"objectsWithImage"`
	Grouped          int `json:"grouped"` // items carrying a sighting_id
}

// ---- console RPC helpers ----

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
		return fmt.Errorf("%s: %s: %s", method, res.Status, strings.TrimSpace(string(raw)))
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, resp)
}

// ---- polling ----

func (b *Bridge) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		b.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
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
		snap.Shore.Error = err.Error()
	} else {
		snap.Shore = nodeStatus(&st)
		if b.link == nil {
			snap.Link.Bps = int64(st.GetLink().GetEstimateBps())
			snap.Link.Bytes = st.GetBytesSent()
		}
	}
	var pol pb.GetPolicyResponse
	var suppressed []string
	if err := b.call(ctx, b.shore, "GetPolicy", &pb.GetPolicyRequest{}, &pol); err == nil {
		snap.Policy = pol.GetPolicy().GetText()
		suppressed = pol.GetPolicy().GetInterest().GetSuppressLabels()
	}
	if b.edge != "" {
		var est pb.GetStatusResponse
		if err := b.call(ctx, b.edge, "GetStatus", &pb.GetStatusRequest{}, &est); err != nil {
			snap.Edge.Error = err.Error()
		} else {
			snap.Edge = nodeStatus(&est)
		}
	}

	var items []*pb.ItemView
	token := ""
	for page := 0; page < 20; page++ {
		var lr pb.ListItemsResponse
		if err := b.call(ctx, b.shore, "ListItems", &pb.ListItemsRequest{Limit: 500, PageToken: token}, &lr); err != nil {
			if snap.Shore.Error == "" {
				snap.Shore.Error = err.Error()
			}
			break
		}
		items = append(items, lr.Items...)
		token = lr.NextPageToken
		if token == "" || len(lr.Items) == 0 {
			break
		}
	}
	snap.Objects, snap.Counts = Group(items, suppressed)

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

func nodeStatus(st *pb.GetStatusResponse) NodeStatus {
	n := NodeStatus{
		OK:          true,
		ItemsTotal:  st.GetItemsTotal(),
		ItemsUnique: st.GetItemsComplete(),
		Unknown:     st.GetItemsUnknown(),
		Bytes:       st.GetBytesSent(),
		Queued:      st.GetHeartbeat().GetQueuedItems(),
		QueuedBytes: st.GetHeartbeat().GetQueuedBytes(),
		LinkUp:      st.GetLink().GetUp(),
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

// priorityName buckets the shore score (0..1) into the P1..P4 tiers the
// operator UI uses.
func priorityName(p float64) string {
	switch {
	case p >= 0.75:
		return "P1"
	case p >= 0.5:
		return "P2"
	case p >= 0.25:
		return "P3"
	default:
		return "P4"
	}
}

// ToCard flattens an ItemView into what a card needs, picking the best
// received image form (crop > thumb > full).
func ToCard(v *pb.ItemView) *Card {
	it := v.GetItem()
	c := &Card{
		ID:          it.GetId(),
		CapturedMs:  it.GetCapturedAtUnixMs(),
		Source:      it.GetSource(),
		Unknownness: float64(it.GetFeatures().GetUnknownness()),
		Override:    it.GetFeatures().GetLabelOverride(),
		Priority:    priorityName(v.GetScore().GetPriority()),
		Score:       v.GetScore().GetPriority(),
		Suppressed:  v.GetScore().GetSuppressed(),
		Tags:        it.GetFeatures().GetTags(),
		Forms:       map[string]FormStatus{},
		FactURL:     "/blob/" + it.GetId() + "/fact",
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
	for _, n := range []string{"crop", "thumb", "full"} {
		if c.Forms[n].State == "received" {
			c.Image, c.ImageForm = "/blob/"+c.ID+"/"+n, n
			break
		}
	}
	return c
}

// Group buckets cards by sighting_id (one object per sighting; items without
// a tag are their own object), newest first. Items whose label is in the
// current policy's suppress list are marked suppressed even if they were
// scored before the policy reached the edge.
func Group(views []*pb.ItemView, suppressLabels []string) ([]*Object, Counts) {
	var cnt Counts
	byKey := map[string]*Object{}
	var order []*Object
	for _, v := range views {
		c := ToCard(v)
		for _, l := range suppressLabels {
			// Mirrors edge/scoring: a negative interest never suppresses an unknown.
			if c.Unknownness < unknownThreshold && (strings.EqualFold(l, c.Label) || (c.Override != "" && strings.EqualFold(l, c.Override))) {
				c.Suppressed = true
			}
		}
		cnt.Items++
		if c.Image != "" {
			cnt.ItemsWithImage++
		}
		key := "item:" + c.ID
		sid := c.Tags["sighting_id"]
		if sid != "" {
			key = "sighting:" + sid
			cnt.Grouped++
		}
		o := byKey[key]
		if o == nil {
			o = &Object{Key: key, SightingID: sid, FirstMs: c.CapturedMs, LastMs: c.CapturedMs, Priority: c.Priority}
			byKey[key] = o
			order = append(order, o)
		}
		o.Items = append(o.Items, c)
		o.Frames++
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
				o.Frames = fr
			}
		}
		if c.Priority < o.Priority { // "P1" < "P2"
			o.Priority = c.Priority
		}
		o.Suppressed = o.Suppressed || c.Suppressed
		o.Unknown = o.Unknown || c.Unknownness >= unknownThreshold
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
		cnt.Objects++
		if o.HasImage {
			cnt.ObjectsWithImage++
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].LastMs > order[j].LastMs })
	return order, cnt
}

// unknownThreshold matches the Object.Unknown flag and edge/scoring's notion of an unknown contact.
const unknownThreshold = 0.5

var formRank = map[string]int{"": 0, "thumb": 1, "full": 2, "crop": 3}

func better(a, b *Card) bool {
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
	mux.HandleFunc("POST /api/shore/pull", b.requestPull)
	mux.HandleFunc("POST /api/shore/override", b.override)
	mux.HandleFunc("GET /api/shore/link", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, b.linkStatus()) })
	mux.HandleFunc("POST /api/shore/link", b.setLink)
	if u, err := url.Parse(b.shore); err == nil {
		mux.Handle("/blob/", httputil.NewSingleHostReverseProxy(u))
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
	w.Header().Set("Cache-Control", "no-cache")
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
	IssuedBy string   `json:"issuedBy"`
}

// setPolicy forwards an operator directive to shore. A free-text directive is
// parsed with Interpret (e.g. "ignore cargo ships" -> suppress cargo ship).
func (b *Bridge) setPolicy(w http.ResponseWriter, r *http.Request) {
	var in policyBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if in.IssuedBy == "" {
		in.IssuedBy = "operator-ui"
	}
	pol := &pb.Policy{IssuedBy: in.IssuedBy, Text: in.Text, Interest: &pb.Interest{SuppressLabels: in.Suppress, BoostLabels: in.Boost}}
	if len(in.Suppress) == 0 && len(in.Boost) == 0 && in.Text != "" {
		pol.Interest.SuppressLabels, pol.Interest.BoostLabels = Interpret(in.Text)
	}
	var out pb.SetPolicyResponse
	if err := b.call(r.Context(), b.shore, "SetPolicy", &pb.SetPolicyRequest{Policy: pol}, &out); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	raw, _ := protojson.Marshal(&out)
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (b *Bridge) requestPull(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"itemId"`
		Form   string `json:"form"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ItemID == "" {
		http.Error(w, "itemId required", http.StatusBadRequest)
		return
	}
	kind := pb.FormKind_FORM_KIND_FULL
	for k, n := range formNames {
		if n == in.Form {
			kind = k
		}
	}
	var out pb.RequestPullResponse
	req := &pb.RequestPullRequest{Pull: &pb.Pull{ItemId: in.ItemID, Form: kind, RequestedBy: "operator-ui"}}
	if err := b.call(r.Context(), b.shore, "RequestPull", req, &out); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "itemId": in.ItemID, "form": formNames[kind]})
}

func (b *Bridge) override(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"itemId"`
		Label  string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ItemID == "" {
		http.Error(w, "itemId required", http.StatusBadRequest)
		return
	}
	var out pb.OverrideLabelResponse
	req := &pb.OverrideLabelRequest{Override: &pb.LabelOverride{ItemId: in.ItemID, Label: in.Label, By: "operator-ui"}}
	if err := b.call(r.Context(), b.shore, "OverrideLabel", req, &out); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.link.SetBps(in.Bps)
	writeJSON(w, b.linkStatus())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
