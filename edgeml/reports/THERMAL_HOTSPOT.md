# Thermal hotspot fallback: development record

Companion: [new-contact MOT birth evaluation](NEW_CONTACT.md) (RGB tracking
sequences, no IR tuning or evaluation in that step).

## Data boundary

No `ir_maritime_vessels` images, annotations, or splits were used for threshold
selection, synthetic generation, or tuning. Real IR is used **only** for evaluation
of frozen defaults (gate inspection in Step A; held-out evaluation in Step B below). Its unknown license still prohibits shipping
models trained on it. This proposer has no learned weights.

Development inputs:

* `tools/synth_thermal.py`, seed **1729**, 120 images at 480x320: 84 contacts,
  36 empty frames. Sky/sea gradients, random horizon height and slight tilt,
  Gaussian clutter, sinusoidal wave texture, horizontal glint streaks; alternating
  white/black-hot ellipses and ship silhouettes with 3–120 px nominal width.
* `clean/v3/coverage-20261105/catalog` SMD onboard **dev only**: 40 images,
  30 object boxes, including all 14 labeled negative images. Images are converted
  to grayscale before proposing. Neither SMD test nor any IR split was tuned on.

Local generated data: `~/data/synth_thermal_shapes_dev`; original rectangle-only
prototype: `~/data/synth_thermal_dev`. Both use the same seed. The rectangle
prototype informed replacing a wide horizontal blur (opposite-polarity side
lobes) with robust row medians, rejecting <3 px component dimensions, and
narrowing aspect ratio to 0.2–8. The final sweep below uses the shipped generator.

Reproduce generation from the repository root:

```sh
.venv/bin/python edgeml/tools/synth_thermal.py --seed 1729 --count 120 \
  --output ~/data/synth_thermal_shapes_dev
```

Output directories must not already exist. No real images are read by this script.

## Threshold selection

Candidates: `k={4,6,8}`, `min_delta={24,32,40}`; fixed min area 12 work pixels,
max component area 4% of work frame, minimum dimension 3, aspect 0.2–8.
Match criterion: proposal/GT IoU >0.3. Unmatched proposals count as false
proposals; this is a proposal-level measure, not tracking precision.

| k | delta | Synthetic matched /84 | Synthetic false proposals | Empty synth frames with proposals /36 | SMD negative proposals /14 frames |
|---|---|---|---|---|---|
| 4 | 24 | 79 | 16 | 4 | 15 |
| 4 | 32 | 78 | 35 | 4 | 11 |
| 4 | 40 | 78 | 18 | 2 | 5 |
| 6 | 24 | 79 | 8 | 1 | 4 |
| 6 | 32 | 78 | 13 | 2 | 2 |
| 6 | 40 | 78 | 13 | 1 | 4 |
| 8 | 24 | 80 | 6 | 0 | 5 |
| 8 | 32 | 79 | 14 | 2 | 2 |
| **8** | **40** | **79** | **8** | **1** | **1** |

Chose 8/40 to prioritize real grayscale-negative FP control while retaining
79/84 synthetic matches. This is deliberately conservative: very small or
low-contrast contacts can be missed. On all 40 grayscale SMD dev frames:
14 proposals, 4/30 GT boxes matched, 10 unmatched proposals; one proposal on
one of the 14 empty frames. This is not a general-purpose RGB detector.

## Frozen defaults and behavior

* Work width 480 (never upsample); robust row background and row MAD sigma;
  3x3 residual smoothing, threshold `max(8 * 1.4826 * MAD, 40)` on 8-bit intensity.
* Gray-friendly horizon uses the strongest median vertical gradient across
  columns, requiring strength >=2 and >=6x median row strength. Unlike the
  unconditional band estimator in `tiled.py`, it can decline a horizon and
  fall back to the whole frame. Search starts 4% frame height above a reliable
  horizon to retain straddling hulls. No shared temporal horizon state.
* Both residual signs in auto mode; optional `white`/`black`. 2x2 opening,
  connected components and the above size/aspect filters; at most eight
  proposals. Contrast-based scores in [0.3,0.9], label `thermal contact`.
* Default fusion `when_empty`, cover threshold .25. `uncovered` uses anomaly
  fusion's padded center/IoU coverage suppression. Non-grayscale frames pay
  only the cheap gate in addition to the original detector.
* Engine wraps after optional anomaly fusion, enabled by default. Explicit
  `thermal_fallback=None` or `False` disables; dictionary config accepts
  proposer options, fusion options, and `unknownness_floor` (default .9).
