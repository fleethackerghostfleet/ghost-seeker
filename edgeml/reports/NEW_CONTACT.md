# New-contact birth evaluation

Review safety change: thermal fallback is now **off by default**; opt in
separately with `--thermal-fallback`. Its frozen opt-in IR false-contact rate
was 1.005/crop (28% nonempty), with unknownness ≥0.9. This change does not
disable the weight-1 new-contact scorer. The final review replay below uses
the corrected temporal comparison and independent source-local memory.

Companion: [thermal fallback evaluation](THERMAL_HOTSPOT.md). This evaluation uses
**no IR data** and changes no production thresholds. Defaults remain
`memory_s=30`, `iou_thr=.3`, `dist_diag=.5`. There was **no parameter sweep or
tuning**: dev was replayed first, defaults frozen, then held-out replayed once.
The branch includes #271 and #274 (`origin/dev` **5083be1**). Both splits were
replayed with fresh detector caches after the review fixes and the thermal
default-off safety change. These are not new threshold selections on held-out
data. Historical integration checkpoints at the end describe earlier runs;
the main tables/assets are the final review replay.

## Method and reproducibility

`edgeml/tools/eval_newcontact.py` constructs the real `EdgemlEngine` per clip,
with YOLO11n-640 ONNX, default LiveTiledDetector, thermal fallback **off**,
ByteTrack, the live `_ZeroShotAggregator`, and per-source NewContactDetector.
Embeddings are off. Every frame is consumed at its native MOT cadence (Ghost
Fleet 5 Hz; FVessel 1 Hz), with captured time
`1700000000000 + round((frame-1)/fps*1000)` ms. Sources reset between clips;
there is no clip-end padding/flush to manufacture delayed OPENs.

Novelty compares the new sighting's **first observed box**, not its moved
confirmation-time box, against each neighbour's last observation at or before
that same `first_t_s`. Only missing trajectories fall back to the event box.
Before every aggregator update, the source-local detector snapshots open/closed
observations into its own copied history. History is pruned against the
30-second memory window; pending first-observation times delay pruning so
confirmation lag cannot discard an observation still needed by an upcoming OPEN.
Pending states themselves never suppress a flag. OPEN/UPDATE/CLOSE/relink
semantics and aggregator eviction are unchanged.

The eviction regression uses the library aggregator's defaults (20-second
closed-state eviction), then confirms a reappearance 25 seconds after its
last observation is not new. The live engine's existing relink override makes
its eviction horizon 40 seconds; independent history removes reliance on
either horizon. Memory is measured from last observation, **not close time**.

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
| dev | YOLO | every OPEN | 11/57 | 16.18 / 50.00 | 9/57 | 13.64 / 45.00 | 0.2 / 0 |
| dev | YOLO | newContact | 11/38 | 22.45 / 50.00 | 9/38 | 19.15 / 45.00 | 0.2 / 0 |
| dev | oracle-d0.3 | every OPEN | 21/2 | 91.30 / 95.45 | 19/2 | 90.48 / 95.00 | 3 / 3 |
| dev | oracle-d0.3 | newContact | 20/1 | 95.24 / 90.91 | 18/1 | 94.74 / 90.00 | 3 / 3 |
| held-out | YOLO | every OPEN | 10/66 | 13.16 / 52.63 | 5/66 | 7.04 / 35.71 | 0.2 / 0.4 |
| held-out | YOLO | newContact | 10/49 | 16.95 / 52.63 | 5/49 | 9.26 / 35.71 | 0.2 / 0.4 |
| held-out | oracle-d0.3 | every OPEN | 18/5 | 78.26 / 94.74 | 13/5 | 72.22 / 92.86 | 1 / 2 |
| held-out | oracle-d0.3 | newContact | 17/1 | 94.44 / 89.47 | 12/1 | 92.31 / 85.71 | 1 / 2.5 |

**Finding:** complete-GT held-out YOLO retains the same ten credited births
while removing **17** FP flags; dev retains its eleven credited births while
removing **19** FP flags. Absolute birth precision remains low. Oracle loses
one timely true birth in each complete-GT split while reducing false flags.
This is not a universally better birth detector.

