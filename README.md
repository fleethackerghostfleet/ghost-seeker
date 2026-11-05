# ghost-seeker

Ship-to-shore **prioritized sync** for disconnected, low-bandwidth links.
Field devices collect far more than the link can carry; this decides, every
time a connection is available, *what to send, in what order, and in what
form*, so the most important information reaches shore first and nothing is
lost. Images are the first data type; the item model is type-agnostic.

See [Fielding](docs/FIELDING.md) for offline builds, container staging, and Zarf deployment.
See [Human review](docs/HUMAN_REVIEW.md) for durable review capture, exports, and offline weight proposals.

**Verification:** hosted CI is manual-only to conserve Actions allowance. Run
`make verify` on a prepared Linux host for the complete fail-closed test/security/
package/image suite. See [local verification](docs/VERIFY.md) for setup, commit-linked
reports, and merge-policy limitations. No GitHub plan upgrade is needed.

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
inference/              inference contract notes + model-free Python stub
edgeml/                 Python detection/tracking/sighting pipeline, real-model sidecar, evaluation reports
deploy/                 Dockerfile, docker compose with Toxiproxy link shaping, Zarf skeleton
frames/                 demo imagery (Git LFS zips)
cmd/ghost-seeker web/   operator web app (Go, in-memory simulation, SSE feed); see "Operator web app" below
ui/                     API notes for wiring the web app to the edge/shore consoles
scenarios/              replay scenarios (datasets + operator events)
```

## Quick start

Requires Go 1.26.10+ (pinned in `go.mod` and the container builder). No CGO, no GPU. The demo imagery in `frames/` is Git LFS: run `git lfs pull` after cloning, or the default replay scenario finds only pointer files.

```sh
make build                 # bin/edge bin/ghost-seeker bin/shore bin/sim
make sim                   # FIFO vs prioritized, 90 simulated minutes of contested SATCOM, seconds of wall time
./bin/sim -trace starlink_turns -duration 60m          # traces: steady_64k trickle_2k4 starlink_turns satcom_contested los_bursty
```

Live, two processes, replaying the frame zips through the mock analyzer:

```sh
make run-shore             # :8080  SyncService + shore console + ui/
make run-edge              # :8081  edge console; syncs to shore
TOKEN=$(make -s demo-token)   # or: cat data/operator.token
curl -s -H "Authorization: Bearer $TOKEN" -H content-type:application/json -d '{}' localhost:8080/ghostseeker.v1.ConsoleService/GetStatus
curl -s -H "Authorization: Bearer $TOKEN" -H content-type:application/json -d '{"limit":10}' localhost:8081/ghostseeker.v1.ConsoleService/GetQueue
# operator on shore: "ignore cargo ships" -> downlinked on the next exchange, edge re-ranks
curl -s -H "Authorization: Bearer $TOKEN" -H content-type:application/json -d '{"policy":{"issuedBy":"ops","text":"ignore cargo ships","interest":{"suppressLabels":["cargo ship"]}}}' \
  localhost:8080/ghostseeker.v1.ConsoleService/SetPolicy
# analyst pulls the full image of an item shore only has the fact for
curl -s -H "Authorization: Bearer $TOKEN" -H content-type:application/json -d '{"pull":{"itemId":"<id>","form":"FORM_KIND_FULL","requestedBy":"analyst"}}' \
  localhost:8080/ghostseeker.v1.ConsoleService/RequestPull
