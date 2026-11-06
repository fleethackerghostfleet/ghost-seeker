# Ghost Fleet demo runbook

## Watch-floor whale-strike draft

Open an image's detail panel and choose **Draft OPREP-3 NAVY BLUE (whale strike)**.
The local-only dialog prefills capture time, source position (when available),
and the highest-confidence marine mammal detection. Edit the reporting fields,
including metadata-prefilled latitude/longitude, capture time, mammal subtype,
and sensor heading (not ship course). Metadata-derived fields are tagged and
remain editable. Sensor evidence includes hardware, frame/record, truncated
content hash and sighting track when present; synthetic capture metadata is
explicitly marked in the draft. If metadata cannot be fetched, embedded image
capture fields are used before falling back to unknown placeholders.
Review the live preview, then copy or download the draft as plain text.
Nothing is submitted or released, and machine detection does not confirm a strike.
Confirm category, addressees and timelines against OPNAVINST 3100.6K and current
fleet marine mammal strike guidance before release. Available to all console roles.

The committed headless run demonstrates **5 of 6 labeled objects imaged at shore
at 9.6 kbps**, vs 0 of 6 naive and 1 of 6 with image-level dedup; the sightings
row reaches all 6 from 64 kbps up. It uses ground-truth object identities to
demonstrate delivery, not detector accuracy. `make demo` reproduces that
comparison. The live stack below uses the implemented detector/tracker sidecar,
so its observed contacts and delivery results can differ.

Background numbers (edgeml reports C §9 / E headline): tracking + sighting aggregation
→ ~0.2 duplicate reports per object vs ~64 naive / ~6.5 image-level dedup; at 72 KB/min
shore gets an image of 10/10 dev objects vs 2/10 naive.

## Run it in Docker (Mac or Linux laptop, one command)

