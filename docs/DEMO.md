# Ghost Fleet demo runbook

What the audience should take away: **at 9.6 kbps the shore still gets an image of nearly
every object the ship sees** (5 of 6 in the committed headless run, vs 0 of 6 naive and 1 of 6
with image-level dedup; all 6 from 64 kbps up), because the edge sends one *sighting* per
object instead of every frame. The operator UI shows this live; `make demo` reproduces it headlessly.

Background numbers (edgeml reports C §9 / E headline): tracking + sighting aggregation
→ ~0.2 duplicate reports per object vs ~64 naive / ~6.5 image-level dedup; at 72 KB/min
shore gets an image of 10/10 dev objects vs 2/10 naive.

## 0. One-time setup

```sh
# toolchain: Go 1.23 (GOTOOLCHAIN=local), gcloud signed in as a lab student account
make build
make demo-clips        # -> scenarios/ghostfleet_demo/frames/<clip>/{img1,gt,seqinfo.ini}  (gitignored)
```

Clips (5 fps, MOT layout, from `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_5fps/`):
`Submarine_surfacing` (372 f, 2 GT objects), `Cruise_ship_Mediterranean_sunset_off_Italy`
(153 f, 3), `Spotted_A_Submarine` (745 f, 1). Change `CLIPS=` in `download.sh` / the
`sets` in `scenarios/ghostfleet_demo/scenario.json` to use others.

## 1. Headless comparison (`make demo`)

```sh
make demo              # = make demo-clips demo-compare
# or directly:
./bin/sim -scenario scenarios/ghostfleet_demo/scenario.json -dedup all \
          -bps 9600,64000,256000,2000000 -strategies prioritized -duration 6m -sample 1m \
          -out scenarios/ghostfleet_demo/out/compare.json -md scenarios/ghostfleet_demo/out/compare.md
```

`cmd/sim` runs edge + shore in one process on a virtual clock (`internal/clock`), replaying
the clips at 5 fps through a flat link at each preset. `-dedup`:

| mode | what the edge sends | source of "object" |
|---|---|---|
| `naive` | every frame (byte-identical frames still dropped by `edge/ingest`) | – |
| `hash` | frames whose dHash differs from the last 64 kept by > 10 bits (image-level dedup) | – |
| `sightings` | one item when an object appears + a refresh ≤ every `-sighting-update` (30 s) | **MOT ground truth** (`gt/gt.txt` + `qa/corrections.yaml` merges) — an oracle stand-in until F1's sidecar lands; then `make demo-edge` with `-infer http://sidecar` + `-dedup sightings` is the same scenario on a real tracker |

Metrics (`compare.json` / `compare.md`): unique GT objects with an image (thumb/crop/full)
on shore at each sample time, time until all objects have an image, bytes on air, and bytes
per delivered object. Items carry `features.tags.gt_tracks` so the metric is computed on the
shore store regardless of mode.

## 2. Live stack (three terminals + browser)

```sh
make demo-shore        # bin/shore   -data data/shore -listen :8080
make demo-ui           # bin/ghost-seeker -addr :8090 -shore http://localhost:8080 -edge http://localhost:8081 -link-listen :18080
make demo-edge         # bin/edge    -data data/edge -listen :8081 -shore http://localhost:18080 -scenario scenarios/ghostfleet_demo/scenario.json -period 200ms -infer mock
open http://localhost:8090
```

Once F1 has merged, start the sidecar and change only the edge line:
`bin/edge ... -infer http://localhost:9090 -dedup sightings` (F1's flags). Items then arrive
with `tags.sighting_id/subtype/event` and a CROP form; the UI groups them per object
automatically (one card, "n frames seen", subtype).

**Link control.** The web app hosts the ship→shore link itself: `internal/shaper` is a TCP
proxy with a token bucket on `:18080`; the edge's `-shore` points at it, so the 0 / 9.6 kbps
/ 64 kbps / 256 kbps / 2 Mbps buttons change the real bytes-on-wire rate (0 drops the
connection). `edge/link` has no console hook for its simulator, and this needs no Docker.
The compose stack keeps Toxiproxy: `deploy/compose/degrade.sh 9k6|64k|256k|2m` applies the
same ladder; in that setup run the UI with `-link-listen ""` and the strip shows the edge's
own link estimate instead.

**Status strip.** current kbps (shaper setting + edge's estimate), items on shore / on edge
(+ queued items and bytes on the edge), unique objects with an image on shore (by
`sighting_id`; items without one count as one object each), bytes through the link.

**Actions.** The directive box calls shore `SetPolicy` (free text is parsed:
"ignore cargo ships; prioritize submarines" → suppress `cargo ship`, boost `submarine`;
the edge picks the policy up on its next exchange). "Pull full image" calls shore
`RequestPull` for FORM_KIND_FULL. "Relabel…" calls `OverrideLabel`. Confirm/Dismiss/Escalate
are local to the browser (localStorage).

## 3. Three-minute presenter script

| t | do | say |
|---|---|---|
| 0:00 | Stack already running, link preset **2 Mbps**, feed tab. | "Ship camera, 5 fps. Edge detects, tracks and aggregates into sightings; shore only ever sees what crosses this link." Point at the strip: items on edge vs shore are equal, every card has a crop/thumb. |
| 0:30 | Click **9.6 kbps**. | "Now a contested SATCOM trickle — 72 KB a minute, less than one frame." The edge's estimator needs ~30–60 s to re-learn the link (watch "edge estimates" in the strip fall from Mbps to ~10 kbps); then the edge queue grows while *new objects still appear* as cards: fact first (text card), then the crop. Form chips go pending → received. |
| 1:15 | Hover a card's chips; open the **Review queue**. | "Per object we get one card, 'n frames seen', the subtype, and we know exactly which forms have landed. Naive delivery would have sent 64 near-identical frames per object and the queue would still be stuck on the first one." |
| 1:45 | Type **`ignore cargo ships`** → Set. | "Operator intent goes back over the same link as a policy." Cargo cards grey out (suppressed), submarines keep arriving. Policy text shows in the strip. |
| 2:15 | On a submarine card click **Pull full image**. | "Full frames only on request." Full chip goes pending → partial % → received; the card switches to the full-resolution image. |
| 2:45 | Click **2 Mbps**; switch to **Commander view**. | "Link back; the backlog drains in seconds. Same software, every link." Show `scenarios/ghostfleet_demo/out/compare.md` for the headless numbers. |

Reset between runs: `rm -rf data/ && ` restart the three processes (the browser's
confirm/dismiss state is in localStorage: DevTools → Application → Clear).
