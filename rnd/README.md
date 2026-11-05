# R&D 500-frame grading

Images (not in Git):
`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/rnd/testsets/rnd-500-v1/images/`

The committed manifest and predictions describe exactly 500 real frames from
seven `frames/*_frames.zip` clips and three
`scenarios/ghostfleet_demo/frames/*/img1` clips. Sources are sorted by name;
frames are naturally sorted by original path. Allocate 50 per source, then
redistribute any shortfall round-robin in source order. Within each clip take
evenly spaced indices including both endpoints (integer floor; singleton uses
the midpoint). A candidate whose SHA-256 was already selected anywhere in the
set is skipped in favor of the nearest byte-unique frame in the same clip.
Search by increasing index distance, preferring the later frame on equal
distance; fail explicitly if a clip cannot supply enough unique frames.
IDs follow this order; SHA-256 and dimensions identify the bytes.
Selection is deterministic, without a random seed. A new output directory is
required so interrupted runs cannot silently mix data.

In v1, Dolphins_Pungo_Ferry_Bridge has only 5 frames; its 45-frame shortfall
is shared equally by the other nine sources (55 each). The 185 images with
detections rank into 18 P1, 46 P2, 65 P3 and 56 P4 predictions. All 315
no-detection scene images are P4, giving 371 P4 overall. The real
`yolo11n-fp32-640-ort+mobileclip-s1-ort-fp32` run returned 219 detections,
no non-tentative subtypes, and zero unknownness on every independent first
frame. These are the actual API outputs, not filled-in maritime assumptions.

The refreshed set has 500 distinct SHA-256 hashes. The source ZIP itself
contains identical all-black JPEG bytes at Cargo_Ship_Uncut_View
`sec_0001.jpg` and `sec_9000.jpg`; endpoint sampling previously selected both.
The latter (rnd-0220) now selects the nearest unused `sec_8999.jpg` instead.
All 500 images were rerun through real inference after deduplication; source,
detection and tier counts above are unchanged. GCS images are checksum-synced
with stale objects removed within this testset's `images/` prefix.

## Algorithm

Each image is sent through the real sidecar Analyze API via `edge/inference`,
with a unique source per image **and run**, isolating tracker state. Predictions
use the response's frame detections, not accumulated sighting detections.
Boxes are normalized x/y/width/height. Subtype is included only when a matching
label's sighting supplies one other than `tentative`; absence is not a guessed
class. Unknownness is the actual Analyze response, including zero when the
first independent frame has not opened a sighting. This is an independent-frame
benchmark, not a replay of the live multi-frame sighting lifecycle.

Primary `model_score` (also `edge_priority_score`) is
`edge/scoring.Default()` for an image item with its Analyze features, no policy,
no operator interests/overrides, no pull boost, and age zero. We call the actual
shore bridge tier assignment with no-detection images marked as scenes. Scenes
are always P4 and excluded from the ranked population, regardless of score.
**Model tiers are rank-relative to the detected-image subset**, not absolute
per-image cutoffs: P1 top 10% (at least the first contact), P2 through 35%,
P3 through 70%, P4 remainder. The bridge uses a stable descending score sort:
exact ties among detected images retain manifest/input order, including across
tier boundaries. IDs are generated in manifest order; no random tie breaker
or secondary ID sort is applied. This follows live edgeml's
scoring/ranking, not the simulation catalog model, and the comparison unit here
is an image rather than a deduplicated live object. The live console has no
catalog mission model for primary edgeml scoring (its display model is `shore`).

Secondary `alt_tier_maritime` calls `ScoreDet → FrameScore → TierFor` with
the simulation default mission **maritime**, no directives/attributes, using
lowercase subtype when available, otherwise the detector label. Labels not in
the catalog retain that pipeline's zero default weight. This alternate tier is
not displayed as the model tier or used in grading deltas.

## Build / generate / upload

```sh
GOTOOLCHAIN=local go build -o bin/rnd-testset ./cmd/rnd-testset
GOTOOLCHAIN=local go build -o bin/rnd-grade ./cmd/rnd-grade
mkdir -p "$HOME/rnd-data"
./bin/rnd-testset -repo . -out "$HOME/rnd-data/rnd-500-v1" -infer http://127.0.0.1:9091
gcloud storage cp --recursive "$HOME/rnd-data/rnd-500-v1/images" \
  gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/rnd/testsets/rnd-500-v1/
```

The sidecar must be real, e.g. ONNX yolo11n-640 plus mobileclip-s1, with
`--prioritizer edgeml`. It does not need the separate image-priority backend.
Copy the two generated JSONL files into `rnd/testsets/rnd-500-v1/` to version
the metadata. To download elsewhere, create the local testset directory, copy
the two committed JSONL files there, then download the GCS `images/` directory
into it. Keep images outside Git.

