# ghost-seeker

Ship-to-shore **prioritized sync** for disconnected, low-bandwidth links.
Field devices collect far more than the link can carry; this decides, every
time a connection is available, *what to send, in what order, and in what
form*, so the most important information reaches shore first and nothing is
lost. Images are the first data type; the item model is type-agnostic.

Key behaviours (all covered by `go test ./...` and the simulator):

- **Unknown-first.** Items the model can't classify score highest by default.
- **Manifest-first.** A ~60-byte entry per item goes ahead of any payload, so
  shore always knows what exists and what is still pending.
- **Fidelity ladder.** Each image goes up as `FACT` (compact JSON, <300 B) →
  `THUMB` → `CROP` → `FULL`; ranking is priority-per-byte, so a partial
  window still delivers something useful.
- **Operator interest = deprioritize, never delete.** "Ignore sailboats"
  pushes their image bytes below everything else but their fact still goes,
  the item stays on the ship, and shore can **pull** any item at any fidelity.
- **Resumable.** Chunks resume from the acknowledged offset; acks are
  idempotent; a dropped link costs at most one in-flight exchange.
- **Bandwidth-adaptive.** The link estimator re-sizes every exchange; the
  planner re-ranks the whole backlog every tick. EMCON `SILENT`/`BURST` modes.
- **Interchangeable image model.** The edge only needs an `Analyzer`
  (`Analyze(image) → labels, boxes, embedding, unknownness`). A deterministic
  mock ships in-tree; a real CPU-only sidecar plugs in over the same contract.

## Layout

```
proto/ghostseeker/v1/   contracts: Item/Form, Policy (interest, thresholds, EMCON),
                        SyncService (edge↔shore), ConsoleService (UI), InferenceService
gen/                    generated Go (buf + connect-go); committed, regenerate with `make gen`
edge/                   store (SQLite + blobs), scoring (pluggable scorers), scheduler,
                        link (Connect transport + simulator with link traces), ingest, inference, console
shore/                  store, sync receiver, console
internal/               fact format, virtual clock, scenario loader, http helpers
cmd/edge cmd/shore      the two services
cmd/sim                 FIFO vs prioritized under a link trace, virtual time, JSON metrics
inference/              Python sidecar contract + stub (no model yet)
deploy/                 Dockerfile, docker compose with Toxiproxy link shaping, Zarf skeleton
frames/                 demo imagery (Git LFS zips)
ui-mockup/              self-contained HTML mockup of the operator UI (open in a browser)
ui/                     where the real console will live; served by edge/shore at /
scenarios/              replay scenarios (datasets + operator events)
```

## Quick start

Requires Go 1.23+. No CGO, no GPU. The demo imagery in `frames/` is Git LFS: run `git lfs pull` after cloning, or the default replay scenario finds only pointer files.

```sh
make build                 # bin/edge bin/shore bin/sim
make sim                   # FIFO vs prioritized, 90 simulated minutes of contested SATCOM, seconds of wall time
./bin/sim -trace starlink_turns -duration 60m          # traces: steady_64k trickle_2k4 starlink_turns satcom_contested los_bursty
```

Live, two processes, replaying the frame zips through the mock analyzer:

```sh
make run-shore             # :8080  SyncService + shore console + ui/
make run-edge              # :8081  edge console; syncs to shore
curl -s -H content-type:application/json -d '{}' localhost:8080/ghostseeker.v1.ConsoleService/GetStatus
curl -s -H content-type:application/json -d '{"limit":10}' localhost:8081/ghostseeker.v1.ConsoleService/GetQueue
# operator on shore: "ignore cargo ships" -> downlinked on the next exchange, edge re-ranks
curl -s -H content-type:application/json -d '{"policy":{"issuedBy":"ops","text":"ignore cargo ships","interest":{"suppressLabels":["cargo ship"]}}}' \
  localhost:8080/ghostseeker.v1.ConsoleService/SetPolicy
# analyst pulls the full image of an item shore only has the fact for
curl -s -H content-type:application/json -d '{"pull":{"itemId":"<id>","form":"FORM_KIND_FULL","requestedBy":"analyst"}}' \
  localhost:8080/ghostseeker.v1.ConsoleService/RequestPull
```

With a shaped network instead of the in-process simulator:

```sh
make compose                       # shore, toxiproxy, edge
deploy/compose/degrade.sh satcom   # 64 kbps / 600 ms; also trickle | starlink | down | up
```

## How ranking works

```
priority = Σ wᵢ·valueᵢ × Π multiplierᵢ × freshness(age, type) + pull boost
```

Scorers (`edge/scoring`): `BaseValue` (per item type), `Unknown` (weight 3.0
on the analyzer's unknownness; a human label resolves it), `Interest`
(operator boost/suppress labels and embedding vectors from the current
policy), `Confidence`. `Freshness` decays per type (telemetry fast, records
slow) with a floor so nothing ever reaches zero. Add a scorer by implementing
`scoring.Scorer` and passing it to `scheduler.New`.

The planner (`edge/scheduler.BuildPlan`) then: sends manifest entries for
anything shore hasn't seen; ranks every item's *next* form by
`priority × ladder_weight / bytes_remaining`; fills the exchange in rank
order, promoting an item's next rung when a form fits entirely; suppressed or
below-threshold images sort in a lower tier and drain only when nothing
better is waiting ("if we have bandwidth, use it").

## Simulator results (satcom_contested, 60 min, mock analyzer)

| strategy | first unknown fact at shore | first unknown full | manifests at shore | unknowns at FACT | FULL images |
|---|---|---|---|---|---|
| FIFO | never | 14 s | 265 / 360 | 0 / 5 | 265 |
| prioritized | 4 s | 8 s | 360 / 360 | 5 / 5 | 208 |

FIFO wins raw image count because it spends zero bytes on manifests, facts and
thumbnails; prioritized gives shore a complete picture of the backlog and
every unknown, and puts the bytes it has into what the operator asked for.

## UI mockup

`ui-mockup/index.html` is a self-contained interactive mockup of the Ghost Seeker operator UI (open it in a browser, no server needed). All imagery is synthetic.

- **Live Feed** – dense grid of prioritized images from edge sensors; click an image for detections, priority reasoning, and processing chain.
- **Review queue** – one image at a time for sleep-deprived reviewers: `1`–`4` set P1–P4, `Space` agree with model, `X` not relevant, `C` send to CDR, `→` skip, `Z` undo, `N` night (red) mode.
- **Reprioritize** – natural-language directives (keyword-based in the mockup) that rescore the feed.

## Status / not done

- Inference: mock + Python stub only. Planned sidecar: ONNX Runtime with the
  OpenVINO execution provider, YOLO-class detector + MobileCLIP-class embedder
  (CPU). See `inference/README.md`.
- Natural-language interest compilation lives in the `dynamic-interest` repo;
  this repo consumes the compiled `Policy` (labels, vectors, thresholds).
- No auth/TLS on either service. Zarf package is a skeleton.
- Shore chunk reassembly assumes in-order delivery within an item/form.
- No sailboat dataset in `frames/` yet; `scenarios/sailboats_example.json`
  shows where it plugs in.