* CLI: `--thermal-fallback '{"mode":"uncovered","unknownness_floor":0.95}'`;
  `--thermal-fallback false` or `null` disables; bare flag uses defaults.
* Detection label passes into track label, then aggregator label votes. The
  zero-shot adapter restores the thermal label when thermal votes dominate,
  and floors unknownness **after** classification (also without an embedder).
  Open/update/close and duplicate reports retain that label/floor. Frame
  unknownness includes thermal duplicate reports. Nonthermal duplicate wire
  fields remain unchanged. Subtype can still hold the zero-shot hypothesis;
  it cannot replace the thermal keyDetection label.

## Modality evaluation (not tuning)

Gate frozen before real IR inspection: downsample to 64 px width; sum of mean
absolute B-G and G-R differences <=6 and mean HSV saturation <=18. Single
channel inputs pass directly. This identifies grayscale appearance, not sensor
physics: desaturated RGB can pass; false-color thermal can fail.

| Evaluation input | Gate positives | Rate |
|---|---|---|
| All clean v3 IR images (all splits pooled) | 8,402 / 8,402 | 100% |
| All clean v3 SMD RGB sample images | 0 / 80 | 0% |
| Ghost Fleet 5fps RGB, sorted local paths stride 20 | 4 / 315 | 1.27% |
| FVessel RGB, sorted local paths stride 20 | 0 / 130 | 0% |

RGB MOT inputs are under `~/data/clean_v3/edgeml/eval/`; IR and SMD paths follow
catalog GCS URIs relative to `~/data/clean_v3`. RGB rates were reported as-is,
not used to adjust the gate after observing IR. The Step A tests use model-free
detector/tracker fakes with the real sighting aggregator. Step B below adds real
ONNX inference and real BoxMOT engine latency, but does not claim IR tracking accuracy.

## Existing suite failures

Clean detached `origin/dev` worktree at `42834c5` reproduces:

* `test_rpc_http_override_and_unexpected_engine_exception`: expected
  `RuntimeError: backend offline`, actual `inference request failed`.
* `test_body_limits_reject_without_waiting_for_the_declared_body[-1]`:
  expected HTTP 413, actual 400.

Neither protocol behavior is changed in this work.

Final full-suite command: `.venv/bin/python -m pytest edgeml/tests -q`

```text
FAILED edgeml/tests/test_sidecar_protocol.py::test_rpc_http_override_and_unexpected_engine_exception
FAILED edgeml/tests/test_sidecar_protocol.py::test_body_limits_reject_without_waiting_for_the_declared_body[-1]
2 failed, 606 passed, 3 skipped in 7.14s
```

All new thermal tests pass within this run, including no-embedder, low zero-shot
unknownness and higher-than-floor zero-shot unknownness on duplicate reports.

## Step B: frozen real-IR evaluation — before cover-threshold fix

**Historical results below: every “Live reported” row in this Step B section
means “before cover-threshold fix”.** Original results and crops are retained as
`THERMAL_HOTSPOT_assets/results_before_cover_threshold_fix.json` and
`crops_before_cover_threshold_fix.json`. The B2 section below supersedes actual
live behavior; original cold-source engine latency here is historical only.

**Evaluation only; no thresholds or production code changed.** Primary held-out
test: **1,081 images / 4,307 GT boxes / 39 recording groups**. Secondary dev:
**457 images / 893 boxes / 25 groups**, also evaluation-only, **not tuning**.
The evaluator filters to `eval_split in {test,dev}` before opening any images;
it never evaluates IR train. This does not retroactively change the Step A
all-split gate inspection recorded above. There are no fully empty IR frames.

### Matching and configurations

Primary **center-hit**: proposal center inside GT, **or** GT center inside proposal
(inclusive edges). Secondary **IoU >=0.3**; reference **IoU >=0.5**. Independently
for each criterion, predictions are processed descending by score, each matches
at most one unused eligible GT; greatest IoU wins, then GT index. Score ties retain
prediction order. No class correctness is required for the class-agnostic contact
match. All GT vessel boxes are included. P=TP/(TP+FP), R=TP/(TP+FN),
F1=2TP/(2TP+FP+FN); empty denominators produce zero.

* **Hotspot**: frozen `ThermalHotspotProposer()` on every frame, all scores.
* **YOLO boat**: shipped `~/models/D/yolo11n-640/model.onnx`, CPU ONNX Runtime,
  score >=0.35, COCO `boat` only. The sidecar does not remap COCO birds/people
  to boats: `runtime/detectors.py` retains original labels. `boat` is the only
  shipped COCO name in its `VESSEL_LABELS` set.
