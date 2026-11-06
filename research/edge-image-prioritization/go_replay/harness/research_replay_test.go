//go:build research_replay

package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"google.golang.org/protobuf/proto"
)

type replayFixture struct {
	ID         string    `json:"id"`
	Source     string    `json:"source_frame"`
	Label      string    `json:"label"`
	Confidence float32   `json:"confidence"`
	Box        []float32 `json:"box_normalized"`
	Forms      []struct {
		Kind  string `json:"kind"`
		File  string `json:"file"`
		Bytes int    `json:"bytes"`
		SHA   string `json:"sha256"`
	} `json:"forms"`
}

type replayLink struct {
	sim      *link.Sim
	clk      *clock.Virtual
	start    time.Time
	events   []map[string]any
	current  map[string]any
	ackTimes map[string]float64
}

func (r *replayLink) State() *pb.LinkState { return r.sim.State() }

func (r *replayLink) Exchange(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	chunks := []map[string]any{}
	payload := 0
	for _, c := range req.Chunks {
		payload += len(c.Data)
		chunks = append(chunks, map[string]any{"item_id": c.ItemId, "kind": c.Form.String(), "offset": c.Offset, "bytes": len(c.Data), "total_bytes": c.TotalBytes})
	}
	e := map[string]any{"start_s": r.clk.Now().Sub(r.start).Seconds(), "request_proto_bytes": proto.Size(req),
		"request_framed_bytes": proto.Size(req) + 64, "chunk_payload_bytes": payload, "chunks": chunks, "manifests": len(req.Manifest)}
	r.current = e
	before := r.sim.BytesOnAir
	resp, err := r.sim.Exchange(ctx, req)
	e["end_s"] = r.clk.Now().Sub(r.start).Seconds()
	e["charged_wire_bytes"] = r.sim.BytesOnAir - before
	e["success"] = err == nil
	if err != nil {
		e["error"] = err.Error()
	}
	if resp != nil {
		e["response_proto_bytes"] = proto.Size(resp)
		e["response_framed_bytes"] = proto.Size(resp) + 64
		e["acks"] = resp.Acks
		for _, a := range resp.Acks {
			if a.Complete && !a.Manifest {
				key := a.ItemId + "/" + a.Form.String()
				if _, ok := r.ackTimes[key]; !ok {
					r.ackTimes[key] = r.clk.Now().Sub(r.start).Seconds()
				}
			}
		}
	}
	r.events = append(r.events, e)
	return resp, err
}

