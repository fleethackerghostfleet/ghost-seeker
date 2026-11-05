package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	edgeStore "github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
	shoreStore "github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	shoreSync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

// A controlled metadata branch test, not a claim that this real image is empty.
func TestResearchSceneRequiresExplicitFullPull(t *testing.T) {
	fixtureDir, outputDir := os.Getenv("REPLAY_FIXTURES"), os.Getenv("REPLAY_OUT")
	if fixtureDir == "" || outputDir == "" {
		t.Skip("research fixture paths not set")
	}
	raw, err := os.ReadFile(filepath.Join(fixtureDir, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []replayFixture
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	f := fixtures[0]
	ctx := context.Background()
	start := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	edge, err := edgeStore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Close()
	shore, err := shoreStore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer shore.Close()
	service := shoreSync.New(shore, clk, nil)
	r := &replayLink{clk: clk, start: start, ackTimes: map[string]float64{}}
	arrivals := map[string]float64{}
	service.OnArrival = func(id string, kind pb.FormKind) {
		key := id + "/" + kind.String()
		if _, ok := arrivals[key]; !ok {
			arrivals[key] = clk.Now().Sub(start).Seconds()
		}
	}
	handler := func(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
		r.current["shore_handler_s"] = clk.Now().Sub(start).Seconds()
		return service.Exchange(ctx, req)
	}
	r.sim = link.NewSim(link.Trace{Name: "scene-64k", Windows: []link.Window{{Duration: time.Hour, Bps: 64000, RTT: 600 * time.Millisecond}}}, clk, handler, 20261105)
	sched, err := New(DefaultConfig("research-scene"), edge, r, scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := &pb.Item{Id: "research-scene", Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: start.UnixMilli(), Source: f.Source,
		Features: &pb.Features{ModelId: "controlled-scene-metadata-fixture", Tags: map[string]string{"event": "scene"}}}
	payloads := map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: fact.FromItem(it).Marshal()}
	for _, form := range f.Forms {
		if form.Kind != "THUMB" && form.Kind != "FULL" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(fixtureDir, form.File))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != form.SHA {
			t.Fatal("fixture hash")
		}
		payloads[pb.FormKind(pb.FormKind_value["FORM_KIND_"+form.Kind])] = b
	}
	if _, err = edge.Put(ctx, it, payloads); err != nil {
		t.Fatal(err)
	}
	row, err := edge.Get(ctx, it.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = sched.ScoreItem(ctx, row); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && clk.Now().Sub(start) < 60*time.Second; i++ {
		wait, err := sched.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if wait > 0 {
			clk.Advance(wait)
		}
	}
	row, err = edge.Get(ctx, it.Id)
	if err != nil {
		t.Fatal(err)
	}
	fullKey := it.Id + "/" + pb.FormKind_FORM_KIND_FULL.String()
	if _, ok := arrivals[fullKey]; ok {
		t.Fatal("scene FULL arrived before explicit pull")
	}
	if row.Form(pb.FormKind_FORM_KIND_FULL).AckedThrough != 0 {
		t.Fatal("scene FULL sent before pull")
	}
	if !row.Form(pb.FormKind_FORM_KIND_THUMB).Complete {
		t.Fatal("scene thumbnail never completed")
	}
	before := clk.Now().Sub(start).Seconds()
	wireBefore := r.sim.BytesOnAir
	if err = sched.Pull(ctx, &pb.Pull{ItemId: it.Id, Form: pb.FormKind_FORM_KIND_FULL, RequestedBy: "research-explicit-full-pull"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		wait, err := sched.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row, err = edge.Get(ctx, it.Id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Form(pb.FormKind_FORM_KIND_FULL).Complete {
			break
		}
		if wait > 0 {
			clk.Advance(wait)
		}
	}
	if !row.Form(pb.FormKind_FORM_KIND_FULL).Complete {
		t.Fatal("pulled FULL did not finish")
	}
	got, err := shore.ReadForm(it.Id, pb.FormKind_FORM_KIND_FULL)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(got) != sha256.Sum256(payloads[pb.FormKind_FORM_KIND_FULL]) {
		t.Fatal("pulled payload mismatch")
	}
	result := map[string]any{"revision": os.Getenv("REPLAY_REVISION"), "scenario": "controlled scene tag plus explicit full pull",
		"synthetic_metadata_not_image_classification": true, "default_observation_end_s": before,
		"full_sent_before_pull": false, "wire_bytes_before_pull": wireBefore, "pull_requested_s": before,
		"full_shore_received_s": arrivals[fullKey], "full_edge_acked_s": r.ackTimes[fullKey],
		"full_payload_bytes": len(got), "wire_bytes_total": r.sim.BytesOnAir, "arrivals": arrivals, "exchanges": r.events,
		"http_auth_middleware_exercised": false}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(outputDir, "scene-explicit-pull.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}