* **YOLO maritime**: actual sidecar keep-set: boat, airplane, bird, person,
  surfboard, kite; score >=0.35. Included to make the fusion comparison faithful.
* **Fusion @.35**: requested `ThermalFusionDetector(YOLO, when_empty)` with that
  maritime YOLO >=0.35 input, frozen cover threshold .25, all proposal scores.
* **Live reported**: exact engine detector-stage configuration: YOLO candidates
  >=0.05, fusion cover threshold .25, then report detections >=0.35. Consequently
  a YOLO candidate in [.25,.35) can block fallback even when no YOLO box is
  reported, and thermal scores in [.30,.35) are not reported. This existing
  integration behavior is measured explicitly, not silently changed. This row
  measures frame detections, not track/sighting accuracy or unknownness quality.

YOLO is cached **once per image/crop at .05**, then reused by all configurations;
postfiltering at .35 gives the higher-threshold baseline (score-ordered NMS).
Cache identity includes model hash, image-catalog hash, thresholds, keep-set,
threads and seed. Engine latency necessarily executes its own real inference,
separately from the metric cache. No MobileCLIP or new learned model is used.

### Overall results

P/R/F1 below are **percent**; proposals/frame counts include unmatched predictions.
Dev is a second evaluation-only result, never a selection/tuning set.

| Split | Configuration | Center P/R/F1 | IoU .3 P/R/F1 | IoU .5 P/R/F1 | Proposals/frame |
|---|---|---|---|---|---:|
| test | Hotspot | 27.62 / 13.63 / 18.25 | 7.06 / 3.48 / 4.66 | 1.51 / 0.74 / 1.00 | 1.966 |
| test | YOLO boat | 95.14 / 21.38 / 34.92 | 92.67 / 20.83 / 34.01 | 90.08 / 20.25 / 33.06 | 0.895 |
| test | YOLO maritime | 93.06 / 22.10 / 35.72 | 89.74 / 21.31 / 34.45 | 86.90 / 20.64 / 33.36 | 0.946 |
| test | Fusion @.35 | 64.25 / 24.45 / 35.42 | 57.11 / 21.73 / 31.48 | 54.42 / 20.71 / 30.00 | 1.516 |
| test | Live reported | 81.62 / 22.89 / 35.76 | 76.74 / 21.52 / 33.62 | 73.68 / 20.66 / 32.28 | 1.117 |
| dev (eval only) | Hotspot | 17.89 / 24.86 / 20.81 | 0.40 / 0.56 / 0.47 | 0.16 / 0.22 / 0.19 | 2.716 |
| dev (eval only) | YOLO boat | 86.81 / 22.84 / 36.17 | 80.00 / 21.05 / 33.33 | 68.94 / 18.14 / 28.72 | 0.514 |
| dev (eval only) | YOLO maritime | 85.66 / 23.40 / 36.76 | 77.87 / 21.28 / 33.42 | 66.80 / 18.25 / 28.67 | 0.534 |
| dev (eval only) | Fusion @.35 | 36.78 / 35.83 / 36.30 | 22.07 / 21.50 / 21.78 | 18.85 / 18.37 / 18.60 | 1.904 |
| dev (eval only) | Live reported | 56.75 / 30.12 / 39.36 | 40.08 / 21.28 / 27.80 | 34.39 / 18.25 / 23.85 | 1.037 |

**Interpretation:** on test, fusion @.35 adds 101 center-hit GT matches, but only
18 IoU .3 matches and 3 IoU .5 matches versus maritime YOLO. Hotspots often cover
small bright/dark subregions rather than full annotated hulls. The real live
detector configuration adds only 34 center hits. Center-hit gains must not be
presented as accurate vessel boxes: frozen proposer precision/localization and
small-contact recall remain weak. These are evaluation findings, not permission
to retune the detector on this dataset.

### Per-GT-size results

Sizes are `sqrt(width*height)` in **original image pixels**: small <32, medium
32–<96, large >=96. Test GT counts: **1,475 / 1,522 / 1,310**; dev:
**27 / 176 / 690**. Matching is global before bucketing, never independently
rematched within buckets. Matched TP and unmatched FN use **GT size**. Since FP
has no GT, it uses **predicted box size**; the resulting bucket P/F1 are a
size-attributed diagnostic, not COCO area-filtered AP. Bucket recall is strictly
per-GT-size. Counts and P/R/F1 for **all three matching criteria** are in
[`THERMAL_HOTSPOT_assets/results.json`](THERMAL_HOTSPOT_assets/results.json).

Center-hit P/R/F1 (%):

