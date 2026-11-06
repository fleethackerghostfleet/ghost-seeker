package shorebridge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
)

// ---- grouping ----

var formNames = map[pb.FormKind]string{
	pb.FormKind_FORM_KIND_FACT:  "fact",
	pb.FormKind_FORM_KIND_THUMB: "thumb",
	pb.FormKind_FORM_KIND_CROP:  "crop",
	pb.FormKind_FORM_KIND_FULL:  "full",
}

func objectTierPin(o *Object, tiers map[string]int32) (string, int32) {
	if o.Scene {
		return "", 0
	}
	policy := &pb.Policy{Interest: &pb.Interest{TierLabels: tiers}, Thresholds: &pb.Thresholds{DetectionMinConf: o.detectionMin}}
	var label string
	var tier int32
	match := func(item *pb.Item) {
		candidate, target := scoring.TierPin(item, policy)
		if target > 0 && (tier == 0 || target < tier || target == tier && candidate < label) {
			label, tier = candidate, target
		}
	}
	displayed := &pb.Features{Tags: map[string]string{"subtype": o.Subtype}}
	if o.Label != "" {
		displayed.Detections = []*pb.Detection{{Label: o.Label, Confidence: 1}}
	}
	if o.Best != nil {
		displayed.LabelOverride = o.Best.Override
	}
	match(&pb.Item{Features: displayed})
	matchCard := func(card *Card) {
		if card == nil {
			return
		}
		if card.item != nil {
			match(card.item)
			return
		}
		features := &pb.Features{LabelOverride: card.Override, Tags: card.Tags}
		if card.Label != "" {
			features.Detections = []*pb.Detection{{Label: card.Label, Confidence: 1}}
		}
		match(&pb.Item{Features: features})
	}
	matchCard(o.Best)
	for _, card := range o.Items {
		matchCard(card)
	}
	return label, tier
}

// assignTiers maps the edge's unbounded priority score onto the P1..P4
// tiers the operator UI uses by rank within the current shore picture:
// top 10% P1, next 25% P2, next 35% P3, rest P4. Suppressed objects are P4.
func assignTiers(objs []*Object, pins ...map[string]int32) {
	tiers := map[string]int32{}
	if len(pins) > 0 {
		tiers = pins[0]
	}
	ranked := make([]*Object, 0, len(objs))
	for _, o := range objs {
		if _, tier := objectTierPin(o, tiers); tier >= 1 && tier <= 4 {
			o.Priority = fmt.Sprintf("P%d", tier)
			o.Suppressed = tier == 4
			continue
		}
		if o.Best != nil && !o.Suppressed && !o.Scene {
			ranked = append(ranked, o)
		} else {
			o.Priority = "P4"
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
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
		item:        it,
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
			o.Score = c.Score
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

func cardReceived(c *Card) bool {
	for _, form := range c.Forms {
		if form.State == "received" {
			return true
		}
	}
	return false
}

func better(a, b *Card) bool {
	if cardReceived(a) != cardReceived(b) {
		return cardReceived(a)
	}
	if (a.Type == "record") != (b.Type == "record") {
		return b.Type == "record"
	}
	if formRank[a.ImageForm] != formRank[b.ImageForm] {
		return formRank[a.ImageForm] > formRank[b.ImageForm]
	}
	return a.CapturedMs > b.CapturedMs
}
