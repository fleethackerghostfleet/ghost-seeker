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
make build                 # bin/edge bin/ghost-seeker bin/shore bin/sim
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

## Operator web app

The operator UI lives in `cmd/ghost-seeker` + `web/` (Go 1.23+, stdlib only, synthetic imagery and its own in-memory simulation for now; wiring it to the edge/shore `ConsoleService` is the next step). It defaults to `:8080`, which the shore service also uses, so pick another port when running both:

```sh
go run ./cmd/ghost-seeker -addr :8090
```

The app imagery is AI-generated synthetic imagery; see [tools/imagegen](tools/imagegen/README.md).

Flags `-addr`, `-retain`, and `-backfill` set the listen address, item retention, and number of historical items generated at startup. Pages:

- **Imagery Feed** — browse prioritized sensor imagery, open an item for detections and processing details, and set its priority.
- **J2 briefing** — in an image's detail drawer, add individual vessel detections to the briefing selection (other object types are excluded). Choose **Create J2 brief** in the feed toolbar to freeze evidence, including locally available imagery with the selected detection highlighted. The integrated `#briefing` workspace provides editable title, analyst assessment and intelligence gaps; combined slides paginate at four observations, or use one slide per observation. Export editable 16:9 PowerPoint or **Print / Save PDF** (opens a printable page; choose Save as PDF in the browser). Export is local and needs no external service. Drafts live only in the current tab and are lost on reload. A new snapshot explicitly replaces evidence without changing review/commander state. Different observations may depict the same vessel: remove duplicates manually; there is no verified track identity, affiliation or geospatial data. Missing images get explicit placeholders. All slides retain the training marking.
- **Review** — `1`–`4` set priority, `Space` accept the model suggestion, `X` dismiss, `C` send to the commander queue, `→`/`S` skip, `Z` undo, `N` night mode.
  Repeated overrides can prompt a one-key directive suggestion; when contacts are close, Review can ask which object caused the override.
  Suggestions stay client-side until accepted, and accepted rules appear on the Commander's Board under “Learned from operator”.
- **Collection Priorities** — live iterative workbench: debounced previews, weighted/source/time-scoped editable chips, scored specificity suggestions, image corrections, tier pins, version comparison/revert, Ctrl+Z / Ctrl+Shift+Z. J/K or ←/→ move image focus; ↑/↓/X propose corrections. Apply publishes the draft and pins to all browser sessions. Active directives, the base-model selector, and effective object weights remain on the page.
- **Commander's Board** (`/cdr`) — separate final-review view for images the watch floor escalated (`C`), ordered by tier, command score, then wait time; shows the frame, contacts, escalation reason and chain of custody. Keys: `1` take action, `2` track, `3` forward to intel, `4` no further action, `B` return to the watch floor, `/` note, `Z` reopen, `↑`/`↓` move, `Tab` Awaiting/Decided. Decisions go to `POST /api/items/{id}/decision` and fan out over SSE. A simulated watch officer escalates the top unreviewed P1/P2 image about every 15 s; `-auto-cdr 0` turns it off. Escalated images survive the `-retain` limit.

Preview adds draft rules on top of active directives without mutating server state. Apply adds a new active directive; Reset clears only the local draft. Time-scoped directives are refreshed every 30 seconds so expired boosts disappear. The feed continues streaming during iteration. Keyword/regex parsing does not use an LLM; operator app state is in memory and resets on restart.

The [standalone design reference](ui-mockup/reprioritize.html) retains 80 seeded synthetic images and simulated apply; open it directly without a server.

Briefing export vendors PptxGenJS 3.12.0 (MIT, `web/static/pptxgen.LICENSE`) with its bundled JSZip dependency. `pptxgen.bundle.js` is the unmodified npm distribution with its source-map reference removed. No CDN or runtime npm install is required. Run web regression checks with `node --test web/*.test.cjs` (Node 18+); `node --check web/static/briefing*.js` checks syntax. Preview, print and PowerPoint share slide geometry; PowerPoint uses editable text and embedded raster images. PowerPoint font rendering may vary by viewer.

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