| Split | Configuration | Small | Medium | Large |
|---|---|---|---|---|
| test | Hotspot | 6.34 / 4.81 / 5.47 | 48.07 / 20.43 / 28.68 | 57.26 / 15.65 / 24.58 |
| test | YOLO boat | 92.59 / 3.39 / 6.54 | 97.13 / 42.25 / 58.88 | 90.48 / 17.40 / 29.19 |
| test | YOLO maritime | 88.14 / 3.53 / 6.78 | 94.79 / 43.04 / 59.20 | 89.74 / 18.70 / 30.95 |
| test | Fusion @.35 | 17.70 / 5.22 / 8.06 | 80.81 / 44.81 / 57.65 | 81.67 / 22.44 / 35.21 |
| test | Live reported | 45.71 / 4.34 / 7.93 | 87.71 / 43.63 / 58.27 | 82.96 / 19.69 / 31.83 |
| dev (eval only) | Hotspot | 0.12 / 3.70 / 0.24 | 20.19 / 24.43 / 22.11 | 81.65 / 25.80 / 39.21 |
| dev (eval only) | YOLO boat | 0 / 0 / 0 | 71.88 / 13.07 / 22.12 | 90.95 / 26.23 / 40.72 |
| dev (eval only) | YOLO maritime | 0 / 0 / 0 | 71.88 / 13.07 / 22.12 | 90.29 / 26.96 / 41.52 |
| dev (eval only) | Fusion @.35 | 0.24 / 3.70 / 0.45 | 36.67 / 31.25 / 33.74 | 86.56 / 38.26 / 53.07 |
| dev (eval only) | Live reported | 0 / 0 / 0 | 41.75 / 24.43 / 30.82 | 85.61 / 32.75 / 47.38 |

### YOLO-empty subset and recovery

Here “YOLO empty” means no sidecar-maritime prediction >=0.35 (not truly empty
sea). Both matching and denominators are restricted to these frames. All
recovered matches below are additions over a zero-output YOLO baseline.

| Split | YOLO-empty frames | GT in subset | Fusion @.35 recovered center / IoU .3 / IoU .5 | Live reported recovered center | Center recall: fusion / live |
|---|---:|---:|---|---:|---|
| test | 432 | 1,218 | 101 / 18 / 3 | 34 | 8.29% / 2.79% |
| dev (eval only) | 275 | 378 | 111 / 2 / 1 | 60 | 29.37% / 15.87% |

On test this is 39.96% of frames; dev 60.18%. Defining empty as **boat-only**
instead gives 462 test frames / 1,362 GT and 278 dev frames / 381 GT. Fusion
center hits in those subsets are 128 / 114, and live 61 / 63 respectively, but
these totals also include non-boat YOLO matches and must **not** all be credited
to thermal recovery. Full subset P/R/F1/counts, including IoU criteria and size
buckets, are in the JSON asset.

### Empty-sea controls

Deterministic seed **73021**, test split only, one crop per source frame, first
200 eligible frames in seeded order. Start strictly at/below the estimated
horizon (no above-horizon margin): same gray-friendly estimator at width <=480;
if it declines, use `tiled.estimate_horizon_y`. Candidate grid stride half crop
size, including the last fitting edge. Shuffle candidates deterministically and
take the first with **no intersection with any GT padded 10% of width/height
on each side**. No detector outputs inform sampling. Crop size 320x240, reduced
height to largest available sea region when necessary; minimum 64x48, no upscale.
549 of 1,081 test frames have eligible grid crops; 200 used, **136 at 320x240**,
64 reduced-height crops (all width 320), **12.58272 MP total**. 19 selected crops
use the unconditional horizon fallback. Source IDs, coordinates, areas and
per-crop counts: [`crops.json`](THERMAL_HOTSPOT_assets/crops.json).

These are **GT-empty crops**, not independently audited empty sensor captures:
horizon estimates, unlabeled objects and crop boundaries can bias FP rates.
They must not replace naturally empty held-out recordings.

| Empty input | Configuration | False contacts | FP/crop or frame | With >=1 FP | FP/MP |
|---|---|---:|---:|---:|---:|
| IR test crops (200) | Hotspot | 216 | 1.080 | 28.00% | 17.166 |
| IR test crops (200) | Fusion @.35 | 201 | 1.005 | 28.00% | 15.974 |
| IR test crops (200) | Live reported | 93 | 0.465 | 19.00% | 7.391 |
| SMD grayscale dev (14), **already used in tuning** | Hotspot | 1 | 0.0714 | 7.14% | 0.0344 |
| SMD grayscale dev (14), **already used in tuning** | Fusion @.35 | 1 | 0.0714 | 7.14% | 0.0344 |
| SMD grayscale dev (14), **already used in tuning** | Live reported | 0 | 0 | 0% | 0 |