```

With a shaped network instead of the in-process simulator, first provision
`GS_OPERATOR_TOKEN`, `GS_SIGNING_KEY`, and `GS_UNIT_TOKEN` as described in the
[Compose setup](docs/FIELDING.md#compose-demonstration), then run:

```sh
make compose                       # shore, toxiproxy, edge
deploy/compose/degrade.sh satcom   # 64 kbps / 600 ms; also trickle | starlink | down | up
```

## Configuration: operator authentication

The operator web app also supports optional **offline CAC/PIV mutual-TLS login** with
operator/viewer roles. See [CAC/PIV mode (offline)](docs/SECURITY.md#1a-cacpiv-mode-offline)
for deployment, air-gap trust/CRL loading, and a synthetic demo PKI generator. Token mode remains the default.

Shore ConsoleService, the edge console, `/blob/` on both and the operator web app share one
**operator bearer token**: `-operator-token` / `GS_OPERATOR_TOKEN` or `-operator-token-file` /
`GS_OPERATOR_TOKEN_FILE` (production, required), or `-dev-token-file` / `GS_DEV_TOKEN_FILE`
(default `data/operator.token`, auto-generated for a local demo; `none` to require an explicit
token). API calls need `Authorization: Bearer <token>` (401 otherwise); the web app exchanges it
for an `HttpOnly; SameSite=Strict` session cookie at `/login` (automatic for a loopback browser in
dev mode). CORS is same-origin unless `-cors-origins` / `GS_CORS_ORIGINS` lists an origin. Set
`-public-hosts` / `GS_PUBLIC_HOSTS` for any public DNS or ingress Host name. See `docs/SECURITY.md`
for the full control list and the Deep-scan before/after.

## Configuration: unit sync authentication and edge storage caps

Unit sync authentication is **on by default**: shore refuses to start without a
signing key unless `-require-auth=false` is passed explicitly (isolated lab links
only; it logs a warning). `make demo-shore` / `make demo-edge` share a generated
`data/unit.key` and mint the edge token automatically. Edge storage is unlimited
by default (no age/staleness deletion).

| Process | Flag | Environment | Default |
|---|---|---|---|
| shore | `-require-auth` | `GS_REQUIRE_AUTH` | `true` |
| shore / mint tool | `-signing-key` | `GS_SIGNING_KEY` | unset |
| shore / mint tool | `-signing-key-file` | `GS_SIGNING_KEY_FILE` | unset |
| edge | `-unit-token` | `GS_UNIT_TOKEN` | unset |
| edge | `-unit-token-file` | `GS_UNIT_TOKEN_FILE` | unset |
| edge | `-max-bytes` (form payloads plus originals kept for full-size overrides) | `GS_EDGE_MAX_BYTES` | `0` (unlimited) |
| edge | `-max-items` | `GS_EDGE_MAX_ITEMS` | `0` (unlimited) |

File credentials take precedence over literal/env credentials. Shore requires a
non-default signing key of at least 32 bytes when auth is enabled; there is no
built-in secret. Prefer environment injection or a protected, read-only secret
mount over flags (flags can appear in process listings). Never log credentials.

With the same signing key securely provisioned to shore and the offline mint tool:

```sh
# GS_SIGNING_KEY is supplied by your secret manager, or use GS_SIGNING_KEY_FILE.
go run ./cmd/shore
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


### Signed policies (opt-in)

Policies downlinked to the edge can be signed by the shore with an Ed25519 key so
the edge only applies command policy from a trusted issuer; the shore in turn only
signs higher-echelon (fleet/command) layers that arrive already signed by a key it
trusts, so a ship console cannot mint locked COMMAND rules under the shore's key.

| Process | Flag | Environment | Default |
|---|---|---|---|
| shore | `-policy-signing-key` (JSON private key from `policy-keygen`) | `GS_POLICY_SIGNING_KEY` | unset (unsigned) |
| shore | `-policy-keys` (JSON keyring trusted for higher-echelon layers) | `GS_POLICY_KEYS` | unset (higher layers immutable) |
| edge | `-require-signed-policy` | `GS_REQUIRE_SIGNED_POLICY` | `false` |
| edge | `-policy-keys` (JSON keyring; required with the flag above) | `GS_POLICY_KEYS` | unset |

