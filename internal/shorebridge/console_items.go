package shorebridge

import (
	"fmt"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"math"
	"sort"
	"strings"
	"time"
)

type objectScore struct {
	edge, central, base   float64
	centralTier, baseTier int
}

// scoreObjects turns the shore's unbounded priority scores into the console's
// 0..1 scores by rank: Edge is the raw score normalized, Base the rank with
// suppression ignored, Central the rank the shore actually uses (suppressed
// objects sink to the bottom band but stay on the board).
func scoreObjects(objs []*Object, pins ...map[string]int32) map[*Object]objectScore {
	tiers := map[string]int32{}
	if len(pins) > 0 {
		tiers = pins[0]
	}
	out := make(map[*Object]objectScore, len(objs))
	contacts := make([]*Object, 0, len(objs))
	for _, o := range objs {
		if o.Scene {
			out[o] = objectScore{centralTier: 4, baseTier: 4}
		} else {
			contacts = append(contacts, o)
		}
	}
	objs = contacts
	maxScore := 0.0
	for _, o := range objs {
		maxScore = math.Max(maxScore, o.Score)
	}
	byScore := append([]*Object(nil), objs...)
	sort.SliceStable(byScore, func(i, j int) bool { return byScore[i].Score > byScore[j].Score })
	ranked := make([]*Object, 0, len(objs))
	for _, o := range byScore {
		_, tier := objectTierPin(o, tiers)
		if !o.Suppressed && (tier < 1 || tier > 4) {
			ranked = append(ranked, o)
		}
	}
	for i, o := range byScore {
		s := out[o]
		if maxScore > 0 {
			s.edge = o.Score / maxScore
		}
		s.base = rankScore(i, len(byScore))
		s.baseTier = rankTier(i, len(byScore))
		s.central, s.centralTier = 0.05+0.1*s.edge, 4
		if _, tier := objectTierPin(o, tiers); tier >= 1 && tier <= 4 {
			s.centralTier = int(tier)
			s.central = [...]float64{0, .9, .7, .475, .175}[tier]
		}
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
	if !o.Scene {
		c.ensureClass(cls)
	}
	if !o.Scene && o.Label != "" {
		c.ensureClass(strings.ToLower(o.Label))
	}
	src := c.ensureSource(b.Source)

	item := &command.Item{
		ID:          o.Key,
		DBID:        c.dbID(o.Key),
		Frame:       o.Frames,
		SourceID:    src,
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
		Unknownness: b.Unknownness, Unknown: o.Unknown, Score: o.Score, Priority: o.Priority,
		Suppressed: o.Suppressed, Frames: o.Frames, FramesKnown: o.FramesKnown, Reports: o.Reports,
		FirstMs: o.FirstMs, LastMs: o.LastMs, Forms: forms, ImageForm: b.ImageForm, FactURL: b.FactURL,
		Pull: pullState(b.Forms["full"], o.Scene),
	}
	for _, card := range o.Items {
		if card.Suppressed && card.SuppressedBy != "" {
			live.SuppressedBy = card.SuppressedBy
			break
		}
	}
	item.Live = live
	populateCapture(item, o)
	if o.Scene {
		item.Background = true
		live.Event, live.Priority = "scene", "P4"
		live.Label, live.Subtype, live.Override = "", "", ""
		live.Unknown, live.Unknownness, live.Suppressed = false, 0, false
		return item
	}

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
		score.Notes = append(score.Notes, command.Rule{Type: "suppress", Classes: []string{strings.ToLower(label)}, Attrs: []string{}, DirectiveID: c.directiveFor(label)})
	}
	if l := matchLabel(c.policy.Boost, b.Override, o.Subtype, o.Label); l != "" {
		score.Matched = true
		score.Notes = append(score.Notes, command.Rule{Type: "boost", Classes: []string{l}, Attrs: []string{}, DirectiveID: c.directiveFor(l)})
	}
	if label, tier := objectTierPin(o, c.policy.TierLabels); tier >= 1 && tier <= 4 {
		score.Matched = true
		score.Notes = append(score.Notes, command.Rule{Type: "boost", Classes: []string{label}, Attrs: []string{}, Tier: int(tier), DirectiveID: c.directiveFor(label)})
	}
	item.Dets = append(item.Dets, det)
	item.Det = append(item.Det, score)
	return item
}

func matchLabel(labels []string, override string, candidates ...string) string {
	// Match the edge scorer: a human override replaces machine labels.
	if override != "" {
		candidates = []string{override}
	}
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
		return command.LivePull{Enabled: false, Label: "Full " + what + " received", Reason: fmt.Sprintf("The full image (%s) is already stored on shore.", fmtBytes(full.Bytes))}
	case "partial":
		pct := 0
		if full.Bytes > 0 {
			pct = int(100 * full.Received / full.Bytes)
		}
		return command.LivePull{Enabled: false, Label: fmt.Sprintf("Full %s %d%%", what, pct), Reason: fmt.Sprintf("The full image is transferring (%s of %s). Interrupted transfers can resume.", fmtBytes(full.Received), fmtBytes(full.Bytes))}
	case "pending":
		if scene && full.Received == 0 {
			// Scene FULL is pull-only: the edge holds it (so it shows as a pending form) but never
			// schedules it. Only an explicit pull releases it.
			return command.LivePull{Enabled: true, Label: "Request full image", Reason: fmt.Sprintf("The full camera overview (%s) stays on the ship until you request it.", fmtBytes(full.Bytes))}
		}
		return command.LivePull{Enabled: false, Label: "Full " + what + " queued", Reason: "The full image is waiting on the ship and will arrive as bandwidth becomes available."}
	case "offered":
		if scene {
			return command.LivePull{Enabled: true, Label: "Request full image", Reason: fmt.Sprintf("The full camera overview (%s) stays on the ship until you request it.", fmtBytes(full.Bytes))}
		}
		return command.LivePull{Enabled: true, Label: "Request full " + what, Reason: fmt.Sprintf("The full image (%s) is available on the ship. Request it to send it ahead of other waiting images.", fmtBytes(full.Bytes))}
	default:
		return command.LivePull{Enabled: true, Label: "Request full " + what, Reason: "No full image is stored on shore yet. Request it to have the ship prepare and send it."}
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