## Policy-wide diagnostic (includes partial GT)

These use all non-incomplete policy clips: dev **31 / 28** all/mid births;
held-out **52 / 46**. Partial labels and the dense partial cruise/dinghy clip
strongly affect these numbers; do not substitute them for complete-GT recall.

| Split | Detector | Signal | All TP/FP | All P/R % | Mid TP/FP | Mid P/R % | Median latency all / mid s |
|---|---|---|---:|---|---:|---|---|
| dev | YOLO | every OPEN | 20/72 | 21.74 / 64.52 | 17/72 | 19.10 / 60.71 | 0.3 / 0.4 |
| dev | YOLO | newContact | 17/46 | 26.98 / 54.84 | 14/46 | 23.33 / 50.00 | 0.2 / 0.3 |
| dev | oracle-d0.3 | every OPEN | 27/5 | 84.38 / 87.10 | 24/5 | 82.76 / 85.71 | 3 / 3 |
| dev | oracle-d0.3 | newContact | 26/2 | 92.86 / 83.87 | 23/2 | 92.00 / 82.14 | 3 / 3 |
| held-out | YOLO | every OPEN | 16/100 | 13.79 / 30.77 | 10/100 | 9.09 / 21.74 | 0.3 / 0.4 |
| held-out | YOLO | newContact | 13/60 | 17.81 / 25.00 | 7/60 | 10.45 / 15.22 | 0.2 / 0.2 |
| held-out | oracle-d0.3 | every OPEN | 35/6 | 85.37 / 67.31 | 29/6 | 82.86 / 63.04 | 1 / 1 |
| held-out | oracle-d0.3 | newContact | 26/1 | 96.30 / 50.00 | 20/1 | 95.24 / 43.48 | 1 / 1 |

Per-dataset breakdown (partial GT included); all baseline metrics, FP counts
and latency distributions are also in the assets:

| Split | Detector | Set | All TP OPEN → flagged | Flagged all P/R % | Mid TP OPEN → flagged | Flagged mid P/R % |
|---|---|---|---|---|---|---|
| dev | YOLO | Ghost Fleet | 12 → 9 | 45.00 / 69.23 | 9 → 6 | 35.29 / 60.00 |
| dev | YOLO | FVessel | 8 → 8 | 18.60 / 44.44 | 8 → 8 | 18.60 / 44.44 |
| dev | oracle-d0.3 | Ghost Fleet | 10 → 9 | 90.00 / 69.23 | 7 → 6 | 85.71 / 60.00 |
| dev | oracle-d0.3 | FVessel | 17 → 17 | 94.44 / 94.44 | 17 → 17 | 94.44 / 94.44 |
| held-out | YOLO | Ghost Fleet | 15 → 12 | 30.00 / 26.67 | 9 → 6 | 17.65 / 15.38 |
| held-out | YOLO | FVessel | 1 → 1 | 3.03 / 14.29 | 1 → 1 | 3.03 / 14.29 |
| held-out | oracle-d0.3 | Ghost Fleet | 28 → 19 | 95.00 / 42.22 | 22 → 13 | 92.86 / 33.33 |
| held-out | oracle-d0.3 | FVessel | 7 → 7 | 100.00 / 100.00 | 7 → 7 | 100.00 / 100.00 |

## Suppressions and timing

| Split / detector | OPENs → flagged | Suppressed | Correct repeat | Matched, not previously reported | Unmatched |
|---|---:|---:|---:|---:|---:|
| dev YOLO | 92 → 63 | 29 | 2 | 5 | 22 |
| dev oracle-d0.3 | 32 → 28 | 4 | 2 | 1 | 1 |
| held-out YOLO | 116 → 73 | 43 | 4 | 6 | 33 |
| held-out oracle-d0.3 | 41 → 27 | 14 | 5 | 9 | 0 |

Complete-GT suppressions: dev YOLO **19** (2 repeat / 2 matched-new /
15 unmatched); dev oracle-d0.3 **2** (0 / 1 / 1); held-out YOLO **17**
(4 / 1 / 12); held-out oracle-d0.3 **5** (4 / 1 / 0).
Not all matched-new suppressions would have been TP within the five-second
window; TP loss is measured separately above. Nor are all removed FP flags
tracker ID switches.

