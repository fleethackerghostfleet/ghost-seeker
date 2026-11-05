# Ghost Fleet demo runbook

The committed headless run demonstrates **5 of 6 labeled objects imaged at shore
at 9.6 kbps**, vs 0 of 6 naive and 1 of 6 with image-level dedup; the sightings
row reaches all 6 from 64 kbps up. It uses ground-truth object identities to
demonstrate delivery, not detector accuracy. `make demo` reproduces that
comparison. The live stack below uses the implemented detector/tracker sidecar,
so its observed contacts and delivery results can differ.

Background numbers (edgeml reports C §9 / E headline): tracking + sighting aggregation
→ ~0.2 duplicate reports per object vs ~64 naive / ~6.5 image-level dedup; at 72 KB/min
shore gets an image of 10/10 dev objects vs 2/10 naive.

## 0. One-time setup

```sh
# toolchain: Go 1.26.10 (GOTOOLCHAIN=local), Python >= 3.10, gcloud signed in as a lab student account
make build
make demo-clips        # -> scenarios/ghostfleet_demo/frames/<clip>/{img1,gt,seqinfo.ini}  (gitignored)
make demo-models       # -> ~/models/D/{yolo11n-640,mobileclip-s1} (D's ONNX exports, ~100 MB) + mobileclip-s1/unknown_calib.npz (H: dev-fitted unknownness calibration + kNN bank, auto-loaded by the sidecar)
python -m venv ~/edgeml-venv && . ~/edgeml-venv/bin/activate
pip install -e "edgeml[tracking]" onnxruntime --extra-index-url https://download.pytorch.org/whl/cpu   # boxmot pulls CPU torch, ~3 min
```

Clips (5 fps, MOT layout, from `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_5fps/`):
`Submarine_surfacing` (372 f, 2 GT objects), `Cruise_ship_Mediterranean_sunset_off_Italy`
(153 f, 3), `Spotted_A_Submarine` (745 f, 1). Change `CLIPS=` in `download.sh` / the
`sets` in `scenarios/ghostfleet_demo/scenario.json` to use others.

## 1. Headless comparison (`make demo`)

```sh
make demo              # = make demo-clips demo-compare
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
make demo-ui           # bin/ghost-seeker -addr :8090 -shore http://localhost:8080 -edge http://localhost:8081 -link-listen :18080
make demo-edge         # bin/edge    -data data/edge -listen :8081 -shore http://localhost:18080 -scenario scenarios/ghostfleet_demo/scenario.json -period 200ms -infer http://localhost:9090 -dedup sightings -unit-token-file data/edge.jwt
open http://localhost:8090
```

**Auth (no extra steps):** the three Go processes share one operator token, and the edge↔shore
link carries a unit JWT (`make demo-shore` writes a random HS256 key to `data/unit.key`;
`make demo-edge` mints a 24 h edge token from it into `data/edge.jwt` on every start). With no
`GS_OPERATOR_TOKEN` set, the first of them to start writes a random token to `data/operator.token`
(0600) and the others read it; a browser on `localhost` is signed in automatically (HttpOnly,
SameSite=Strict session cookie). From another machine the UI asks for the token at `/login`
(`cat data/operator.token`). For `curl` against the shore/edge API use
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

**Object-free frames.** In `-dedup sightings` mode the edge folds frames with no tracked object
into one **scene** item per source per `-scene-every` (default 60 s; `tags.event: "scene"`).
Only FACT+THUMB ship; the FULL frame stays on the edge and is pull-only. The UI does not show
them in the feed: they feed the **Cameras** strip above it (one entry per camera: latest view,
last seen, contact count, "Pull full frame" for the current view). They are not objects, so they
are excluded from the unique-object count, the review queue and the commander view (the strip
says "N camera heartbeats not counted"). Any per-frame leftovers
(`-scene-every 0`) are hidden by the "sightings only" toggle.

**Send full size.** The operator override in the image drawer asks for a name,
then calls `/api/items/{id}/full-res`. In live mode the bridge calls shore
`RequestPull` with `original: true`, so the edge sends the capture's original
bytes as FULL with no re-encoding.

## 3. Three-minute presenter script

| t | do | say |
|---|---|---|
| 0:00 | Stack already running, link preset **2 Mbps**, feed tab. | "Ship camera, 5 fps. Edge detects, tracks and aggregates into sightings; shore only ever sees what crosses this link." Point at the strip: items on edge vs shore are equal, every card has a crop/thumb. |
| 0:30 | Click **9.6 kbps**. | "Now a contested SATCOM trickle — 72 KB a minute." Watch the estimator adapt and the form chips advance. FACT and thumbnail precede larger images; actual delivery time depends on image sizes, backlog, and link overhead. FULL contains the complete capture. |
| 1:15 | Hover a card's chips; open the **Review queue**. | "Per object we get one card with its crop, the zero-shot subtype, and we know exactly which forms have landed; 'duplicate frames folded on the edge' in the strip is what we did *not* send. Naive delivery would have sent 64 near-identical frames per object and the queue would still be stuck on the first one." |
| 1:45 | Type **`ignore cargo ships`** → Set. | "Operator intent goes back over the same link as a policy." Cargo cards grey out (suppressed; matched on the sidecar subtype, the detector class or an operator override), submarines keep arriving. Policy text shows in the strip. Cards the edge flagged *unknown* are never suppressed (same rule as `edge/scoring`), so a low-confidence "Cargo Ship (Unknown)" stays — say so, it is the point of the unknown gate. |
| 2:15 | On a card whose FULL chip is still pending click **Pull full image**, or use **Pull full frame** in the Cameras strip. | "A pull gives this image priority." Ordinary contact FULL forms can drain automatically after the smaller forms; camera-overview FULL forms are pull-only. Watch pending → partial → received on the form chip. Use **Send full size** in the image drawer to request the original bytes. |
| 2:45 | Click **2 Mbps**; switch to **Review queue** / **Commander view**. | "Link back; the backlog drains in seconds. Same software, every link." The review queue lists every sighting (zero-shot subtype, unknownness, chips). Show `scenarios/ghostfleet_demo/out/compare.md` for the headless numbers. |

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