SMD controls are full-resolution grayscale conversions, 29.0304 MP total.
They are an in-tuning reference, **not new held-out evidence**.

### Latency on this VM

CPU **Intel(R) Xeon(R) Processor**, 8 logical CPUs visible; process affinity
**0–3**, **4 threads** via `pin_threads`, CPU ONNX Runtime 1.23.2, OpenCV
4.11.0, NumPy 2.2.6, BoxMOT 26.0.0 (CPU Torch 2.14.1). Five warmup calls;
image disk decode excluded. No GPU. Single pass, no confidence intervals.

RGB set: **62 FVessel dev images**, seven nonquarantined clips from current
`configs/splits.yaml`, sorted local paths stride 20; **0/62 pass is_thermal**.
Exact paths are in results JSON. Gate repeated 10 times/image below.

| Operation | N | Mean ms | p50 ms | p95 ms |
|---|---:|---:|---:|---:|
| is_thermal on RGB | 620 | 2.190 | 2.193 | 2.336 |
| Hotspot proposer, every IR test frame | 1,081 | 5.813 | 5.002 | 11.951 |
| YOLO alone, IR test (.05 inference, reused at .35) | 1,081 | 70.871 | 65.091 | 119.106 |
| Live fusion detector, IR test, reconstructed | 1,081 | 73.672 | 67.246 | 123.016 |
| Engine analyze IR, thermal off | 1,081 | 120.362 | 118.341 | 164.762 |
| Engine analyze IR, thermal on | 1,081 | 125.141 | 123.107 | 172.344 |
| Engine analyze RGB, thermal off | 62 | 169.338 | 165.820 | 194.898 |
| Engine analyze RGB, thermal on | 62 | 173.399 | 172.492 | 196.671 |

Reconstructed fusion = cached original YOLO wall time + measured wrapper call
using those detections; this avoids extra YOLO inference, but is not a contiguous
wall-clock benchmark. Includes live .05 coverage behavior, not just .35 fusion.

Engine calls use actual ONNX detector, actual ByteTrack/aggregator and normal
report/nightlight processing; `embed="none"`, no anomaly fallback, no transport
or disk decode. **Independent still-frame calls** clear source state before each
call so numeric IR filenames are not misrepresented as verified temporal GT.
Wall timing includes fresh tracker/source creation. Thus these are end-to-end
**cold-source analyze** latencies, not steady-state video or MobileCLIP latency.
Thermal-on/off order alternates per frame to reduce drift.

Mean paired on-minus-off: IR **+4.780 ms**, RGB **+4.062 ms**; the latter has a
noisy p95 **25.684 ms**, so the whole difference cannot be causally assigned to
the gate. The instrumented RGB thermal wrapper itself is **2.473 / 2.426 /
2.776 ms** mean/p50/p95, versus a separately timed gate in the same engine loop
**2.411 / 2.366 / 2.765 ms**. Since 0/62 RGB frames pass the gate, the production
short-circuit runs **no thermal proposer** on this set: incremental detector work
is gate-only (plus wrapper bookkeeping), not the noisy full-engine timing delta.
IR thermal-stage mean/p50/p95: **2.996 / 2.636 / 10.186 ms**.

### Reproduction and assets

```sh
.venv/bin/python edgeml/tools/eval_thermal.py \
  --data-root ~/data/clean_v3 \
  --model ~/models/D/yolo11n-640/model.onnx --threads 4 --seed 73021 \
  --crop-count 200 --cache ~/data/thermal_eval_yolo_cache.json \
  --output ~/data/thermal_eval_frozen
```

The script exports `results.json`, `crops.json`, and per-frame `predictions.jsonl`;
the first two are checked in here. Large predictions and model cache stay local
under `~/data/`; execution log: `~/data/thermal-eval.log`. Model SHA256 and frozen
constructor parameters are in the results asset. Boat-only aggregates were
derived from the same cached per-frame detections after the initial run; all
existing metrics reproduced **exactly**, with no inference or threshold changes.
Matching tests cover symmetric center-hit vs IoU, greedy score ordering,
one-to-one assignment, inclusive IoU cutoffs, size boundaries, empty inputs and
invalid criteria.

Latest-script replay with `--skip-engine-latency` reused the YOLO cache and
reproduced all metrics, subset rows, crop records and RGB gate counts exactly.
The replay comparison initially caught omitted additive boat-only rows in the
saved maritime-empty subset; the generated results asset now includes them.
Engine latency above remains from the original real-engine run, not recomputed.