To re-derive tiers from stored inference without calling the sidecar:

```sh
./bin/rnd-testset -rerank -out "$HOME/rnd-data/rnd-500-v1"
```

This atomically replaces only `predictions.jsonl`, preserving all fields except
`model_tier`. Copy it back to the versioned testset directory afterward.
Stop the grader first and archive existing journals before changing tiers.

## Grade

```sh
./bin/rnd-grade -testset "$HOME/rnd-data/rnd-500-v1" \
  -log-dir rnd/logs -reviewer operator -addr 127.0.0.1:8095
```

Open through a localhost tunnel; this lightweight tool has no login. Bind only
to loopback or a trusted development network.
All requests require a loopback Host (localhost, 127.0.0.1 or ::1), or a
hostname explicitly permitted by repeatable `-allow-host <hostname>` flags.
Rejected Hosts and accepted request Hosts are logged; forwarded-host headers
do not grant access. For a trusted preview proxy, allow its exact hostname.
Keys: 1–4 grade, Enter/Space accept, Z undo, left/right navigate.
A negative signed mean delta means the human
assigns higher priority than the model (the model under-prioritizes).

The displayed position is one-based; graded progress counts distinct effective
image grades. A regrade replaces that image's effective grade; undo appends an
event and restores the previous effective grade. Last-20 means the newest 20
**effective** grades in journal order. Empty counters are zero. The summary's
4×4 confusion matrix rows are model P1–P4, columns human P1–P4.

Logs are fsynced append-only JSONL, with reviewer/testset names sanitized and
hash-suffixed in filenames to avoid collisions. Restart with the same reviewer,
testset directory name and log directory to resume the latest matching session.
A process lock prevents concurrent writers to that session. Replayed records
are validated against predictions; summaries are regenerated on startup and
atomically replaced after every grade/undo. A torn final line is saved to a
`.partial-<timestamp>` file before recovery; complete corrupt lines fail closed.
Do not change metadata underneath an existing session.
At startup every local image is hashed and compared with the manifest SHA-256;
missing images or mismatches abort before a grading session can open.

Logs default to `rnd/logs/` (ignored by Git). Preserve the raw journal plus
`*-summary.json`; the latter is a derived view, not the source of truth.

Checks: `GOTOOLCHAIN=local go vet ./... && GOTOOLCHAIN=local go test ./...`.

## rnd-500-v2 — diverse set

Built with `rnd-testset -plan rnd/testsets/rnd-500-v2/plan.json -sources ~/rnd-data/sources -out ~/rnd-data/rnd-500-v2`.
The plan lists every source and its frame count; sources are the 42 ingested
video clips (5 frames each, 6 for the five night clips) plus five labeled public
datasets from the catalog. Catalog images are picked evenly over sorted
`image_id` (see `select_catalog.py` notes below), clip frames evenly through the
clip, byte-duplicates skipped as in v1. Images:
`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/rnd/testsets/rnd-500-v2/images/`.

| Source | Images | Tags |
|---|---:|---|
| 42 video clips | 215 | surface, rgb, unlabeled |
| SeaShips7000 | 75 | surface, rgb, labeled |
| Singapore Maritime | 75 | surface, rgb, labeled |
| SeaDronesSee | 50 | aerial, rgb, labeled |
| IR maritime vessels | 50 | surface, ir, labeled |
| SMD VIS onboard sample | 35 | surface, rgb, labeled |

Each manifest row carries `tags` (viewpoint, modality, labeled, lighting) and,
for labeled sources, `ground_truth` boxes in pixel x/y/w/h. The grader draws
them dashed yellow under the model's green detections.

Model output (same detector and live rank bands as v1):

| Has detections | Images | P1 | P2 | P3 | P4 |
|---|---:|---:|---:|---:|---:|
| Yes | 293 | 29 | 73 | 103 | 88 |
| No | 207 | 0 | 0 | 0 | 207 |

Detector vs. ground truth, per labeled source (images with at least one truth box):

| Source | GT-positive images | Detector found nothing | GT boxes | Detected boxes |
|---|---:|---:|---:|---:|
| seaships7000 | 75 | 19 | 98 | 66 |
| singapore_maritime | 75 | 3 | 4139 | 223 |
| seadronessee | 50 | 13 | 351 | 82 |
| ir_maritime_vessels | 44 | 20 | 169 | 44 |
| smd_vis_onboard_sample | 32 | 9 | 60 | 30 |

Overall the detector returned nothing on 64 of 276 GT-positive images. As in
v1, frames are scored independently, so no subtypes or unknownness appear.
Catalog candidates were selected and downloaded with a small script (evenly
spaced over sorted `image_id`, `count + 6` candidates per dataset to allow
duplicate skipping) into `~/rnd-data/sources/datasets/<dataset>/index.jsonl`.