func TestResearchRealPayloadReplay(t *testing.T) {
	fixtureDir := os.Getenv("REPLAY_FIXTURES")
	outputDir := os.Getenv("REPLAY_OUT")
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
	for _, rate := range []float64{9600, 64000} {
		for _, outage := range []bool{false, true} {
			name := fmt.Sprintf("rate%d-outage%v", int(rate), outage)
			t.Run(name, func(t *testing.T) {
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
				arrivalEvents := []map[string]any{}
				service.OnArrival = func(id string, kind pb.FormKind) {
					key := id + "/" + kind.String()
					at := clk.Now().Sub(start).Seconds()
					if _, ok := arrivals[key]; !ok {
						arrivals[key] = at
					}
					arrivalEvents = append(arrivalEvents, map[string]any{"item_id": id, "kind": kind.String(), "at_s": at})
				}
				trace := link.Trace{Name: name, Windows: []link.Window{{Duration: time.Hour, Bps: rate, RTT: 600 * time.Millisecond}}}
				if outage {
					trace.Windows = []link.Window{{Duration: 2 * time.Second, Bps: rate, RTT: 600 * time.Millisecond},
						{Duration: 10 * time.Second, Bps: 0, RTT: 600 * time.Millisecond}, {Duration: time.Hour, Bps: rate, RTT: 600 * time.Millisecond}}
				}
				handler := func(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
					r.current["shore_handler_s"] = clk.Now().Sub(start).Seconds()
					resp, err := service.Exchange(ctx, req)
					if resp != nil {
						r.current["shore_response_proto_bytes"] = proto.Size(resp)
					}
					return resp, err
				}
				r.sim = link.NewSim(trace, clk, handler, 20261105)
				cfg := DefaultConfig("research-replay")
				sched, err := New(cfg, edge, r, scoring.Default(), clk, nil)
				if err != nil {
					t.Fatal(err)
				}
				expected := map[string][]byte{}
				for _, f := range fixtures {
					it := &pb.Item{Id: f.ID, Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: start.UnixMilli(), Source: f.Source,
						Features: &pb.Features{ModelId: "frozen-nanodet-development-cache", Detections: []*pb.Detection{{Label: f.Label, Confidence: f.Confidence,
							Box: &pb.Box{X: f.Box[0], Y: f.Box[1], W: f.Box[2], H: f.Box[3]}}}}}
					payloads := map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: fact.FromItem(it).Marshal()}
					for _, form := range f.Forms {
						b, err := os.ReadFile(filepath.Join(fixtureDir, form.File))
						if err != nil {
							t.Fatal(err)
						}
						h := sha256.Sum256(b)
						if len(b) != form.Bytes || hex.EncodeToString(h[:]) != form.SHA {
							t.Fatal("fixture integrity")
						}
						kind := pb.FormKind(pb.FormKind_value["FORM_KIND_"+form.Kind])
						payloads[kind] = b
					}
					for k, b := range payloads {
						expected[f.ID+"/"+k.String()] = b
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
				}
				ticks := 0
				for ticks < 300 && clk.Now().Sub(start) < 600*time.Second {
					ticks++
					wait, _ := sched.Tick(ctx) // Expected outage failures remain recorded by real link.
					rows, err := edge.All(ctx)
					if err != nil {
						t.Fatal(err)
					}
					done := true
					for _, row := range rows {
						for _, f := range row.Forms {
							if !f.Complete {
								done = false
							}
						}
					}
					if done {
						break
					}
					if wait > 0 {
						clk.Advance(wait)
					}
				}
				verified := []map[string]any{}
				expectedBytes := 0
				for _, fixture := range fixtures {
					edgeRow, err := edge.Get(ctx, fixture.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, f := range edgeRow.Forms {
						key := fixture.ID + "/" + f.Kind.String()
						b := expected[key]
						expectedBytes += len(b)
						if !f.Complete {
							t.Fatalf("form incomplete after bounded replay: %s", key)
						}
						actual, err := shore.ReadForm(fixture.ID, f.Kind)
						if err != nil {
							t.Fatal(err)
						}
						wantHash := sha256.Sum256(b)
						gotHash := sha256.Sum256(actual)
						if wantHash != gotHash {
							t.Fatalf("shore payload mismatch: %s", key)
						}
						if arrivals[key] > r.ackTimes[key] {
							t.Fatal("ACK predates shore receipt")
						}
						verified = append(verified, map[string]any{"key": key, "bytes": len(b), "sha256": hex.EncodeToString(gotHash[:]),
							"shore_received_s": arrivals[key], "edge_complete_ack_s": r.ackTimes[key]})
					}
				}
				result := map[string]any{"case": name, "rate_bps": rate, "outage": outage, "rtt_s": .6,
					"trace": trace, "scheduler_config": cfg, "ticks": ticks, "virtual_elapsed_s": clk.Now().Sub(start).Seconds(),
					"wire_bytes": r.sim.BytesOnAir, "link_exchanges": r.sim.Exchanges, "link_failed_exchanges": r.sim.Failed,
					"unique_payload_bytes": expectedBytes, "verified_forms": verified, "shore_arrival_events": arrivalEvents,
					"exchanges": r.events, "scope": "two-image queue-drain integration fixture; not source-clip latency or benchmark equivalence"}
				if err = os.MkdirAll(outputDir, 0755); err != nil {
					t.Fatal(err)
				}
				data, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(outputDir, name+".json"), data, 0644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wire=%d payload=%d forms=%d virtual_s=%.6f failures=%d", r.sim.BytesOnAir, expectedBytes, len(verified), clk.Now().Sub(start).Seconds(), r.sim.Failed)
			})
		}
	}
}
