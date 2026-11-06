package shorebridge

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	edgefact "github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
	"io"
	"log"
	"net/http"
	"time"
)

// maxRecordFetchesPerRefresh bounds the extra /blob/{id}/fact reads one poll
// may do for closed sightings' contact reports, so a burst of closes cannot
// stall the snapshot.
const maxRecordFetchesPerRefresh = 25

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
	snap.Link = b.linkStatus()

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
	} else {
		snap.Shore.Error = "upstream shore error"
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
		snap.TierLabels = currentPolicy.GetInterest().GetTierLabels()
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
	if snap.Shore.Error != "" {
		snap.Shore.OK = false
		b.mu.Lock()
		if b.lastGood != nil {
			previous := *b.lastGood
			previous.TS, previous.Link, previous.Edge = snap.TS, snap.Link, snap.Edge
			previous.Shore.OK = false
			previous.Shore.Error = snap.Shore.Error
			snap = &previous
			currentPolicy = b.lastPolicy
		}
		b.mu.Unlock()
	} else {
		b.updateFactVersions(items)
		snap.Objects, snap.Counts = Group(items, suppressed, b.recordReports(ctx, items))
		for _, o := range snap.Objects {
			o.detectionMin = currentPolicy.GetThresholds().GetDetectionMinConf()
		}
		if len(snap.TierLabels) > 0 {
			assignTiers(snap.Objects, snap.TierLabels)
		}
		b.mu.Lock()
		b.lastGood, b.lastPolicy = snap, currentPolicy
		b.mu.Unlock()
	}
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

func (b *Bridge) updateFactVersions(items []*pb.ItemView) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, v := range items {
		for _, form := range v.GetItem().GetForms() {
			if form.GetKind() != pb.FormKind_FORM_KIND_FACT {
				continue
			}
			complete := false
			for _, state := range v.GetFormStates() {
				if state.GetKind() == pb.FormKind_FORM_KIND_FACT {
					complete = state.GetComplete()
				}
			}
			id := v.GetItem().GetId()
			version := fmt.Sprintf("%s:%d:%t", form.GetContentHash(), form.GetBytes(), complete)
			if b.factVersions[id] != version {
				delete(b.facts, id)
				b.factVersions[id] = version
			}
		}
	}
}

func (b *Bridge) fetchFact(ctx context.Context, id string) *edgefact.Fact {
	b.mu.Lock()
	if f, ok := b.facts[id]; ok {
		b.mu.Unlock()
		return f
	}
	version := b.factVersions[id]
	b.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.shore+"/blob/"+id+"/fact", nil)
	if err != nil {
		return nil
	}
	b.authorize(req)
	res, err := b.client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil
	}
	var f edgefact.Fact
	if json.Unmarshal(raw, &f) != nil || f.ID == "" {
		var env struct {
			Fact json.RawMessage `json:"fact"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil {
			return nil
		}
		for _, part := range [][]byte{env.Fact, env.Data} {
			if len(part) > 0 && json.Unmarshal(part, &f) == nil && f.ID != "" {
				break
			}
		}
	}
	if f.ID == "" {
		return nil
	}
	b.mu.Lock()
	if b.factVersions[id] == version {
		b.facts[id] = &f
	}
	b.mu.Unlock()
	return &f
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
		MetaOnly:    st.GetMetaOnly(), MetaOnlySince: st.GetMetaOnlySinceUnixMs(), MetaOnlyReason: st.GetMetaOnlyReason(),
	}
	if n.EstimateBps == 0 {
		n.EstimateBps = float64(st.GetHeartbeat().GetLinkEstimateBps())
	}
	return n
}
