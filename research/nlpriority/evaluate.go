package nlpriority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

// Markers prevent production Interest's intentional fuzzy label matching from
// treating every generic "boat" as a match for a novel "rescue boat" request.
// They live only in a scoring copy's subtype tag; original evidence is retained.
func conceptMarker(concept string) string {
	sum := sha256.Sum256([]byte(concept))
	return "__nl_" + hex.EncodeToString(sum[:12]) + "__"
}

func Policy(rules []Rule) *pb.Policy {
	p := scheduler.DefaultPolicy()
	p.Id = "research-nl"
	p.Text = makeDraft(rules, "", "").Readback
	p.Interest.BoostWeights = map[string]float64{}
	for _, r := range rules {
		m := conceptMarker(r.Concept)
		if r.Action == "boost" {
			p.Interest.BoostLabels = append(p.Interest.BoostLabels, m)
			p.Interest.BoostWeights[m] = r.Weight
		} else {
			p.Interest.SuppressLabels = append(p.Interest.SuppressLabels, m)
		}
	}
	return p
}

func scoringItem(it Item, rules []Rule, matches map[string]Match) *pb.Item {
	tags := map[string]string{"subtype": it.Subtype}
	if it.TomerScore != nil {
		tags[scoring.TagTomerScore] = strconv.FormatFloat(*it.TomerScore, 'g', -1, 64)
	}
	f := &pb.Features{Unknownness: it.Unknownness, Embedding: it.Embedding, ModelId: it.ModelID, Tags: tags, LabelOverride: it.LabelOverride, OverrideBy: it.OverrideBy}
	if it.Label != "" {
		f.Detections = []*pb.Detection{{Label: it.Label, Confidence: it.Confidence}}
	}
	markers := []string{}
	for _, r := range rules {
		if conceptMatches(it, r.Concept, matches) {
			markers = append(markers, conceptMarker(r.Concept))
		}
	}
	if it.LabelOverride == "" && len(markers) > 0 {
		tags["subtype"] = it.Subtype + " " + strings.Join(markers, " ")
	}
	return &pb.Item{Id: it.ID, Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: it.CapturedAt, Source: it.Source, Features: f}
}

func conceptMatches(it Item, concept string, matches map[string]Match) bool {
	if it.LabelOverride != "" {
		return NormalizeConcept(it.LabelOverride) == concept
	}
	if (it.Confidence >= scoring.DefaultDetectionMin && NormalizeConcept(it.Label) == concept) || NormalizeConcept(it.Subtype) == concept {
		return true
	}
	if concept == "unknown" && it.Unknownness >= scoring.DefaultUnknownMin {
		return true
	}
	m, ok := matches[it.ID]
	return ok && m.Accepted && NormalizeConcept(m.Label) == concept
}

func previewImpactWarnings(items []Item, rules []Rule, matches map[string]Match, before, after Evaluation) []string {
	out := []string{}
	if len(items) == 0 {
		return []string{"The research queue is empty; import image records to preview priority changes."}
	}
	for _, r := range rules {
		count := 0
		for _, it := range items {
			if conceptMatches(it, r.Concept, matches) {
				count++
			}
		}
		if count == 0 {
			out = append(out, fmt.Sprintf("No images currently match %q; this rule will not change priorities in the current queue.", r.Concept))
		}
	}
	prior := map[string]RankedItem{}
	for _, it := range before.Items {
		prior[it.ID] = it
	}
	changed := false
	for _, it := range after.Items {
		old := prior[it.ID]
		if it.Priority != old.Priority || it.Suppressed != old.Suppressed {
			changed = true
			break
		}
	}
	if !changed {
		out = append(out, "No image priorities changed in this preview. The same interests may already be active, no images may match, or existing protections may apply.")
	}
	return out
}

