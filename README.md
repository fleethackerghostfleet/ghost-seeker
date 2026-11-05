# ghost-seeker

Ship-to-shore **prioritized sync** for disconnected, low-bandwidth links.
Field devices collect far more than the link can carry; this decides, every
time a connection is available, *what to send, in what order, and in what
form*, so the most important information reaches shore first and nothing is
lost. Images are the first data type; the item model is type-agnostic.

See [Fielding](docs/FIELDING.md) for offline builds, container staging, and Zarf deployment.

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
algorithm/              standalone CPU image providers, color+pHash ranking, whole-JPEG selection/evaluation
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

## Configuration: unit sync authentication and edge storage caps

Both features are opt-in. Defaults preserve existing compose/simulator behavior:
sync auth is off, and edge storage is unlimited (no age/staleness deletion).

| Process | Flag | Environment | Default |
|---|---|---|---|
| shore | `-require-auth` | `GS_REQUIRE_AUTH` | `false` |
| shore / mint tool | `-signing-key` | `GS_SIGNING_KEY` | unset |
| shore / mint tool | `-signing-key-file` | `GS_SIGNING_KEY_FILE` | unset |
| edge | `-unit-token` | `GS_UNIT_TOKEN` | unset |
| edge | `-unit-token-file` | `GS_UNIT_TOKEN_FILE` | unset |
| edge | `-max-bytes` | `GS_EDGE_MAX_BYTES` | `0` (unlimited) |
| edge | `-max-items` | `GS_EDGE_MAX_ITEMS` | `0` (unlimited) |

File credentials take precedence over literal/env credentials. Shore requires a
non-default signing key of at least 32 bytes when auth is enabled; there is no
built-in secret. Prefer environment injection or a protected, read-only secret
mount over flags (flags can appear in process listings). Never log credentials.

With the same signing key securely provisioned to shore and the offline mint tool:

```sh
# GS_SIGNING_KEY is supplied by your secret manager, or use GS_SIGNING_KEY_FILE.
go run ./cmd/shore -require-auth
GS_UNIT_TOKEN=$(go run ./cmd/mint-unit-token -id edge-1 -ttl 1h)
export GS_UNIT_TOKEN
go run ./cmd/edge -id edge-1 -max-bytes 1073741824 -max-items 10000
```

The mint tool defaults to the reference tooling's one-hour TTL; `-ttl` accepts a
Go duration (e.g. `5m`). Tokens use **HS256** and require `sub` (edge node ID),
`role: "unit"`, and `exp`. Missing/invalid/expired tokens return Connect
`Unauthenticated`; an `ExchangeRequest.edge_id` different from `sub` returns
`PermissionDenied`. The SyncService interceptor covers unary calls and checks
every received message on streaming handlers (the current proto only defines
unary Exchange). No relay role or three-tier relay is implemented. The edge reads
its token at startup: provision a fresh token and restart before expiry. This is
sync authentication only, not operator/ConsoleService auth or TLS; deploy behind
an approved TLS boundary and restrict console access to a trusted network.

Caps count retained item rows and the sum of their form payload sizes, not
SQLite/WAL overhead or audit history. Negative caps are rejected; either cap may
be set independently. To make room, the edge evicts **only fully delivered**
items: manifest ACKed and **every stored form** ready, complete, and ACKed through
its full byte length. A delivered FACT does not make an undelivered FULL safe.
Eviction order is lowest persisted scheduler priority first, then oldest capture
time (ingest sequence breaks ties). Pending shipboard corrections, active pull state, and persistent local pins
(`edge/store.SetPinned`) protect items; pins are not a new console/proto API.

Eviction removes the local item and its blobs, not shore's received copies.
Every eviction emits a structured `slog` event and a durable `eviction_log` row
in `edge.db` (`item_id`, `bytes`, `score`, `reason`, `ts` in Unix milliseconds).
Cleanup markers resume interrupted blob removal after restart; audit history
and the cumulative counter survive restart. SQLite reuses freed pages rather
than necessarily shrinking its database file. Local hash deduplication covers
retained items; a later capture of already-evicted content can become a new item.

If only protected items remain, admission returns `store.ErrFull` and logs a
warning instead of deleting them. The edge replay ingester waits on that capture
and retries every five seconds, without consuming the next capture. Other store
callers must retain/retry input on `ErrFull`. New admission is conservative for
all values: an urgent new capture never justifies deleting an unACKed item. An
existing oversized protected backlog is retained and drains normally; caps are
rechecked after ACKs and downlink pulls are applied. A capture larger than the
byte cap cannot fit: raise the cap or change intake, rather than dropping it.
Growing an existing form is also checked before writing it.
Sighting form updates wait/retry just like new-item intake. A downlinked
correction blocked by the cap stops the downlink cursor before that correction
for retry, while preserving progress through already-applied messages.

`GetStatusResponse.items_evicted` reports cumulative evictions. The live-shore
operator app displays it alongside the existing edge queue status.

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

## Offline image prioritization baseline

[`algorithm/`](algorithm/README.md) provides the standalone CPU workflow **analyze once → rank every eligible image → select whole JPEGs within an absolute byte allowance**. Object, visual-interest and quality providers are interchangeable. The default image-redundancy comparison combines color layout with pHash. Operator policy can rerank cached evidence; changing bandwidth reuses that order.