```sh
make build demo-key
go run ./cmd/policy-keygen -id shore-1 -out-dir keys     # keys/shore-1.key.json (0600) + keys/keyring.json
(umask 077; ./bin/mint-unit-token -id edge-1 -ttl 24h -signing-key-file data/unit.key > data/edge.jwt)
# Run these in separate terminals; the Ed25519 policy key is separate from unit auth.
./bin/shore -signing-key-file data/unit.key -policy-signing-key keys/shore-1.key.json
./bin/edge -id edge-1 -unit-token-file data/edge.jwt -require-signed-policy -policy-keys keys/keyring.json
```

Unsigned or badly signed policies are rejected and audited; the edge keeps its
last-good policy. A signed higher-echelon policy must carry a `version` above the
one `GetPolicy` currently shows (ship edits continue that sequence), which keeps a
revoked command policy from being replayed. In EMCON `SILENT` the edge still exchanges an empty heartbeat so
the policy that lifts SILENT arrives.

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
| `CROP` | JPEG at native resolution. Sighting items (`-dedup sightings`): the key detection box ×1.5 about its centre, at least 96 px per side. Other items: the union of the analyzer's detection boxes, padded per side by a fraction of the box size. Skipped when there are no boxes, the crop would exceed 60% of the frame, or it would not be smaller than FULL (FULL is the next useful rung). Also skipped for unknown items (unknownness ≥ the policy's `unknownMin`, default 0.5): their whole frame is kept for later training, so they go THUMB → FULL | pad 0.15, q80 (`-crop-pad`, `-crop-quality`; `-crop-unknowns` to crop unknowns too) |
| `FULL` | JPEG/WebP captures are sent byte-for-byte (re-encoding only adds generation loss); PNG and other formats are re-encoded as JPEG when that is smaller, unless they have transparency (JPEG has no alpha) | q90; `-full-quality 0` always sends the original bytes |

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

### Operator override: full size, no compression

An operator can force an image to go at full size with no compression: a
`RequestPull` whose `pull` has `"original": true` (shore or edge console).
`requestedBy` is required, because this is a human decision that costs link
bandwidth. The edge then sends the capture's original bytes as FULL, undoing
any JPEG re-encode (it keeps the original whenever FULL was re-encoded), and
holds back that item's THUMB/CROP until FULL is complete. "No compression"
means the image bytes are not re-encoded; link gzip is lossless and leaves
them byte-identical. The pull boost puts it at the head of the
queue, and the edge event log records `pull-original` with the operator.

    TOKEN=$(make -s demo-token)
    curl -s -H "Authorization: Bearer $TOKEN" -H content-type:application/json localhost:8080/ghostseeker.v1.ConsoleService/RequestPull \
      -d '{"pull":{"itemId":"<id>","original":true,"requestedBy":"op1"}}'

In the operator web app this is the **Send full size** button in the image
drawer. It asks for the operator's name and posts to
`/api/items/{id}/full-res` with `{"by":"op1"}`; a blank `by` is rejected.
In live shore mode (`-shore`) the bridge calls shore `RequestPull` with
`original: true`. In the standalone simulation it only records the override
on the simulated item. Both modes use the same UI at `/`; the retired
`/shore.html` path redirects there.

## Offline image prioritization baseline

[`algorithm/`](algorithm/README.md) provides the standalone CPU workflow **analyze once → rank every eligible image → select whole JPEGs within an absolute byte allowance**. Object, visual-interest and quality providers are interchangeable. The default image-redundancy comparison combines color layout with pHash. Operator policy can rerank cached evidence; changing bandwidth reuses that order.

- [Provider contracts and adapters](algorithm/PROVIDERS.md)
- [Ranking, byte accounting and CLI examples](algorithm/RANKING_AND_SELECTION.md)
- [Color + pHash defaults, cache migration and pair evaluation](algorithm/SIMILARITY.md)