Step B full suite (`.venv/bin/python -m pytest edgeml/tests -q`):

```text
FAILED edgeml/tests/test_sidecar_protocol.py::test_rpc_http_override_and_unexpected_engine_exception
FAILED edgeml/tests/test_sidecar_protocol.py::test_body_limits_reject_without_waiting_for_the_declared_body[-1]
2 failed, 618 passed in 8.03s
```

Only the same two protocol failures previously reproduced on clean origin/dev;
all five evaluator test cases pass in this run. Optional runtime dependencies
installed for evaluation enable previously skipped tests. Full log:
`~/data/thermal-step-b-pytest.log`. No visual/UI verification applies.

### Licenses and permitted use

* **IR:** HF `hanchong/real-infrared-maritime-vessel-dataset`, revision
  **64a6646**; **no declared license**, clean-v3 `internal_dev_only` note:
  `upstream card states no license; not for shipped models`. **Evaluation only,
  never tuning or training**; no redistribution of IR images in these assets.
* **SMD:** queried `ghost_fleet.datasets_clean_v3` for
  `smd_vis_onboard_sample`: license **`unknown (academic research use)`**;
  source revision `5975fe6481e8e5bd1a3776fdb6381eb1b0e20c60`, source URL
  https://sites.google.com/site/dilipprasad/home/singapore-maritime-dataset.
  Treat as academic/internal reference, not cleared for commercial deployment.
  The 14 grayscale negatives were already used in Step A tuning.
* **Synthetic:** generated in-repo by `tools/synth_thermal.py`, no external
  image source; seed/data construction and actual tuning history recorded above.

## Step B2: report-threshold wiring fix (not tuning)

Rebased onto origin/dev `1b4b716`, retaining both live tiling/MOB labeling and
thermal fallback. Composition: ONNX → `LiveTiledDetector` → optional anomaly →
thermal, so fallback sees final base detections. Rebase-only pytest:
`643 passed in 11.66s`.

Engine wiring now forces fusion cover threshold to the engine `score_thr` and
floors **only emitted fused thermal scores** to that threshold. Standalone
proposer and standalone fusion retain original scores/defaults. No proposer,
gate, geometry or tuning thresholds changed. Tests exercise overlapping YOLO
candidates just below the report cutoff, low thermal scores surviving reports,
YOLO at the cutoff blocking fallback, configurable cutoffs, composition, and
unchanged standalone scores.

Re-ran `eval_thermal.py` with the same cache, splits, crops and seed, using
`--skip-engine-latency`. Current assets are `results.json` and `crops.json`;
old assets are retained under the explicit `before_cover_threshold_fix` names.
Actual live now includes the rebased default live tiler; **native fixed** also
isolates just the cover/report wiring change without tiling. Thus the actual-live
improvement must not all be attributed to the threshold bug fix. Tiled ONNX
outputs are separately cached by crop-pixel hash; native outputs reuse the
original cache. Engine latency was not repeated; no old engine numbers are
claimed to describe the fixed live path.

| Split | Configuration | Center P/R/F1 (%) | IoU .3 P/R/F1 (%) | IoU .5 P/R/F1 (%) | Proposals/frame |
|---|---|---|---|---|---:|
| test | Live before cover-threshold fix | 81.62 / 22.89 / 35.76 | 76.74 / 21.52 / 33.62 | 73.68 / 20.66 / 32.28 | 1.117 |
| test | Native fixed, no tiling | 64.25 / 24.45 / 35.42 | 57.11 / 21.73 / 31.48 | 54.42 / 20.71 / 30.00 | 1.516 |
| test | Actual live fixed + default tiling | 68.04 / 32.23 / 43.74 | 57.89 / 27.42 / 37.21 | 53.87 / 25.52 / 34.63 | 1.887 |
| dev (evaluation only) | Live before cover-threshold fix | 56.75 / 30.12 / 39.36 | 40.08 / 21.28 / 27.80 | 34.39 / 18.25 / 23.85 | 1.037 |
| dev (evaluation only) | Native fixed, no tiling | 36.78 / 35.83 / 36.30 | 22.07 / 21.50 / 21.78 | 18.85 / 18.37 / 18.60 | 1.904 |
| dev (evaluation only) | Actual live fixed + default tiling | 39.94 / 43.78 / 41.77 | 23.19 / 25.42 / 24.25 | 18.28 / 20.04 / 19.12 | 2.142 |