The `is_new` timings sum only the body of `NewContactDetector.is_new`
via an instance proxy on each source: event calls including early-return
non-OPENs, divided by **all replayed frames**, including frames with zero
events. They exclude proxy bookkeeping, tracker, aggregator and detector cost;
they are not a whole-engine on/off difference. Timer/call overhead is not
subtracted. Per-frame history snapshot/pruning is timed separately as
`history_us_per_frame` in the assets and table; it is not hidden in the detector
inference timings.

| Final replay | Frames | is_new calls | is_new µs/frame | History snapshot/prune µs/frame |
|---|---:|---:|---:|---:|
| dev YOLO | 2,382 | 354 | 3.983 | 109.751 |
| dev oracle-d0.3 | 2,382 | 125 | 1.196 | 37.784 |
| held-out YOLO | 5,830 | 452 | 5.698 | 279.521 |
| held-out oracle-d0.3 | 5,830 | 209 | 0.584 | 93.156 |

CPU: Intel Xeon virtual CPU, 4 threads per replay, CPU ONNX Runtime. Final
dev used CPUs 0–3; held-out used CPUs 4–7 concurrently. Final Go checks briefly
shared the dev CPU set; these are shared-VM
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
These thermal-gate rows are historical **opt-in** measurements. The final
default-off new-contact replay executes no thermal modality gate or proposer.

## Interpretation and limitations

* This is **birth signaling**, not lifetime object recall. An object first
  reported after 5 seconds is FP under this task even if correctly tracked.
  Recall can also be lost to ByteTrack/aggregator merging before any OPEN.
* The spatial novelty heuristic has no appearance/camera-motion compensation.
  Distinct objects entering a recently occupied region can be suppressed.
  This evaluation is not evidence that every suppression is a corrected ID
  switch. A higher filtered precision can come at lower birth recall.
* The detector now retains its own memory independently of aggregator eviction.
  The regression covers eviction and delayed confirmation, but these replay
  clips do not establish long-running memory/throughput behaviour or persistence
  across source restarts. No memory horizon was tuned.
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

## Historical integration checkpoint (#274, before review corrections)

Latest base is **origin/dev 5083be1**. Marine verification remains default
off; thermal fallback wraps its final base output when enabled. Marine
tracking/aggregation hooks are retained, forwarding non-marine observations
to the same existing algorithms; the thermal label/unknownness and new-contact
state stay intact. A dispatch bridge preserves per-source marine scan indices
through the new outer thermal wrapper, including two interleaved sources.

**No full MOT or IR quality replay was repeated for this default-off rebase.**
The tables and JSON fingerprints remain the full #271 live-path measurements
described above, with no thresholds selected or changed afterward. Source
inspection and integration tests establish unchanged default decision paths;
this is not a claim that a new full replay was measured at 5083be1. Per the
final integration brief, reran a quick 32-IR / 16-RGB real-engine latency
sanity check instead: mean thermal off/on **138.675 / 141.343 ms IR**,
**173.713 / 175.661 ms RGB**; RGB gate **2.235 ms mean**. Details and limitations
are in the companion report's final checkpoint section and JSON asset.

Final post-#274 checks: `pytest edgeml/tests -q` **696 passed in 8.56s**;
`make test` passed; Node **388 pass / 0 fail**; `make prod-gate`: **PASS**.
Full production gate output: `~/data/prod-gate-final.log`.

`make verify VERIFY_PYTHON=.venv/bin/python` was attempted on clean pre-#274
commit `366cc8a68ad8ff2db27aaa1edc341d9553930cb5`: 14/15 stages passed,
including race, vulnerabilities, package and protobuf regeneration. Image
building failed on `files.pythonhosted.org` timeout, not Docker Hub 429;
the exact failed sidecar build passed on retry. Original report:
`dist/verify/366cc8a68ad8-mjfewrdc/report.json`; retry log:
`~/data/newcontact-sidecar-image-retry.log`. This is **not** a passing full
verifier report for the final rebased commit; the final four requested
checks above were rerun after integration. No image/base/build config changes
or mirror substitutions were needed.