The package itself is an offline image-level baseline, separate from `edgeml/`
sighting aggregation and the Go scheduler's manifest/fidelity/chunk planning.
The sidecar's `tomer` adapter integrates its scoring and color+pHash similarity
into the live pipeline: run the sidecar with `--prioritizer tomer` or `both`
and the edge with `-prioritizer tomer`. The Go scheduler still selects forms
and chunks; it does not call the offline whole-JPEG selector. See the
[integration report](docs/reports/TOMER_INTEGRATION.md) for the streaming
differences. Full-image selection metrics remain distinct from unique-object
delivery metrics in `edgeml/`.

## Simulator results (satcom_contested, 60 min, mock analyzer)

| strategy | first unknown fact at shore | first unknown full | manifests at shore | unknowns at FACT | FULL images |
|---|---|---|---|---|---|
| FIFO | never | 14 s | 265 / 360 | 0 / 5 | 265 |
| prioritized | 3 s | 7 s | 360 / 360 | 5 / 5 | 208 |

FIFO wins raw image count because it spends zero bytes on manifests, facts,
thumbnails and crops; prioritized gives shore a complete picture of the backlog and
every unknown, and puts the bytes it has into what the operator asked for.

## Operator web app

The operator UI lives in `cmd/ghost-seeker` + `web/` (Go 1.26.10+, stdlib only, synthetic imagery and its own in-memory simulation by default; `-shore` runs it on a live shore instead, see [docs/DEMO.md](docs/DEMO.md)). It defaults to `:8080`, which the shore service also uses, so pick another port when running both:

```sh
go run ./cmd/ghost-seeker -addr :8090
```

The app imagery is AI-generated synthetic imagery; see [tools/imagegen](tools/imagegen/README.md).

Flags `-addr`, `-retain`, and `-backfill` set the listen address, item retention, and number of historical items generated at startup. Pages:

- **Imagery Feed** — browse prioritized sensor imagery, open an item for detections and processing details, and set its priority.
  Images the pipeline handled are no longer dropped before storage. Low-value frames and repeats of an earlier image are kept at **P4 Low**, and their cards say why. The feed still holds at most `-retain` images; when it is full, the oldest filtered images nobody touched are removed first. In live shore mode, frames of one sighting are still combined into a single card. **Show duplicates** (on by default) shows or hides the repeats; any repeat a person acted on stays visible either way. **Human review** selects all images, **Sent by a person** (newest send first), reviewed by a person, or not reviewed yet. Purple **➤ Sent by operator** marks a human send, and green **✓ Auto-sent to Commander** marks an automatic one. **Size** is Small or Large. These choices are saved in this browser and reset with Display settings. **Details: counts & filtered photos** opens the pipeline counts and lets you browse the filtered photos (each repeat is shown next to its original).
- **J2 briefing** — in an image's detail drawer, add individual vessel detections to the briefing selection (other object types are excluded). Choose **Create J2 brief** in the feed toolbar to freeze evidence, including locally available imagery with the selected detection highlighted. The integrated `#briefing` workspace provides editable title, analyst assessment and intelligence gaps; combined slides paginate at four observations, or use one slide per observation. Export editable 16:9 PowerPoint or **Print / Save PDF** (opens a printable page; choose Save as PDF in the browser). Export is local and needs no external service. Drafts live only in the current tab and are lost on reload. A new snapshot explicitly replaces evidence without changing review/commander state. Different observations may depict the same vessel: remove duplicates manually; there is no verified track identity, affiliation or geospatial data. Missing images get explicit placeholders. All slides retain the training marking.
- **Review** — `1`–`4` set priority, `Space` accept the model suggestion, `X` dismiss, `C` send to the commander queue, `→`/`S` skip, `Z` undo, `N` night mode.
  Repeated overrides can prompt a one-key directive suggestion; when contacts are close, Review can ask which object caused the override.
  Suggestions stay client-side until accepted, and accepted rules appear on the Commander's Board under “Learned from operator”.