On the same **native YOLO-empty** subsets (432 test frames / 1,218 GT; 275 dev /
378 GT), native-fixed thermal recovery is **101 / 111** center matches
(8.29% / 29.37% recall), versus **34 / 60 before the fix**. Actual-live fixed
with tiling recovers **156 / 115** center matches (12.81% / 30.42%); these totals
include extra tile detections as well as thermal. All criteria, size buckets,
boat-only and maritime-empty subsets remain available in current JSON.

| Empty control | Fixed actual-live FP/frame | With >=1 FP | FP/MP |
|---|---:|---:|---:|
| Same 200 IR crops | 1.005 (201 contacts) | 28.00% | 15.974 |
| Same 14 SMD grayscale tuning negatives | 0.0714 (1 contact) | 7.14% | 0.0344 |

Unchanged standalone proposer and requested fusion @.35 crop metrics reproduce
the earlier results exactly. Fixed native recall improves, but exposing previously
dropped low-score proposals also increases false contacts; this is the expected
effect of the wiring fix, not grounds to tune against IR.

Cheap timing refresh, mean/p50/p95 ms: proposer **4.276 / 3.052 / 8.495**,
RGB gate **2.234 / 2.217 / 2.392**. Cached YOLO original timing unchanged.
Actual-live reconstructed native+tile inference plus thermal stage:
**138.723 / 97.770 / 292.180 ms**; excludes tiler merge/hash/cache bookkeeping,
and soft budget admission uses measured call cost, so it is not a fresh
contiguous end-to-end benchmark. Engine latency refresh intentionally skipped.

Final B2 full suite: `645 passed in 8.47s`. Earlier protocol failures are resolved
by current origin/dev; no protocol changes made here. Log:
`~/data/thermal-b2-pytest.log`; evaluator log: `~/data/thermal-eval-b2.log`.

## Step D: live-path refresh after open-vocabulary rebase

