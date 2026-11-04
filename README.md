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
cmd/ghost-seeker web/   operator web app (Go, in-memory simulation, SSE feed); see "Operator web app" below
ui/                     API notes for wiring the web app to the edge/shore consoles
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

## Payload encoding

Form generation (`edge/ingest/forms.go`) and the transport (`edge/link`) own
compression; the scheduler only sees each form's byte count and `Mime`.

| form | encoding | defaults (`ingest.DefaultConfig`, `bin/edge` flags) |
|---|---|---|
| `FACT` | compact JSON (`internal/fact`) | — |
| `THUMB` | JPEG, fitted within W×H (aspect kept, never upscaled), CatmullRom downscale; grayscale (IR) stays single-channel | 160×160, q55 (`-thumb-width`, `-thumb-height`, `-thumb-quality`) |
| `CROP` | JPEG at native resolution of the union of the analyzer's detection boxes, padded per side by a fraction of the box size. Skipped when there are no boxes or the crop would exceed 60% of the frame (FULL is the next useful rung) | pad 0.15, q80 (`-crop-pad`, `-crop-quality`) |
| `FULL` | JPEG/WebP captures are sent byte-for-byte (re-encoding only adds generation loss); PNG and other formats are re-encoded as JPEG when that is smaller | q90; `-full-quality 0` always sends the original bytes |

`Mime` is sniffed from each form's bytes when it is stored (`edge/store`), so
it always matches what was produced; shore stores it per form and serves
`/blob/...` with that `Content-Type`. JPEG rather than WebP because Go has no
pure-Go lossy WebP encoder (`x/image/webp` only decodes) and the build is
CGO-free; an encoder can be swapped in inside `forms.go` without touching
the scheduler, link or shore.

On the replay frames, THUMB averages ~2.1 KB (was ~2.5 KB with the previous
bilinear 160 px-wide resize), CROP ~8 KB, FULL
~68 KB.

The Connect/gRPC link gzips `ExchangeRequest`s (BestSpeed, messages ≥ 512 B)
and the shore `SyncService` handler gzips responses to edges that accept it
(`link.HandlerOptions`). Manifest batches and FACT JSON shrink ~10× (a
65-item manifest+FACT exchange: 16 KB → 1.4 KB); JPEG chunks are already
compressed and pass through at negligible overhead. The link estimator and
budget stay in uncompressed protobuf bytes, so compression shows up as a
higher estimated rate. The in-process simulator (`link.Sim`) charges
uncompressed bytes.

## Simulator results (satcom_contested, 60 min, mock analyzer)

| strategy | first unknown fact at shore | first unknown full | manifests at shore | unknowns at FACT | FULL images |
|---|---|---|---|---|---|
| FIFO | never | 14 s | 265 / 360 | 0 / 5 | 265 |
| prioritized | 3 s | 7 s | 360 / 360 | 5 / 5 | 208 |

FIFO wins raw image count because it spends zero bytes on manifests, facts,
thumbnails and crops; prioritized gives shore a complete picture of the backlog and
every unknown, and puts the bytes it has into what the operator asked for.

## Operator web app

The operator UI lives in `cmd/ghost-seeker` + `web/` (Go 1.22+, stdlib only, synthetic imagery and its own in-memory simulation for now; wiring it to the edge/shore `ConsoleService` is the next step). It defaults to `:8080`, which the shore service also uses, so pick another port when running both:

```sh
go run ./cmd/ghost-seeker -addr :8090
```

Flags `-addr`, `-retain`, and `-backfill` set the listen address, item retention, and number of historical items generated at startup. Pages:

- **Imagery Feed** — browse prioritized sensor imagery, open an item for detections and processing details, and set its priority.
- **Review** — `1`–`4` set priority, `Space` accept the model suggestion, `X` dismiss, `C` send to the commander queue, `→`/`S` skip, `Z` undo, `N` night mode.
- **Collection Priorities** — live iterative workbench: debounced previews, weighted/source/time-scoped editable chips, scored specificity suggestions, image corrections, tier pins, version comparison/revert, Ctrl+Z / Ctrl+Shift+Z. J/K or ←/→ move image focus; ↑/↓/X propose corrections. Apply publishes the draft and pins to all browser sessions. Active directives, the base-model selector, and effective object weights remain on the page.
- **Commander's Board** (`/cdr`) — separate final-review view for images the watch floor escalated (`C`), ordered by tier, command score, then wait time; shows the frame, contacts, escalation reason and chain of custody. Keys: `1` take action, `2` track, `3` forward to intel, `4` no further action, `B` return to the watch floor, `/` note, `Z` reopen, `↑`/`↓` move, `Tab` Awaiting/Decided. Decisions go to `POST /api/items/{id}/decision` and fan out over SSE. A simulated watch officer escalates the top unreviewed P1/P2 image about every 15 s; `-auto-cdr 0` turns it off. Escalated images survive the `-retain` limit.

Preview adds draft rules on top of active directives without mutating server state. Apply adds a new active directive; Reset clears only the local draft. Time-scoped directives are refreshed every 30 seconds so expired boosts disappear. The feed continues streaming during iteration. Keyword/regex parsing does not use an LLM; operator app state is in memory and resets on restart.

The [standalone design reference](ui-mockup/reprioritize.html) retains 80 seeded synthetic images and simulated apply; open it directly without a server.

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