- [Provider contracts and adapters](algorithm/PROVIDERS.md)
- [Ranking, byte accounting and CLI examples](algorithm/RANKING_AND_SELECTION.md)
- [Color + pHash defaults, cache migration and pair evaluation](algorithm/SIMILARITY.md)

This package is an offline image-level baseline. It is independent of `edgeml/` sighting aggregation and the live Go scheduler's manifest/fidelity/chunk planning. It does not implement the `InferenceService` RPCs or wire itself into the operator app. Its full-image selection metrics are distinct from unique-object delivery metrics in `edgeml/`.

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

## Prioritization benchmark

Unknown contacts are injected in 12% of frames using a separate seeded RNG (library contacts and false positives are unchanged). Persistent unknowns default to P2; approaching or dark unknowns to P1, in every mission, even at low classification confidence. Transient flicker defaults to P4. Persistence is simulated track evidence, not a new vision detector. No restricted-area/geofencing model is implemented.

Compare `go run ./cmd/bench -unknowns=false` (original frames), `go run ./cmd/bench -before-unknown` (same injected contacts, closed-set command/edge weights), and `go run ./cmd/bench` (unknown-aware). The extra table reports persistent unknown edge retention and joint retention/≤P2 scoring, approaching P1 scoring, and transient P4 scoring; contact scores avoid credit from unrelated high-priority frame contacts.

`go run ./cmd/bench` scores edge triage and each command model against a written mission oracle (`internal/bench/oracle.go`) on deterministic synthetic frames: generated-library contacts with drawn confidence and track attributes, plus a spurious detection in 30% of frames. The oracle ignores confidence and false positives; each model is judged against its own mission (maritime security, air picture, search and rescue). Tune on seeds 1–3; report the held-out default seeds 101–103 (6,000 frames each).

| Metric (held-out) | Maritime before → after | Air before → after | SAR before → after |
|---|---|---|---|
| Tier exact | 38.6% → 77.8% | 43.8% → 91.2% | 41.9% → 79.8% |
| P1 recall | 54.0% → 89.7% | 65.2% → 91.5% | 76.4% → 99.3% |
| P1 precision | 29.6% → 75.4% | 51.0% → 92.6% | 9.7% → 73.3% |
| Critical miss (oracle P1 scored P3/P4) | 8.8% → 0.0% | 5.2% → 0.0% | 0.0% → 0.0% |
| False flash (oracle P3/P4 scored P1) | 10.8% → 1.1% | 4.9% → 0.5% | 20.1% → 0.2% |
| NDCG@20 per 200-frame window | 0.663 → 0.979 | 0.816 → 0.979 | 0.559 → 0.977 |
| Review burden (share of window to reach every P1) | 80.8% → 52.2% | 45.2% → 21.4% | 46.3% → 5.7% |

Edge triage drops 6.5% of frames and 0.1% of oracle P1 frames. Model changes: frame score is the top contact plus a damped (`CrowdShare` 0.1) noisy-or of the rest, so crowds of minor contacts no longer stack into P1; confidence saturates at `ConfSat` 0.7; per-model attribute lifts (approaching, dark, loitering) replace fixed multipliers and skip clutter. `TestModelsMeetFloor` fails if a change pushes any model below 0.95 NDCG, 85% P1 recall or above 1% critical miss. The oracle encodes one plausible commander intent and the confidences are simulated, so these numbers measure fit to that spec, not field accuracy.

### Live shore mode (`-shore`)

```sh
go run ./cmd/ghost-seeker -addr :8090 -shore http://localhost:8080 -edge http://localhost:8081
```

Backs the same app with a running shore node instead of the generator: `internal/shorebridge`
polls the shore ConsoleService (`ListItems`/`GetStatus`/`GetPolicy`) once a second and streams
snapshots to the browser over SSE; `/blob/*` is proxied to shore. Items are grouped into one
card per `features.tags.sighting_id` (falling back to one card per item), showing the best
received form (crop > thumb > full, fact text otherwise) and a chip per form
(fact/thumb/crop/full: offered / pending / partial / received). Directives call shore
`SetPolicy`, "Pull full image" calls `RequestPull`, "Relabel" calls `OverrideLabel`.

The process also hosts the ship→shore link: `internal/shaper` listens on `-link-listen`
(default `:18080`) and forwards to shore with a token bucket, so start the edge with
`-shore http://localhost:18080` and the 0 / 9.6 kbps / 64 kbps / 256 kbps / 2 Mbps buttons
shape the real traffic. Pass `-link-listen ""` when the link is shaped elsewhere (compose +
Toxiproxy, `deploy/compose/degrade.sh 9k6|64k|256k|2m`). See `docs/DEMO.md` for the demo.

## Status / not done

- Inference: mock + Python stub only. Planned sidecar: ONNX Runtime with the
  OpenVINO execution provider, YOLO-class detector + MobileCLIP-class embedder
  (CPU). See `inference/README.md`.
- Natural-language interest compilation lives in the `dynamic-interest` repo;
  this repo consumes the compiled `Policy` (labels, vectors, thresholds).
- No TLS on either service; SyncService unit auth is opt-in (see Configuration above) and ConsoleService has no auth.
- Shore chunk reassembly assumes in-order delivery within an item/form.
- Sailboat footage is in `frames/Sailboat_on_the_horizon_frames.zip` (unlabelled;
  the boat is not visible in every frame). `scenarios/sailboats_example.json`
  still uses a placeholder path for the sailboat set.