## Review correction checkpoint

Fresh full dev/test replays use the final default-off engine and corrected
new-contact implementation (pipeline fingerprint
`4f5f2dbd00d9b9d4c8bb3d4deefea18e223b53c0508afb5a7030e184967563fb`).
Thresholds were not retuned.
The comparison below is against the pre-review thermal-on assets; it combines
both new-contact correctness fixes and the thermal default-off safety change,
not an isolated ablation. Oracle never uses thermal, so its changes reflect
new-contact logic only. Birth denominators are unchanged.

| Split | Detector | All filtered P/R %, before → final | Mid-clip filtered P/R %, before → final |
|---|---|---|---|
| dev | YOLO | 19.61 / 45.45 → 22.45 / 50.00 | 16.33 / 40.00 → 19.15 / 45.00 |
| dev | oracle-d0.3 | 95.45 / 95.45 → 95.24 / 90.91 | 95.00 / 95.00 → 94.74 / 90.00 |
| held-out | YOLO | 14.49 / 52.63 → 16.95 / 52.63 | 7.81 / 35.71 → 9.26 / 35.71 |
| held-out | oracle-d0.3 | 94.44 / 89.47 → 94.44 / 89.47 | 92.31 / 85.71 → 92.31 / 85.71 |

Final checks after the last code edit: Python **701 passed in 7.95s**;
`make test` passed; Node **388 pass / 0 fail**; `make prod-gate`: **PASS**.
Full logs: `~/data/review277-optin-pytest.log`,
`~/data/review277-final-go.log`, `~/data/review277-final-node.log`,
`~/data/review277-final-prod-gate.log`.
Deployment/compose scan found **no** `--thermal-fallback` or
`thermal_fallback` opt-ins. The independent new-contact scorer remains on at
weight 1. Full `make verify` was not repeated for these review changes; the
historical verifier limitation above remains explicit.

### Bounded-history performance correction

The subsequent performance-only review fix walks trajectories backwards only
to the observation cutoff, rather than copying lifetime trajectories every
frame. Current-state lookups reverse-scan arrival order, skipping observations
later than `first_t_s`, then consume only the latest eligible arrival. This
does not assume sorted timestamps: captured-time rollbacks are accepted by
the live frame clock. Work stays window/confirmation-lag bounded when timestamps
are monotonic, rather than scanning the sighting's entire lifetime.
Copied detector history remains window-bounded (with the pending-lag anchor
described above). No aggregator trajectory or event semantics changed.

A 10,000-point regression retains only 31 inclusive one-second observations
for the 30-second window and reads only 32 trajectory points during snapshot.
Looking up time 9995 from the far end reads five points. Twenty full-scan
equivalence cases cover live and evicted states, old/current/future first
times and memory boundaries; existing moving-contact, pruning, pending-lag,
empty-state and source-isolation tests also pass.

Quality tables/assets are retained from the measured `6a7954a` replay based on
those equivalence tests; no full evaluation was rerun for this optimization.
The timing table above is **pre-optimization**, not a new measurement of this
bounded implementation. Checks before the rollback correction: **722 passed in 8.43s** and
`make prod-gate`: **PASS**. Logs: `~/data/review277-bounded-pytest.log` and
`~/data/review277-bounded-prod-gate.log`. Go/Node inputs are unchanged from the
earlier successful checks.

The rollback correction removes the binary search from `8392e97` rather than
adding a sortedness-dependent fallback. Its regression covers an arrival-order
trajectory with timestamps 0, 10, 12, 2, 11: a sighting first seen at time 3
must match the nearby observation at time 2, both live and after eviction.
The monotonic MOT quality results remain unchanged by construction and the
full-scan equivalence tests; no full evaluation or latency replay was repeated.
After the rollback correction: **723 passed in 8.24s**; `make prod-gate`:
**PASS**. Full logs: `~/data/review277-rollback-pytest.log` and
`~/data/review277-rollback-prod-gate.log`.
