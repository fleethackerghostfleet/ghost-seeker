package shorebridge

import (
	"encoding/json"
	"fmt"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type sseEvent struct {
	name string
	data []byte
}

// ---- building items ----

func (c *Console) onSnapshot(snap *Snapshot, pol *pb.Policy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.base
	c.base, c.order = map[string]*command.Item{}, nil
	c.objects = map[string]*Object{}
	c.policy = c.policyFrom(snap, pol)
	// Once the edge tracks sightings, per-frame items without a sighting_id
	// (the first frames before the tracker locked on) are duplicates of a
	// sighting card and are folded away; they still count in Counts.Objects.
	sightingMode := snap.Counts.Grouped > 0
	objects := make([]*Object, 0, len(snap.Objects))
	for _, o := range snap.Objects {
		if o.Best == nil || (!o.Scene && sightingMode && o.SightingID == "") {
			continue
		}
		objects = append(objects, o)
	}
	scores := scoreObjects(objects, c.policy.TierLabels)
	for _, o := range objects {
		item := c.buildItem(o, scores[o])
		c.base[item.ID] = item
		c.objects[item.ID] = o
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

// ---- snapshot / overlay ----

func (c *Console) withOverlay(base *command.Item) command.Item {
	item := *base
	if isScene(base) {
		return item
	}
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

func isScene(item *command.Item) bool {
	return item != nil && item.Live != nil && item.Live.Event == "scene"
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
		if o.Sent && c.base[id] != nil && !isScene(c.base[id]) {
			n++
		}
	}
	return n
}

func (c *Console) statsLocked() command.Stats {
	e, s := c.live.Edge, c.live.Shore
	bytes := s.Bytes
	if c.live.Link.Controllable {
		bytes = c.live.Link.Bytes
	}
	stats := command.Stats{Cap: int(e.ItemsTotal + e.Deduped), Keep: int(e.ItemsTotal), Drop: int(e.Deduped), Bytes: int(bytes), DB: int(s.ItemsTotal), MetaOnly: e.MetaOnly, MetaOnlyReason: e.MetaOnlyReason}
	if e.MetaOnlySince != 0 {
		t := time.UnixMilli(e.MetaOnlySince).UTC()
		stats.MetaOnlySince = &t
	}
	return stats
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
