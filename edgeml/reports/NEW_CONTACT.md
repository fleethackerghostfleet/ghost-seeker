# New-contact birth evaluation

Companion: [thermal fallback evaluation](THERMAL_HOTSPOT.md). This evaluation uses
**no IR data** and changes no production thresholds. Defaults remain
`memory_s=30`, `iou_thr=.3`, `dist_diag=.5`. There was **no parameter sweep or
tuning**: dev was replayed first, defaults frozen, then held-out replayed once.
At the requested integration checkpoint, the branch was rebased onto
`origin/dev` **908cce2** (#271), and both splits were replayed again with
fresh detector caches on that live path. Tables/assets below are post-rebase
runs, not a second parameter selection on held-out data. All pre/post-rebase
birth and suppression metrics reproduce exactly for both detectors/splits.

## Method and reproducibility

`edgeml/tools/eval_newcontact.py` constructs the real `EdgemlEngine` per clip,
with YOLO11n-640 ONNX, default LiveTiledDetector, default thermal fallback,
ByteTrack, the live `_ZeroShotAggregator`, and per-source NewContactDetector.
Embeddings are off. Every frame is consumed at its native MOT cadence (Ghost
Fleet 5 Hz; FVessel 1 Hz), with captured time
`1700000000000 + round((frame-1)/fps*1000)` ms. Sources reset between clips;
there is no clip-end padding/flush to manufacture delayed OPENs.

No open-vocabulary prompts are sent. #271's hooks are present but its optional
prompt encoder is inactive with embeddings off. Separate integration tests
exercise an active prompt alongside thermal OPENs and confirm that the thermal
label, unknownness floor, `newContact`, edge `new_contact` tag and whole-word
label matching coexist without rewriting one another.

The YOLO run caches **final detector-stack detections including empty frames**,
not sightings. Initial results below were collected with live inference, not
cache replay. A subsequent cached run still uses the real ByteTrack,
aggregator, wire-box conversion and new-contact logic. Cache validation includes
model+pipeline source hash, frame-file size/mtime manifest, sequence metadata,
frame count and thread count. Live tiling has a wall-clock admission budget;
the cache freezes the actual admitted passes from this run, not a hypothetical
budget-independent set of tiles.

The **oracle-d0.3** variant replaces the entire detector stack with the existing
`OracleDetector`: GT boxes, score .99, independent 30% detection dropout,
seed 1729 reset per clip, no jitter. It does **not** pass GT through image tiles
or thermal fallback. The rest of the real engine path is unchanged; in
particular GT identities never enter ByteTrack. Oracle and YOLO have separate
fresh tracker/aggregator state.

The existing lifetime `eval.sightings.match_sightings` majority-frame match is
not a birth-flag metric. This evaluator reuses `data.mot` policy selection/GT
reading, `OracleDetector` and `types.box_iou`, with the explicit matcher below.

Reproduce from the repo root:

```sh
OMP_NUM_THREADS=4 .venv/bin/python edgeml/tools/eval_newcontact.py \
  --split dev --output ~/data/newcontact-dev.json
# Freeze configuration before this command; do not tune using its output.
OMP_NUM_THREADS=4 .venv/bin/python edgeml/tools/eval_newcontact.py \
  --split test --output ~/data/newcontact-test.json
```

Defaults resolve data from `~/data/clean_v3/edgeml/eval`, model from
`~/models/D/yolo11n-640/model.onnx`, cache from `~/data/newcontact-cache`.
Use a new cache directory if model, pipeline or input identity changes.
Full per-clip metrics, OPEN boxes/flags, licenses and suppression counts:
[dev.json](NEW_CONTACT_assets/dev.json), [test.json](NEW_CONTACT_assets/test.json).

### Matching and denominators

* A GT birth is the first MOT frame for a persistent GT track ID, independent
  of whether the detector ever sees it. All GT classes count; no class-match
  restriction is applied to OPENs.
* An OPEN signal is credited if its **wire OPEN box has IoU >= .3** with GT
  **in that same frame**, whose birth was **0–5 seconds before the OPEN**
  (inclusive), and that GT birth has not already been credited. There is no
  look-ahead or grace after the window. IoU-only, **not center-hit**.
* Matching is chronological, score-descending within a frame, then sighting ID;
  highest-IoU eligible uncredited GT wins (track-ID tie break). Each birth can
  receive at most one TP. Every other evaluated signal is FP. Precision is
  TP/(TP+FP); recall is credited births/all eligible GT births. Latency is
  median `(open_frame-birth_frame)/fps` among credited births, not among misses.
* **All births** includes startup objects. **Mid-clip births** uses only births
  at frame >1. For mid-clip precision, flags spatially matching a frame-1 GT
  object within the first 5 seconds are ignored as startup signals. Later
  reopens of startup tracks remain FP. Thus a correct delayed startup OPEN
  is not misleadingly penalized as a mid-clip false alarm. Assets retain the
  ignored-startup counts.
* Baseline uses **every OPEN** as new. Filtered uses only OPENs with
  `newContact=true`; all UPDATE/relink, DUPLICATE and CLOSE events are excluded
  from both. Flags on a non-OPEN would abort the evaluator.
* A **correct repeat suppression** spatially matches a GT track already
  matched by an earlier OPEN from a different sighting (regardless of the
  earlier OPEN's birth-window eligibility). Other suppressions are separately
  categorized as matched-but-not-previously-reported or unmatched. Unmatched
  does not prove background, particularly with partial GT. Suppressed OPENs
  still exist as sightings; this only removes their *new* signal.

## Split and annotation coverage

Selection uses `data.mot.load_set` and current `configs/splits.yaml`, **not**
the stale FVessel index split. FVessel clip05 is quarantined and never loaded.
Following existing tracking evaluation (`run_c.py`), `gt_status=incomplete`
clips are excluded: dev `Chase_N_Fish_Sharky`,
`Far_away_but_did_you_see_that_splash`; test
`Dolphin_from_far_distance_in_Bay_of_Bengal`. These are not sea negatives.
The complete zero-GT dev clip `Sailing_POV_Corfu_Paxos` is retained.

| Policy split after exclusions | Ghost Fleet clips / frames / births | FVessel clips / frames / births | Combined births / mid-clip births |
|---|---|---|---|
| dev | 7 / 1,160 / 13 | 7 / 1,222 / 18 | 31 / 28 |
| held-out | 8 / 4,641 / 45 | 2 / 1,189 / 7 | 52 / 46 |

Partial GT is retained in the policy-wide diagnostic table, but is **not**
headline ground truth: [A.md](A.md) explicitly warns against headline recall
on partial test clips. The complete-GT headline excludes `partial` Ghost
Fleet clips while retaining public-GT FVessel and the complete sea negative.
All per-clip GT status values are recorded in the assets.

## Complete-GT headline

P/R are percentages; TP/FP are counts, latency seconds. Complete-GT dev is
10 clips / 1,721 frames / **22 births (20 mid-clip)**; held-out is
8 clips / 4,254 frames / **19 births (14 mid-clip)**. Zero latency means
the OPEN and annotated birth share a sampled frame, not zero compute cost.

| Split | Detector | Signal | All TP/FP | All P/R % | Mid TP/FP | Mid P/R % | Median latency all / mid s |
|---|---|---|---:|---|---:|---|---|
| dev | YOLO | every OPEN | 11/57 | 16.18 / 50.00 | 9/57 | 13.64 / 45.00 | .2 / 0 |
| dev | YOLO | newContact | 10/41 | 19.61 / 45.45 | 8/41 | 16.33 / 40.00 | .1 / 0 |
| dev | oracle-d0.3 | every OPEN | 21/2 | 91.30 / 95.45 | 19/2 | 90.48 / 95.00 | 3 / 3 |
| dev | oracle-d0.3 | newContact | 21/1 | 95.45 / 95.45 | 19/1 | 95.00 / 95.00 | 3 / 3 |
| held-out | YOLO | every OPEN | 10/74 | 11.90 / 52.63 | 5/74 | 6.33 / 35.71 | .2 / .4 |
| held-out | YOLO | newContact | 10/59 | 14.49 / 52.63 | 5/59 | 7.81 / 35.71 | .2 / .4 |
| held-out | oracle-d0.3 | every OPEN | 18/5 | 78.26 / 94.74 | 13/5 | 72.22 / 92.86 | 1 / 2 |
| held-out | oracle-d0.3 | newContact | 17/1 | 94.44 / 89.47 | 12/1 | 92.31 / 85.71 | 1 / 2.5 |

**Finding:** complete-GT held-out YOLO retains the same credited births while
removing 15 FP flags, but absolute birth precision remains low. Oracle removes
four repeat flags at the cost of one timely true birth. Dev already shows a
recall cost for YOLO. This is not a universally better birth detector.

## Policy-wide diagnostic (includes partial GT)

These use all non-incomplete policy clips: dev **31 / 28** all/mid births;
held-out **52 / 46**. Partial labels and the dense partial cruise/dinghy clip
strongly affect these numbers; do not substitute them for complete-GT recall.

| Split | Detector | Signal | All TP/FP | All P/R % | Mid TP/FP | Mid P/R % | Median latency all / mid s |
|---|---|---|---:|---|---:|---|---|
| dev | YOLO | every OPEN | 20/80 | 20.00 / 64.52 | 17/80 | 17.53 / 60.71 | .3 / .4 |
| dev | YOLO | newContact | 16/57 | 21.92 / 51.61 | 13/57 | 18.57 / 46.43 | .2 / .2 |
| dev | oracle-d0.3 | every OPEN | 27/5 | 84.38 / 87.10 | 24/5 | 82.76 / 85.71 | 3 / 3 |
| dev | oracle-d0.3 | newContact | 26/2 | 92.86 / 83.87 | 23/2 | 92.00 / 82.14 | 3 / 3 |
| held-out | YOLO | every OPEN | 16/108 | 12.90 / 30.77 | 10/108 | 8.47 / 21.74 | .3 / .4 |
| held-out | YOLO | newContact | 13/72 | 15.29 / 25.00 | 7/72 | 8.86 / 15.22 | .2 / .2 |
| held-out | oracle-d0.3 | every OPEN | 35/6 | 85.37 / 67.31 | 29/6 | 82.86 / 63.04 | 1 / 1 |
| held-out | oracle-d0.3 | newContact | 25/1 | 96.15 / 48.08 | 19/1 | 95.00 / 41.30 | 1 / 1 |

Per-dataset breakdown (partial GT included); all baseline metrics, FP counts
and latency distributions are also in the assets:

| Split | Detector | Set | All TP OPEN → flagged | Flagged all P/R % | Mid TP OPEN → flagged | Flagged mid P/R % |
|---|---|---|---|---|---|---|
| dev | YOLO | Ghost Fleet | 12 → 9 | 30.00 / 69.23 | 9 → 6 | 22.22 / 60.00 |
| dev | YOLO | FVessel | 8 → 7 | 16.28 / 38.89 | 8 → 7 | 16.28 / 38.89 |
| dev | oracle-d0.3 | Ghost Fleet | 10 → 9 | 90.00 / 69.23 | 7 → 6 | 85.71 / 60.00 |
| dev | oracle-d0.3 | FVessel | 17 → 17 | 94.44 / 94.44 | 17 → 17 | 94.44 / 94.44 |
| held-out | YOLO | Ghost Fleet | 15 → 12 | 24.00 / 26.67 | 9 → 6 | 13.64 / 15.38 |
| held-out | YOLO | FVessel | 1 → 1 | 2.86 / 14.29 | 1 → 1 | 2.86 / 14.29 |
| held-out | oracle-d0.3 | Ghost Fleet | 28 → 18 | 94.74 / 40.00 | 22 → 12 | 92.31 / 30.77 |
| held-out | oracle-d0.3 | FVessel | 7 → 7 | 100 / 100 | 7 → 7 | 100 / 100 |

## Suppressions and timing

| Split / detector | OPENs → flagged | Suppressed | Correct repeat | Matched, not previously reported | Unmatched |
|---|---:|---:|---:|---:|---:|
| dev YOLO | 100 → 73 | 27 | 2 | 6 | 19 |
| dev oracle-d0.3 | 32 → 28 | 4 | 2 | 1 | 1 |
| held-out YOLO | 124 → 85 | 39 | 4 | 6 | 29 |
| held-out oracle-d0.3 | 41 → 26 | 15 | 5 | 10 | 0 |

Complete-GT subsets: dev YOLO suppresses **17** (2 repeat / 3 matched-new /
12 unmatched), oracle **1** (unmatched). Held-out YOLO suppresses **15**
(4 / 1 / 10), oracle **5** (4 / 1 / 0). Not all matched-new suppressions
would have been TP within the five-second window; TP loss is measured
separately above. Nor are all removed FP flags tracker ID switches.

The timings sum only the body of `NewContactDetector.is_new`
via an instance proxy on each source: event calls including early-return
non-OPENs, divided by **all replayed frames**, including frames with zero
events. They exclude proxy bookkeeping, tracker, aggregator and detector cost;
they are not a whole-engine on/off difference. Timer/call overhead is not
subtracted.

| Rebased replay | Frames | is_new calls | is_new µs/replayed frame |
|---|---:|---:|---:|
| dev YOLO | 2,382 | 370 | 1.431 |
| dev oracle-d0.3 | 2,382 | 125 | .279 |
| held-out YOLO | 5,830 | 468 | .613 |
| held-out oracle-d0.3 | 5,830 | 209 | .169 |

CPU: Intel Xeon virtual CPU, 4 threads per replay, CPU ONNX Runtime. Rebased
dev used CPUs 0–3; held-out used CPUs 4–7 concurrently. Thermal paired latency
and final tests subsequently used the other CPU set; these are shared-VM
microsecond observations, not isolated hardware benchmarks.
From [Step B2](THERMAL_HOTSPOT.md): RGB thermal-gate mean/p50/p95
**2.234 / 2.217 / 2.392 ms**. The earlier paired engine RGB sample admitted
0/62 thermal frames, with wrapper mean **2.473 ms** vs gate **2.411 ms**;
thermal overhead there is gate-only plus bookkeeping. That historical RGB
finding is not a claim that every grayscale-looking MOT frame bypasses thermal.
The post-#271 refresh in the companion report measures the RGB gate at
**2.635 / 2.553 / 2.827 ms** standalone, or **2.235 / 2.166 / 2.609 ms** in
the paired engine loop, again **0/62** RGB positives. Actual RGB wrapper cost
is **2.322 ms mean**; noisy whole-engine on/off deltas are not gate cost.

## Interpretation and limitations

* This is **birth signaling**, not lifetime object recall. An object first
  reported after 5 seconds is FP under this task even if correctly tracked.
  Recall can also be lost to ByteTrack/aggregator merging before any OPEN.
* The spatial novelty heuristic has no appearance/camera-motion compensation.
  Distinct objects entering a recently occupied region can be suppressed.
  This evaluation is not evidence that every suppression is a corrected ID
  switch. A higher filtered precision can come at lower birth recall.
* Default memory is bounded by available aggregator state/history. This tests
  clip-local defaults, not long-running sources, source restarts, or a tuned
  memory horizon.
* Ghost Fleet labels include teacher+human and partial teacher coverage;
  unannotated objects inflate apparent FP. FVessel is 1 Hz, AIS-fused GT with
  occasional box lag, same fixed camera/source conditions across splits: a
  weak same-source hold-out, not a new camera benchmark. One dropout seed and
  small birth counts do not establish statistical superiority.
* No images, weights, data or labels were trained on or shipped by this step.

## Licenses

Sources: [A.md §1/§2/§7](A.md) and downloaded MOT `index.jsonl` / `seqinfo.ini`
(per-clip strings preserved in result assets).

* **ghost_fleet_5fps:** mixed source licenses, not a blanket open dataset.
  Most clips record "Standard YouTube license (no license stated); internal
  use only"; Facebook clips have no redistribution permission stated;
  `US_Destroyer_POV_Warship_Crossing` is a Reddit repost of DVIDS material,
  locally marked no license stated/internal use. Cruise/dinghy and ferry
  record YouTube CC BY. `A.md` describes Norfolk as CC-BY, but local MOT
  metadata marks its segments standard YouTube/internal-use: use the more
  conservative recorded restriction until provenance is reconciled.
* **fvessel_clip10:** [A.md](A.md) records HF `gy65896/FVessel`, research use
  (CC BY-NC-SA on HF). MOT metadata says academic/research use; video
  redistribution is subject to the authors' terms. This is internal offline
  evaluation, not a grant to ship videos or train commercial weights.