// Evaluate invokes the production engine and BuildPlan. The local delivery
// simulator acknowledges planned byte ranges without network or payload I/O.
// Payload bytes are placeholders; image content never affects transport order.
func Evaluate(ctx context.Context, items []Item, rules []Rule, matches map[string]Match, engine string, now time.Time, bandwidth float64) (Evaluation, error) {
	result := Evaluation{Items: []RankedItem{}, Delivery: []Delivery{}, BandwidthBPS: bandwidth, SimulatedSeconds: 50}
	eng, err := scoring.ForPrioritizer(engine)
	if err != nil {
		return result, err
	}
	p := Policy(rules)
	rows := make([]*store.Row, 0, len(items))
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		item := scoringItem(it, rules, matches)
		itemPolicy := p
		if it.LabelOverride != "" {
			// Keep the human label byte-for-byte unchanged (Unknown and Dark
			// depend on it). Only exact matching policies can address it.
			itemPolicy = Policy(rules)
			for _, rule := range rules {
				if NormalizeConcept(it.LabelOverride) != rule.Concept {
					continue
				}
				marker := conceptMarker(rule.Concept)
				for i, label := range itemPolicy.Interest.BoostLabels {
					if label == marker {
						itemPolicy.Interest.BoostLabels[i] = it.LabelOverride
						itemPolicy.Interest.BoostWeights[strings.ToLower(strings.TrimSpace(it.LabelOverride))] = rule.Weight
					}
				}
				for i, label := range itemPolicy.Interest.SuppressLabels {
					if label == marker {
						itemPolicy.Interest.SuppressLabels[i] = it.LabelOverride
					}
				}
			}
		}
		score := eng.Score(item, &scoring.Context{Policy: itemPolicy, Now: now}, 0)
		row := &store.Row{Item: item, Score: score, Forms: []store.FormState{{Kind: pb.FormKind_FORM_KIND_FACT, Bytes: 256, Ready: true}, {Kind: pb.FormKind_FORM_KIND_THUMB, Bytes: it.ThumbBytes, Ready: true}, {Kind: pb.FormKind_FORM_KIND_FULL, Bytes: it.FullBytes, Ready: true}}}
		rows = append(rows, row)
		r := RankedItem{ID: it.ID, Label: it.Label, Subtype: it.Subtype, Priority: score.Priority, Suppressed: score.Suppressed, ImageURL: it.ImageURL, Unknownness: it.Unknownness, LabelOverride: it.LabelOverride, Reasons: []Reason{}}
		if m, ok := matches[it.ID]; ok {
			r.Similarity = m.Similarity
			if m.Accepted && it.LabelOverride == "" && m.Experimental {
				r.ExperimentalLabel = m.Label
			} else if !m.Accepted {
				r.Abstention = m.Reason
			}
		}
		for _, reason := range score.Reasons {
			note := reason.Note
			for _, rule := range rules {
				note = strings.ReplaceAll(note, conceptMarker(rule.Concept), rule.Concept)
			}
			r.Reasons = append(r.Reasons, Reason{reason.Scorer, reason.Value, note})
		}
		result.Items = append(result.Items, r)
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Priority > result.Items[j].Priority })
	if len(items) == 0 {
		return result, nil
	}
	// The production scheduler requires a store for policy lifecycle; this
	// ephemeral empty store never replaces or mutates the source research state.
	tmp, err := os.MkdirTemp("", "ghost-nl-plan-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	st, err := store.Open(tmp)
	if err != nil {
		return result, err
	}
	defer st.Close()
	cfg := scheduler.DefaultConfig("research")
	cfg.MaxExchangeBytes = 256 << 10
	sch, err := scheduler.New(cfg, st, nil, eng, clock.NewVirtual(now), nil)
	if err != nil {
		return result, err
	}
	if _, _, err = sch.ApplyPolicy(ctx, p); err != nil {
		return result, err
	}
	byID := map[string]*store.Row{}
	for _, r := range rows {
		byID[r.Item.Id] = r
	}
	for tick := 1; tick <= 10; tick++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		plan := sch.BuildPlan(rows, bandwidth, now.Add(time.Duration(tick-1)*5*time.Second))
		for _, m := range plan.Manifest {
			byID[m.ItemId].ManifestAcked = true
		}
		for _, ch := range plan.Chunks {
			row := byID[ch.ItemId]
			form := row.Form(ch.Form)
			if form == nil {
				return result, fmt.Errorf("scheduler selected missing form")
			}
			form.AckedThrough = ch.Offset + int64(len(ch.Data))
			form.Complete = form.AckedThrough >= form.Bytes
			kind := strings.TrimPrefix(ch.Form.String(), "FORM_KIND_")
			d := Delivery{ch.ItemId, kind, int64(len(ch.Data)), tick, form.Complete}
			// Coalesce adjacent chunks of a form for a readable delivery trace.
			n := len(result.Delivery)
			if n > 0 && result.Delivery[n-1].ID == d.ID && result.Delivery[n-1].Form == d.Form && result.Delivery[n-1].Tick == tick {
				result.Delivery[n-1].Bytes += d.Bytes
				result.Delivery[n-1].Complete = d.Complete
			} else {
				result.Delivery = append(result.Delivery, d)
			}
		}
		if len(plan.Manifest) == 0 && len(plan.Chunks) == 0 {
			break
		}
	}
	return result, nil
}