Rebased onto `origin/dev` **908cce2** (#271). The service conflict keeps both
the thermal-aware frame unknownness calculation (including duplicates) and
the new open-vocabulary detection hook. Composition remains native ONNX →
default live tiling/MOB → optional anomaly → thermal fallback. No thermal
proposer, modality-gate or new-contact defaults changed.

Regression tests now exercise an active open-vocabulary prompt alongside a
thermal OPEN: its `thermal contact` label, unknownness floor and `newContact`
survive, as do the stored edge label and `new_contact=1` tag when the same
frame also matches a different open-vocabulary label. Whole-word matching
accepts `thermal contact(s)` without matching `hypothermal contact` or
`thermal contactless`. Production code needed no additional label/tag rewrite.

Reran `eval_thermal.py` on test/dev, the same YOLO-empty subsets and deterministic
empty crops, **including fresh real-engine paired latency this time**:

```sh
OMP_NUM_THREADS=4 .venv/bin/python edgeml/tools/eval_thermal.py \
  --output ~/data/thermal_eval_rebased \
  --cache ~/data/thermal_eval_yolo_cache.json
```

Current `results.json` / `crops.json` assets are this refresh; the separately
named **before cover-threshold fix** assets and historical rows remain intact.
YOLO native/tile outputs are reused from the validated model/config/image cache;
engine latency runs its own fresh inference. The live detector reconstruction
is still valid with #271: prompt results are appended after the base detections,
not rewritten into them. These benchmarks pass no open-vocabulary prompt and
disable embeddings, so they are not a prompt-encoder latency/quality evaluation.

| Rebased actual live | Center P/R/F1 % | IoU .3 P/R/F1 % | IoU .5 P/R/F1 % | Proposals/frame |
|---|---|---|---|---:|
| test | 68.04 / 32.23 / 43.74 | 57.89 / 27.42 / 37.21 | 53.87 / 25.52 / 34.63 | 1.887 |
| dev (evaluation only) | 39.94 / 43.78 / 41.77 | 23.19 / 25.42 / 24.25 | 18.28 / 20.04 / 19.12 | 2.142 |

All detection/matching metrics reproduce B2 exactly. On the same native
YOLO-empty subsets (432 test / 275 dev frames), actual-live recovered center /
IoU .3 / IoU .5 matches are **156 / 36 / 19** test and **115 / 7 / 6** dev;
center subset recall **12.81% / 30.42%**. Same 200 IR crops: **201** false
contacts, **1.005/crop**, **28%** nonempty, **15.974/MP**. Same 14 grayscale
SMD tuning negatives: **1** false contact, **.0714/frame**, **7.14%** nonempty,
**.0344/MP**. These controls remain annotation-empty, not audited negatives.

### Refreshed latency (mean / p50 / p95 milliseconds)

Intel Xeon virtual CPU, 4 threads, affinity CPUs 0–3. The independent held-out
MOT replay used CPUs 4–7 concurrently; Go/Python checks were also confined to
that other CPU set. This is a shared-VM timing observation, not an isolated
hardware throughput claim. Same independent still-frame protocol as Step B:
1,081 IR test images, 62 FVessel RGB images, warmed models, sources cleared per
image, alternating thermal on/off order, no embeddings/prompts.

| Measurement | Mean / p50 / p95 ms |
|---|---|
| Proposer (IR test) | 4.846 / 3.785 / 10.829 |
| RGB gate, standalone repeated sample | 2.635 / 2.553 / 2.827 |
| Cached native+tile fusion reconstruction | 139.216 / 97.880 / 292.567 |
| Engine IR, thermal off | 91.552 / 56.802 / 207.422 |
| Engine IR, thermal on | 92.931 / 59.937 / 210.541 |
| Engine IR, paired on − off | 1.379 / 1.696 / 31.224 |
| Engine RGB, thermal off | 175.071 / 169.107 / 202.891 |
| Engine RGB, thermal on | 174.365 / 171.054 / 201.551 |
| Engine RGB, paired on − off | −0.706 / 1.639 / 13.870 |
| RGB wrapper thermal stage in engine | 2.322 / 2.263 / 2.757 |
| RGB gate measured in engine loop | 2.235 / 2.166 / 2.609 |

**0/62 RGB gate positives**: thermal cost is gate-only plus bookkeeping in
that sample. The noisy negative mean whole-engine delta does not mean a
negative gate cost or a speedup. Original cached YOLO costs are not fresh
measurements and must not be compared causally with new engine timing; the
reconstructed row excludes cache/hash/merge bookkeeping. No performance
improvement is attributed to #271 from these changing VM timings.

Evaluator log: `~/data/thermal-eval-rebased.log`. New-contact birth signaling,
its precision/recall trade-off and microsecond cost are in
[NEW_CONTACT.md](NEW_CONTACT.md).

### Final integration checkpoint: #274, origin/dev 5083be1

Rebased again after the marine-mammal merge. Thermal fusion now wraps the
final base stack **after** optional marine verification. The dispatcher
forwards the source frame index through the thermal wrapper when marine is
enabled, preserving per-source scan cadence. The marine verifier, tracker
and aggregator logic is otherwise unchanged; the active-marine integration
test still checks alternating scan frames for two sources. Its internal
detector access was updated for the additional outer thermal wrapper.

With default `MarineMammalConfig.enabled=False`, `wrap_detector` returns its
input unchanged. `MarineTracker` delegates tracking; its extra provenance
handling and `MarineAggregator`'s overrides delegate unchanged for non-marine
labels. Default YOLO/thermal cannot emit `marine mammal`. Thus there is no
changed default detection/association decision to re-evaluate: the full IR
quality/crop/recovery metrics and MOT birth tables above remain the #271
measurements, **not new full replays at 5083be1**. Their source fingerprints
are intentionally retained. No thresholds changed.

As requested for this default-off integration, reran only a **quick fresh
real-engine latency sanity check**: first 32 sorted IR test images and first
16 previously benchmarked RGB paths, 4 threads, same paired still-frame
protocol. This small subset is not a replacement for the full benchmark.
[post274_latency.json](THERMAL_HOTSPOT_assets/post274_latency.json) stores it.

| Post-#274 sanity, mean / p50 / p95 ms | Result |
|---|---|
| IR thermal off, n=32 | 138.675 / 134.687 / 161.045 |
| IR thermal on, n=32 | 141.343 / 139.344 / 161.663 |
| IR paired on − off | 2.668 / 3.160 / 14.771 |
| RGB thermal off, n=16 | 173.713 / 171.707 / 193.876 |
| RGB thermal on, n=16 | 175.661 / 166.490 / 218.276 |
| RGB paired on − off | 1.948 / 2.253 / 25.739 |
| RGB wrapper thermal stage | 2.456 / 2.204 / 3.309 |
| RGB gate in engine loop | 2.235 / 2.167 / 2.574 |

Post-#274 checks: **696 passed in 8.56s** (Python); `make test` passed;
Node **388 pass / 0 fail**; `make prod-gate`: **PASS**. Full gate output is
`~/data/prod-gate-final.log`. Full `make verify` was attempted before #274:
14/15 stages passed, image stage hit a PyPI download timeout; its failed
sidecar build passed on retry. That report is evidence for the earlier
commit, not a full verification of this later rebase. It was not rerun in
full at this final checkpoint. Log: `~/data/thermal-post274-sanity.log`.