- **Collection Priorities** — live iterative workbench: debounced previews, weighted/source/time-scoped editable chips, scored specificity suggestions, image corrections, tier pins, version comparison/revert, Ctrl+Z / Ctrl+Shift+Z. J/K or ←/→ move image focus; ↑/↓/X propose corrections. Apply publishes the draft and pins to all browser sessions. Active directives, the base-model selector, and effective object weights remain on the page.
- **Commander's Board** (`/cdr`) — separate final-review view for images the watch floor escalated (`C`), ordered by tier, command score, then wait time; shows the frame, contacts, escalation reason and chain of custody. Keys: `1` take action, `2` track, `3` forward to intel, `4` no further action, `B` return to the watch floor, `/` note, `Z` reopen, `↑`/`↓` move, `Tab` Awaiting/Decided. Decisions go to `POST /api/items/{id}/decision` and fan out over SSE. A simulated watch officer escalates the top unreviewed P1/P2 image about every 15 s; `-auto-cdr 0` turns it off. Escalated images survive the `-retain` limit.

Preview adds draft rules on top of active directives without mutating server state. Apply adds a new active directive; Reset clears only the local draft. Time-scoped directives are refreshed every 30 seconds so expired boosts disappear. The feed continues streaming during iteration. Live previews use the deterministic rules parser; operator app state is in memory and resets on restart.

### Natural-language compiler

Directives compile in `internal/command`. Set `GS_LLM_URL`, `GS_LLM_MODEL` and
`GS_LLM_TIMEOUT` (or pass `-llm-url`, `-llm-model` and `-llm-timeout`) to use an
optional local OpenAI-compatible LLM. The JSON-schema-constrained draft falls
back to the deterministic regex parser; the workbench shows a readback and
guardrails before applying.

For Ollama:

```sh
GS_LLM_URL=http://localhost:11434 GS_LLM_MODEL=qwen2.5:3b-instruct go run ./cmd/ghost-seeker -addr :8090
```

`llama-server --port 8002` and vLLM `--port 8002` can also expose the
OpenAI-compatible endpoint at `http://localhost:8002/v1`. Test an LLM-backed
compiler with `GS_LLM_URL=... go test ./internal/command -run TestEvalLLMPath -v`.

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
`-shore http://localhost:18080`. Its rate varies automatically every five seconds using a
normal distribution (center 8 Mbps, standard deviation about 4.86 Mbps), clipped to
0 kbps–50 Mbps. The spread puts about 5% of five-second intervals at zero, simulating
internet dropouts; this is a long-run target, not a fixed quota per minute.
The feed strip shows the current applied rate and the ship's measured
estimate; there are no manual speed buttons or rate-setting API. A zero sample drops
the link until the next nonzero sample. The former `-link-bps` flag is removed.
Pass `-link-listen ""` when the link is shaped elsewhere (compose +
Toxiproxy, `deploy/compose/degrade.sh 9k6|64k|256k|2m`). See `docs/DEMO.md` for the demo.

## Status / not done

- Inference: Go mock, model-free Python stub, and an implemented CPU sidecar
  with ONNX Runtime/OpenVINO detection, tracking, MobileCLIP classification,
  and sighting aggregation. Real models must be provisioned separately; see
  [the sidecar setup](edgeml/sidecar/README.md). Evaluation results are prototype
  measurements on small datasets, not field accuracy.
- NL directives compile in `internal/command`, optionally through a local
  OpenAI-compatible LLM (`GS_LLM_URL`, `GS_LLM_MODEL`, `GS_LLM_TIMEOUT`) with
  JSON-schema constraints, regex fallback, deterministic readback and
  guardrails before apply.
- Unit authentication is on by default, and operator authentication gates
  ConsoleService and blobs. Policy signing remains opt-in. No service provides
  TLS; see [Security](docs/SECURITY.md) for deployment requirements and residuals.
- Capture input is frame/scenario replay; no live camera ingestion is implemented.
- Shore chunk reassembly assumes in-order delivery within an item/form.
- Sailboat footage is in `frames/Sailboat_on_the_horizon_frames.zip` (unlabelled;
  the boat is not visible in every frame). `scenarios/sailboats_example.json`
  still uses a placeholder path for the sailboat set.