Prerequisites: **Docker Desktop** (Apple silicon or Intel; ~6 GB free for the images) and,
while the repo is private, the **GitHub CLI** signed in once (`brew install gh && gh auth login`)
so the fallback downloader and `make demo-models` can pull clips + models from the
[`demo-data-v1` release](https://github.com/fleethackerghostfleet/ghost-seeker/releases/tag/demo-data-v1)
(`deploy/demo-data.sh`). No Go needed.
Python 3 generates the live scenario; signed-in **gcloud** is the primary source
and checksum-refreshes both eval clips and all selected bucket frame sets on every run.
Only when gcloud is absent or unauthenticated do eval clips fall back to the release;
that mode warns that bucket discovery/refresh is unavailable. Custom buckets require gcloud.
The optional ocean watch comes from the same release (`demo-ocean.tar.gz`, 32 screened crops)
and is laid out by `ocean.py` with the system `python3` (no Pillow, no LFS).

```sh
make demo-clips demo-models   # GCS camera refresh + model provisioning; repeat to refresh
make compose-demo             # builds the four images (first time ~5-10 min), starts the stack
open http://localhost:8090    # sign in with the generated operator token; replay starts when the sidecar is healthy
make compose-demo-down        # stop + delete data and credentials -> next `make compose-demo` is a fresh replay
```

What runs (`deploy/compose/demo.compose.yml`, the same four processes as the native stack below):
`sidecar` (edgeml image with the tracking extra, CPU torch + boxmot, `--prioritizer both`),
`shore` (:8080), `ui` (Patrick's console on :8090 + the shaped link on :18080 — bandwidth
varies automatically), `edge` (replays `scenarios/ghostfleet_demo` at 5 fps through the shaper into the
sidecar, `-dedup sightings`). A one-shot `init` container writes the operator token, the HS256 unit
key and the edge's 24 h JWT into the `demo-secrets` volume, so there is nothing to configure; the
three Go containers run as the distroless `nonroot` user and bind to `127.0.0.1` on the host only.
`MODELS=/path/to/D make compose-demo` if the models live elsewhere; `make compose-demo-logs` tails
all four. Shore/edge consoles are also on `127.0.0.1:8080` / `:8081`.

Plain `make compose-demo` requires the real operator token at the UI login page;
it does **not** enable trusted-peer auto-login or arbitrary-token login. Retrieve
the generated token locally, then paste it into **Operator token** at
`http://localhost:8090/login` and click **Sign in**:

```sh
docker run --rm --entrypoint cat \
  -v ghost-seeker-demo_demo-secrets:/s:ro \
  ghost-seeker/demo-init:0.1.0 /s/operator.token
```

This is a credential: do not paste it into logs, screenshots or chat. It is stored
in the stack's named volume, not the native `data/operator.token` file.
`make compose-demo-down` deletes that volume; retrieve the new token after the
next fresh start. After login, the browser uses an HttpOnly, SameSite=Strict
session cookie. No relaxed override is required to view or operate the feed.

This is a laptop demo, not the fielding topology — `deploy/compose/docker-compose.yml` (Toxiproxy
link, explicit `GS_OPERATOR_TOKEN` / `GS_SIGNING_KEY` / `GS_UNIT_TOKEN`, production posture) is unchanged.

## Optional local-language research integration

`./research/nl-priority/demo.sh` adds the isolated IBM Granite compiler to the
demo. See [the integration runbook](../research/nl-priority/DEMO_INTEGRATION.md)
for supported classes, native startup, and validation limits.

## 0. One-time setup

### Optional relaxed local presentation override

Use the secure token login above for the normal Compose stack. For a deliberately
relaxed local presentation, explicitly add the override after preparing the data
and models. Keep the same model directory and generated scenario as the normal
stack:

```sh
GS_MODELS="$HOME/models/D" GS_DEMO_SCENARIO=scenario.generated.json \
  docker compose -f deploy/compose/demo.compose.yml \
  -f deploy/compose/demo.local.compose.yml up -d
```

This replaces the UI command only, reuses the same project/data/secrets, and
keeps published ports bound to `127.0.0.1`. It enables `-dev-trust-peers` for
local Docker browser auto-login, `-demo-link-control` for presenter link controls,
and `-demo-accept-any-token` as an insecure login fallback. Only in this mode
does any non-empty login token grant **operator** access. Empty tokens are rejected;
PIV plus arbitrary-token login is rejected at startup. Never use this override
in production or on an untrusted network. Real operator credentials are still
required between the services; their bearer authentication is not relaxed.

Run `make compose-demo` again to restore the base UI command. Previously issued
browser sessions do not prove auto-login is enabled: use a fresh/private browser
session when checking the secure login screen. The override is not used by
`make demo-ui`; do not assume a native launch accepts arbitrary tokens.

```sh
# toolchain: Go 1.26.10, Python >= 3.10, GitHub CLI signed in (gh auth login) for the demo data
make build
python -m venv ~/edgeml-venv && . ~/edgeml-venv/bin/activate
pip install -e "edgeml[tracking]" onnxruntime --extra-index-url https://download.pytorch.org/whl/cpu   # boxmot pulls CPU torch, ~3 min
make demo-clips        # refresh eval clips + discovered GCS sets (all gitignored)
# INCLUDE_OCEAN_SCENES=1 make demo-clips   # opt into curated ocean scene tiles
make demo-models       # -> ~/models/D/{yolo11n-640,mobileclip-s1} (D's ONNX exports + H's unknown_calib.npz)
```

Clips (5 fps, MOT layout, from `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_5fps/`):
`Submarine_surfacing` (372 f, 2 GT objects), `Cruise_ship_Mediterranean_sunset_off_Italy`
(153 f, 3), `Spotted_A_Submarine` (745 f, 1). Change `CLIPS=` in `download.sh` / the
the generator filters to use others in the live demo.
`scenario.json` remains the unchanged three-ship input to the headless comparison.

`make demo-clips` prepares the eval clips and GCS frame sets. Ocean is off by default;
`INCLUDE_OCEAN_SCENES=1 make demo-clips` prepares it and folds its scene entries into
the generated scenario. Neither launch target changes the manifest or prepares ocean implicitly.
`demo-ocean` fetches the released crops (verified against `ocean-manifest.json`); only
without them does it regenerate from the LFS ZIP, which needs `git lfs pull
--include='frames/30_Days_at_Sea_Timelapse_frames.zip'` and Pillow 12.3.0. Headless `make demo` fetches
the three ship clips via `demo-ship-clips`, and `demo-compare` uses those clips directly.
`demo-ocean` runs `ocean.py`: 32 manually inspected, real-sidecar-screened
688×516 side-region crops of the existing `30_Days_at_Sea_Timelapse_frames.zip`.
Clear, overcast, glare and dusk views contain no external contacts or camera-ship
structure. `ocean-manifest.json` records source ZIP/GCS generation, crop coordinates,
source/output SHA-256 and licence/usage provenance. Only crops returning **zero
detections and zero sightings** were selected; ingestion still runs real inference
and never bypasses detections. Crops are generated deterministically (Pillow 12.3.0,
fixed JPEG parameters; checksum mismatch fails closed), not bundled as new imagery.
The 32 crops cycle through 420 MOT-style frames, names 000001–001258, proportionally
spread over the entire replay (one ocean input per ~3 ship inputs). The four-set live
replay has 1690 inputs at 200 ms (~338 s before processing); ship clips and their
relative interleaving are unchanged. The released three-clip archive is unchanged.

`CLIPS` (space-separated) and `BUCKET` override the eval clips and their GCS prefix.
Authenticated eval clips always checksum-sync, including existing cached clips.

`make demo-clips` also discovers every `frames/*_frames/` prefix in that bucket
and syncs the flat JPEG directories into `frames/bucket/`. Every run checks
content checksums and updates changed/new files (and removes deleted local files
within each synced set). No bucket data is modified. The resulting on-disk image
sets are written to gitignored `scenario.generated.json`, preserving the other
fields of `scenario.json`; optional scene entries retain their cadence from `live.json`.
`make compose-demo` and `make demo-edge` select this generated scenario, falling
back to committed `scenario.json` when it does not exist.
Restart the edge after another sync to load newly added sets. For direct Compose,
set `GS_DEMO_SCENARIO=scenario.generated.json`; without it `scenario.json` is used.
The headless `make demo` comparison fetches only the eval clips (no bucket frame
discovery/sync) and keeps the committed three-clip scenario and its GT data.

The visual simulator and live shaper use the demo's automatic normal rate model.
`-sim-link-bps=N` sets a nominal simulated upload rate (varies within ±15%, resampled every 5 s); legacy `-link-bps=N` still pins the live
shaper and aliases the simulator override when `-sim-link-bps` is not set to a
nominal rate. Zero represents an outage; `-1` selects the automatic model.

Optional environment controls (apply when running `make demo-clips`):
- `INCLUDE_BUCKET_FRAMES=0`: eval clips only.
- `INCLUDE_OCEAN_SCENES=1`: opt into screened ocean scenes (use with `make demo-clips`).
- `FRAME_SETS_INCLUDE='*sailboat*,*SMD*'` / `FRAME_SETS_EXCLUDE='*test*'`:
  comma-separated, case-sensitive glob filters on full directory names (including
  `_frames`). Excluded cached sets are not added to the generated scenario.
- `FRAMES_BUCKET=gs://another-bucket`: override the bucket root for discovery.
- `DEST`: override the local frames directory; keep it inside the mounted
  `scenarios/ghostfleet_demo/` directory for Docker replay.

## 1. Headless comparison (`make demo`)

```sh
make demo              # = make demo-ship-clips demo-compare (no ocean ZIP or Pillow needed)
# or directly, for the three modes that do not need a sidecar:
./bin/sim -scenario scenarios/ghostfleet_demo/scenario.json -dedup naive,hash,sightings \
          -bps 9600,64000,256000,2000000 -strategies prioritized -duration 6m -sample 1m \
          -out scenarios/ghostfleet_demo/out/compare.json -md scenarios/ghostfleet_demo/out/compare.md
```

`cmd/sim` runs edge + shore in one process on a virtual clock (`internal/clock`), replaying
the clips at 5 fps through a flat link at each preset. `-dedup`:

| mode | what the edge sends | source of "object" |
|---|---|---|
| `naive` | every frame (byte-identical frames still dropped by `edge/ingest`) | – |
| `hash` | frames whose dHash differs from the last 64 kept by > 10 bits (image-level dedup) | – |
| `sightings` | one item when an object appears + a refresh ≤ every `-sighting-update` (30 s) | **MOT ground truth** (`gt/gt.txt` + `qa/corrections.yaml` merges) — an oracle for this simulator comparison. `make demo-edge` instead uses the real sidecar with `-dedup sightings`. |
| `tomer` | Tomer's `algorithm/ghost_priority` image-level ranking through the sidecar (`--prioritizer tomer|both`, started by `compare.sh` on :9093): priority = his whole-frame score, color+pHash near-duplicates folded like `hash` ([report](reports/TOMER_INTEGRATION.md)) | – |

The simulator's `hash` row uses perceptual dHash; the live edge's `-dedup hash`
uses exact-content hashing (plus the sidecar's near-duplicate flag in `tomer`
mode). To include `tomer` in a direct simulator run, use `-dedup all -infer <URL>`
with a running sidecar in `--prioritizer tomer` or `both` mode. The comparison
script starts that sidecar automatically when its Python dependencies are available.

Metrics (`compare.json` / `compare.md`): unique GT objects with an image (thumb/crop/full)
on shore at each sample time, time until all objects have an image, bytes on air, and bytes
per delivered object. Items carry `features.tags.gt_tracks` so the metric is computed on the
shore store regardless of mode.

## 2. Live stack (four terminals + browser)

```sh
make demo-sidecar      # python -m edgeml.sidecar --listen :9090 --detector ~/models/D/yolo11n-640/model.onnx --embed ~/models/D/mobileclip-s1 --threads 4 --fps 5
make demo-shore        # bin/shore   -data data/shore -listen :8080 -signing-key-file data/unit.key
make demo-ui           # bin/ghost-seeker -addr 127.0.0.1:8090 -shore http://localhost:8080 -edge http://localhost:8081 -link-listen :18080
make demo-edge         # replays scenario.generated.json, or scenario.json if nothing has been generated
open http://localhost:8090
```

**Native authentication:** the three Go processes share one operator token, and the edge↔shore
link carries a unit JWT (`make demo-shore` writes a random HS256 key to `data/unit.key`;
`make demo-edge` mints a 24 h edge token from it into `data/edge.jwt` on every start). With no
`GS_OPERATOR_TOKEN` set, the first of them to start writes a random token to `data/operator.token`
(0600) and the others read it. `make demo-ui` binds to `127.0.0.1` and does **not**
enable arbitrary-token login or trusted peers. To sign in when prompted, run
`make -s demo-token` (or read `data/operator.token`), paste that credential into
the token field at `http://localhost:8090/login`, and click **Sign in**. Keep it
private. Do not use the Docker volume token for this separate native stack.

The existing native development-token mode can auto-authenticate a direct,
non-forwarded loopback browser request. That exception is not guaranteed through
a proxy or Docker bridge, and is disabled when an explicit `GS_OPERATOR_TOKEN`
or `GS_OPERATOR_TOKEN_FILE` is configured; then use that configured credential.
Do not depend on automatic sign-in for a presentation. Browser sessions use an
HttpOnly, SameSite=Strict cookie after authentication.

For `curl` against the native shore/edge API use
`-H "Authorization: Bearer $(make -s demo-token)"`. Details: `docs/SECURITY.md`.

Start the edge last (it starts replaying immediately) and give the sidecar ~10 s to load the
models (log line `sidecar listening on :9090`). Without the sidecar: `make demo-edge INFER=mock
DEDUP=hash` (one item per distinct frame, no sightings). `make demo-edge PRIORITIZER=tomer DEDUP=hash` (sidecar in `--prioritizer both`, the `make demo-sidecar` default) ranks the queue by Tomer's image-level score instead; the strip shows `prioritization: tomer`.

With the sidecar, items arrive with `tags.sighting_id`, `subtype` (zero-shot
MobileCLIP: cargo ship / submarine / marine mammal …), and `event` (open / update).
A CROP form is included when eligible: unknowns skip CROP by default, and a crop
no smaller than FULL is omitted. FULL is the complete capture, not the key crop;
the UI shows one card per sighting with the crop, and the strip's "N duplicate frames folded on
the edge" is `GetStatus.items_deduped`. When a sighting closes, the edge sends a ~200 B contact
report (RECORD item); the card then shows "n frames seen" from it.

**Object-free frames.** In `-dedup sightings` mode, only responses with neither detections
nor sightings produce **scene** items (`tags.event: "scene"`). Detections without a tracked
object are not empty-water scenes. Scene cadence is per source: global `-scene-every`
stays at 60 s for ship cameras; the live-only `Open_ocean_watch` set overrides it with
`"scene_every_s": 4`. An omitted override inherits the global setting; 0 disables scene
handling for that source (ordinary hash/new-item behavior), negative intervals are rejected.
FACT+THUMB ship only after contact traffic; FULL stays on the edge, pull-only. Scenes
automatically score below contacts, including suppressed contacts, and directives do not
boost them. An explicit operator pull overrides that background priority using the normal
pull boost, so requested scene FULLs can precede pending contact forms. The Imagery Feed includes **P4 Low / Empty water · no contacts**
scene cards, interleaved with other cameras within the same tier. They carry empty
detection arrays and do not change contact scoring, normalization, object counts, review,
Commander, escalation or alerts. No contact actions appear on their drawer. The **Cameras**
strip remains unchanged (latest view, last seen, contact count, "Pull full frame").

**Send full size.** The operator override in the image drawer asks for a name,
then calls `/api/items/{id}/full-res`. In live mode the bridge calls shore
`RequestPull` with `original: true`, so the edge sends the capture's original
bytes as FULL with no re-encoding.

## Transmit queue: what is sent next

Bandwidth is simulated, not set by the operator: every 5 s a new rate is drawn from a normal
distribution (mean 0.5 MB/s, clipped to 0–2 MB/s, ~5% of draws are 0 = link outage). The
operator console shows that one rate in the connection panel. The rate is the only thing that
decides *how fast* bytes leave the ship (`go test -run 'Throughput|GovernsSendTime' -v
./internal/shaper ./cmd/ghost-seeker` measures it). The queue decides *what* goes next.

**Simulation console (`go run ./cmd/ghost-seeker`, `internal/store/upload.go`).** On every tick
the link budget (rate × elapsed time) is spent top-down on one ordered queue:

1. Images the operator marked **Upload next**, in the order they were marked.
2. Everything else by priority tier, P1 Critical → P4 Low.
3. Within a tier, by shore score, highest first.

Images marked **Hold upload** and images already on shore are skipped. The first image in the
queue gets the whole budget until it is done; leftover bytes in the same tick go to the next
one. A new higher-priority image pre-empts a partly sent one, which keeps its progress and
resumes later. During an outage nothing moves and partly sent cards say "Link outage, paused".
A card being sent has a blue ↑ badge in its top-right corner; a sent card has a green ✓ there.

**Live stack (`edge/scheduler`).** The edge rebuilds its plan before every exchange: manifest
entries (FACT summaries) for new items first, then payload forms ranked by priority × ladder
weight ÷ remaining bytes, keeping each item's FACT → THUMB → CROP → FULL order. Operator pulls
multiply an item's score by 100, so it goes next. Items below the image threshold or suppressed
by a directive only send FACT until nothing better is waiting. The edge sizes each exchange from its own measured link
estimate, which is internal to the scheduler; the operator only sees the shaped link rate.

## Demo talking points

- **Open integration:** It's an open system with an API, so any third-party system can pull the data (with authorized access).
- **Adaptability during war:** Present the boss's mid-demo requests as proof of "adaptability during war": change collection priorities, review a contact, then show the updated report at command without rebuilding the system.
- **Future work:** Granular audit logging of operator actions is a placeholder for later work; do not claim a complete operator-action audit trail exists today.

### Empty water and shore receipts

The standalone operator demo (`go run ./cmd/ghost-seeker -addr :8090`) includes
two native-resolution, manually inspected empty-water frames from the team's
`frames/Cruise_Ship_POV_frames/` GCS dataset. They show water, shoreline and the
camera ship's wake, with no external contacts. No detections are invented for
these frames. They appear under **BY-12** as **P4 Low / No contacts · empty water**
and remain on ship, even when bandwidth is available. Interesting contacts use
the link instead. An explicit **Upload next**, manual priority override,
**Send to Commander**, or **Send full size** can release one for demonstration.
The LFS archive `web/static/imagery/empty-ocean.zip` contains the original bytes
and `provenance.json` with source, internal-use license restriction, GCS object
generations and SHA-256 checksums. Run `git lfs pull` before building.

Open **Commander View → Received** beside the ship feed: only completed demo
uploads appear, independently of whether the operator has sent a command report.
**Awaiting decision** contains received, escalated reports; **Decided** preserves
the command outcome and note. Walk through sensor capture → ship scoring → shore
receipt → operator report → command decision in the right-hand chain of custody.
The Received count is the current retained picture, not a lifetime delivery total.

In the live stack, Received reflects actual shore form states: a received summary
can appear before an image, and Summary / Preview / Object close-up / Full image
are listed separately. Pending or partial-only items do not count as received.
The live object-free camera heartbeat behavior described above is unchanged;
the empty-water backlog example is the standalone simulation, not a change to
the sidecar or live edge scheduler. Commander View is a UI projection of the
console feed, not a separate authorization boundary or a new audit log.
## 3. Three-minute presenter script

| t | do | say |
|---|---|---|
| 0:00 | Stack already running, feed tab. Point to the automatic connection rate. | "Ship camera, 5 fps. Edge detects, tracks and aggregates into sightings; shore only ever sees what crosses this link." The connection panel shows one number: the current ship-to-shore bandwidth in MB/s. |
| 0:30 | Watch the rate change without touching any controls. | "Bandwidth is sampled every five seconds from a normal distribution centered on 0.5 MB/s, with a 0.30 MB/s standard deviation, clipped to 0–2 MB/s. About 5% of intervals drop the link." Watch the card being sent (blue ↑ badge and progress bar) and the green ✓ appear on each image once it is on shore. FACT and thumbnail precede larger images; actual delivery time depends on image sizes, backlog, and link overhead. FULL contains the complete capture. |
| 1:15 | Hover a card's chips; open the **Review queue**. | "Per object we get one card with its crop, the zero-shot subtype, and we know exactly which forms have landed; 'duplicate frames folded on the edge' in the strip is what we did *not* send. Naive delivery would have sent 64 near-identical frames per object and the queue would still be stuck on the first one." |
| 1:45 | Type **`ignore cargo ships`** → Set. | "Operator intent goes back over the same link as a policy." Cargo cards grey out (suppressed; matched on the sidecar subtype, the detector class or an operator override), submarines keep arriving. Policy text shows in the strip. Cards the edge flagged *unknown* are never suppressed (same rule as `edge/scoring`), so a low-confidence "Cargo Ship (Unknown)" stays — say so, it is the point of the unknown gate. |
| 2:15 | On a card whose FULL chip is still pending click **Pull full image**, or use **Pull full frame** in the Cameras strip. | "A pull gives this image priority." Ordinary contact FULL forms can drain automatically after the smaller forms; camera-overview FULL forms are pull-only. Watch pending → partial → received on the form chip. Use **Send full size** in the image drawer to request the original bytes. |
| 2:45 | Switch to **Review queue** / **Commander view**. | "The scheduler keeps adapting as the link varies automatically." The review queue lists every sighting (zero-shot subtype, unknownness, chips). Show `scenarios/ghostfleet_demo/out/compare.md` for the separate fixed-rate headless numbers. |

Historical dry run (2026-11-04, real sidecar, 8-core laptop-class VM; not rerun here with the current full-frame/form defaults): the three clips replay **interleaved** (same
frame names, so `edge/ingest` sorts them together) and the sidecar tracks them as three sources; the
whole replay is 1270 frames = 254 s, so start the script within a minute of `make demo-edge`. Over
the run the sidecar reported 20+ sightings (subtypes: submarine, marine mammal, cargo ship, tanker,
buoy, coast guard) and folded ~650 duplicate frames; every sighting had a crop on shore within one
exchange at 9.6 kbps once the estimator had settled. Right after a drop the estimator oscillates once
(a successful small exchange bumps it back toward half the last good rate, the next exchange fails,
it quarters again) — expect one more "re-learning: 1 failed exchange" ~30 s after the first recovery.

Reset between runs: `rm -rf data/ && ` restart the three processes (the browser's
confirm/dismiss state is in localStorage: DevTools → Application → Clear).
