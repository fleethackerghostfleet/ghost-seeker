# Live marine-mammal verification — calibration and held-out evaluation

## 0. Scope and operating point

Rebased onto `origin/dev` at `908cce2` (including PR #271), with the same frozen
thresholds. CPU YOLO11n + the sidecar's `LiveTiledDetector` + MobileCLIP-S1 verifier. No
training, new prompts, image relabeling, or test-driven threshold changes.
The dev-only optimum under **v3-negative FP ≤ 10/1000** is:

* `crop_thr = 0.9480436444282532`
* `scan_thr = 0.7265272736549377`

These are the shipped `MarineMammalConfig` defaults. Selection maximizes pooled
dev frame F1, subject to the v3-negative constraint, over all distinct observed
crop/scan scores ≥ the engine's 0.35 reporting threshold, plus 0.35 and 1.
Ties prefer higher F1, fewer pooled false positives, then higher thresholds.
The constraint is feasible: **9/1029 dev v3 negatives, 8.75 FP/1000**.
It is not a constraint on public hard negatives or a guarantee about held-out data.

## 1. Data and label provenance

| split | v3 negatives | public hard negatives | public positives | v3 positives | total |
|---|---:|---:|---:|---:|---:|
| dev, calibration only | 1029 | 109 | 92 | 44 | 1274 |
| held-out test | 1249 | 99 | 88 | 34 | 1470 |

Inputs are `~/data/whale_eval/split_dev.jsonl` and `split_test.jsonl`, unchanged.
Their SHA-256 values are respectively:

```text
1a44e9461820d0087e5afc9c850f020b25dcf79c38b336416a34e59b1dbd3d17
2c2a7a2b26b4dd4842be7e93db054b2e23394c5b9c12d3976ce408df11b5c688
```

* **v3 positives:** frames of marine-mammal clips where Gemini 2.5 labeled a
  visible mammal **and drew a box**. The dataset curator visually excluded
  `tarpon_offshore`, `chase_n_fish_sharky`, and `far_away_but_did_you_see` because
  Gemini hallucinated dolphins on empty sky/sea. Remaining mammals are often
  tiny/distant; these are noisy labels, not manually verified ground truth.
* **v3 negatives:** all selected frames of non-mammal datasets (SeaDronesSee,
  SeaShips7000, SMD visible onboard and open-sea/maritime videos), retained
  regardless of Gemini. **22** have `gemini_flag` set: **9 dev, 13 test**.
  They remain negatives, without relabeling or exclusion.
* **Public:** Wikimedia Commons images, retained only when Gemini agreed with
  the query class; split by SHA-1(title) parity. Evaluation-only, never trained
  on. Per-image license, author, source page and license URL are in
  `~/data/whale_eval/public/manifest.json`; split rows preserve license/page.
  No public image or dataset is distributed in this change.
* No v1 tables or training data were used. The held-out v3 positive source is
  `dolphin_from_far_distance_in_bay_of_bengal`. Its supplied subtype labels are
  23 whale, 10 dolphin and 1 blow; those labels were **not corrected**.

### Public licenses (398 evaluated images)

| license | images |
|---|---:|
| Public domain | 85 |
| CC0 | 16 |
| CC BY 2.0 / 2.0 de | 14 / 1 |
| CC BY 3.0 / 3.0 us | 12 / 4 |
| CC BY 4.0 | 14 |
| CC BY-SA 2.0 / 2.5 | 54 / 1 |
| CC BY-SA 3.0 / 3.0 igo | 27 / 1 |
| CC BY-SA 4.0 | 159 |

These are the supplied Commons license identifiers, not a new license grant.
Attribution and share-alike obligations remain per image. v3 license provenance
is `datasets_clean_v3` as referenced in the supplied rows; this experiment does
not establish a uniform redistribution license for those frames.

## 2. What is measured

`mm_eval.py --live` constructs `EdgemlEngine(engine='ort', threads=4)` with real
YOLO11n and MobileCLIP-S1 exports. It calls the engine's actual final detector:
YOLO → LiveTiledDetector → MarineMammalFusionDetector. It does not substitute
the old native-only eval detector, manually propose crops, or bypass the MM hook.
Frame prediction is ≥1 returned `marine mammal` detection with score ≥0.35,
matching the engine's frame-detection filter. Tracker/aggregator/RPC overhead and
sighting persistence are not measured; their wire behavior is covered by tests.

These are independent still-image snapshots, not contiguous live video. For
each image, only the MM frame counter is reset to zero, making the first-frame
scan eligible. The live tiler's rotating cursor/cost estimator persist. The
ordinary scan gate still requires **no final base detection ≥0.35**, including
non-candidate classes. Crop verification still sees all final tiled detections.
Default live streaming scans only every fifth frame; this report's recall and
latencies describe **scan-eligible cold-start snapshots**, not stride-5 video
recall or throughput. Dev scanned 797/1274 frames; 843 had candidate crops.
743 dev images received two extra YOLO tiles, 531 only the native pass.

Each inference writes all proposal scores, kind, subtype, box, maxima and timing
to a local JSONL cache. Joint threshold sweeps use that cache, without rerunning
models. Thresholds were written to `live_frozen.json` and code **before** opening
the held-out inference run. Test inference is run once; cached threshold replay
is checked against the actual emitted frame detections. No test tuning follows.
The initial held-out run was once before PR #271; a second run was explicitly
requested after rebasing. It uses the identical frozen thresholds, not a new
calibration. Dev is also rerun as a fixed-threshold regression check.

### PR #271 integration and open-vocabulary gating

No service conflict occurred on rebase. `EdgemlEngine` wraps the final tiled
detector in MM during construction. `analyze` calls that detector first and
appends open-vocabulary results afterward, outside the MM detector and tracker.
Thus MM cannot relabel an appended open-vocabulary result. An engine test checks
this order using an appended `bird` result and an MM-labelled tracked result.

Open-vocabulary inference is **request-driven**, not unconditional default image
embedding: `labels_from_wire` requires `open-vocab:` prefixes, and `analyze`
only calls `open_vocab.detect` when that list is nonempty. Default known labels
are closed labels. A whale directive compiles to closed `marine mammal`, so
`openvocab.Labels` does not add an open-vocabulary request either. This export
directory has no `text_model.onnx` / `tokenizer.json`; default cache-only mode
therefore also reports open-vocabulary unavailable. Rebased score caches record
`open_labels=[]` and capability availability. No nondefault prompt or torch
text fallback was injected into these default-path evaluations/latency tests.

For active nondefault open-vocabulary requests, the full frame may be embedded
again after MM; candidate crops have different padding and scan grids differ.
There is no cross-stage image-feature reuse. Adding a bounded per-frame feature
cache across the detector and service would be broader than this integration,
so it is not done here. The new service text cache does not replace the bundled
MM prompt JSON: MM initializes its prototypes once, before that cache exists.

The Go scoring regression compiles `look for whales` in edge-subtype mode and
checks boosts for `marine mammal`, `marine_mammal`, and `Marine Mammals` with a
`dolphin` subtype. The new whole-word matcher retains normalized equality;
`marine mammal` remains closed vocabulary. An unrelated boat is not boosted.

## 3. Frame results

| split / source | TP / positives | FP / negatives | precision | recall | F1 | FP/1000 negatives |
|---|---:|---:|---:|---:|---:|---:|
| dev, all | 116/136 | 16/1138 | 0.879 | 0.853 | 0.866 | 14.06 |
| dev, v3 | 34/44 | 9/1029 | 0.791 | 0.773 | 0.782 | 8.75 |
| dev, public | 82/92 | 7/109 | 0.921 | 0.891 | 0.906 | 64.22 |
| test, all | 108/122 | 67/1348 | 0.617 | 0.885 | 0.727 | 49.70 |
| test, v3 | 23/34 | 58/1249 | 0.284 | 0.676 | 0.400 | 46.44 |
| test, public | 85/88 | 9/99 | 0.904 | 0.966 | 0.934 | 90.91 |

**The dev FP constraint does not generalize to held-out v3 negatives.** Test
v3 has 46.44 FP/1000, not ≤10. Public hard negatives also remain problematic.
No post-test threshold adjustment was made.

Subtype recall means any mammal emitted on a positive frame with that supplied
subtype; correctly named subtype recall is also reported separately. A hit does
not require box overlap with a ground-truth mammal. Multiple detections count as
one frame prediction. FP/1000 denominators are negative frames, not all frames.

| supplied subtype | dev mammal TP / n (recall) | dev correctly named TP / n | test mammal TP / n (recall) | test correctly named TP / n |
|---|---:|---:|---:|---:|
| whale | 57/68 (0.838) | 40/68 (0.588) | 57/67 (0.851) | 37/67 (0.552) |
| blow | 9/9 (1.000) | 6/9 (0.667) | 8/8 (1.000) | 6/8 (0.750) |
| fluke | 30/32 (0.938) | 23/32 (0.719) | 26/26 (1.000) | 21/26 (0.808) |
| dolphin | 20/27 (0.741) | 20/27 (0.741) | 17/21 (0.810) | 15/21 (0.714) |

### Per-source counts

For positive-only sources the last column is recall; for negative-only sources
it is FP/1000. `video_` prefixes are omitted below for readability.

| split | source | hits / frames | recall or FP/1000 |
|---|---|---:|---:|
| dev | public:pos | 82/92 | 0.891 |
| dev | dolphins_pungo_ferry_bridge (positive) | 0/2 | 0.000 |
| dev | two_dolphins_near_the_coast_of_gibraltar (positive) | 7/10 | 0.700 |
| dev | whale_watching_in_northern_peru (positive) | 27/32 | 0.844 |
| dev | public:hardneg | 7/109 | 64.22 |
| dev | seadronessee | 1/150 | 6.67 |
| dev | seaships7000 | 0/150 | 0.00 |
| dev | smd_vis_onboard_sample | 2/40 | 50.00 |
| dev | 30_days_at_sea_timelapse | 0/150 | 0.00 |
| dev | argentina_to_alaska_cruise_bridge_timelapse | 0/139 | 0.00 |
| dev | cargo_ship_storm_first_hour | 0/150 | 0.00 |
| dev | cargo_ship_uncut_view | 3/150 | 20.00 |
| dev | sailing_pov_corfu_paxos | 3/60 | 50.00 |
| dev | sunrise_somewhere_in_the_ocean | 0/40 | 0.00 |
| test | public:pos | 85/88 | 0.966 |
| test | dolphin_from_far_distance_in_bay_of_bengal (positive) | 23/34 | 0.676 |
| test | public:hardneg | 9/99 | 90.91 |
| test | seadronessee | 0/200 | 0.00 |
| test | seaships7000 | 2/200 | 10.00 |
| test | smd_vis_onboard_sample | 1/40 | 25.00 |
| test | arriving_in_norfolk_usa_3hr_uncut | 2/300 | 6.67 |
| test | sailboat_on_the_horizon | 8/300 | 26.67 |
| test | ship_in_the_distance_ian | 0/32 | 0.00 |
| test | spotted_a_submarine | 45/146 | 308.22 |
| test | us_destroyer_pov_warship_crossing | 0/31 | 0.00 |

45 of 58 held-out v3 false positives occur in the submarine clip. This is a
source-level error concentration, not a visually established explanation of
which object or patch caused each hit.

## 4. CPU latency and live budget

| mode (50 real dev frames) | mean ms/frame | p95 ms/frame | mean CLIP crops/frame |
|---|---:|---:|---:|
| YOLO live baseline, MM off | 81.78 | 145.16 | 0.00 |
| +MM crop only | 161.97 | 311.57 | 1.82 |
| +MM scan only | 348.28 | 564.57 | 4.06 |
| +MM both | 436.62 | 611.42 | 5.72 |

Measured serially on 50 evenly spaced **dev** rows, four ORT threads and four
OpenCV threads, x86-64 Intel Xeon VM (8 exposed cores), ORT 1.23.2. Model loading
and JPEG decoding excluded; first detector call included, no warm-up discarded.
Baseline includes the real YOLO native + admitted tiles + person-in-water
heuristic, not a different detector. Other modes run that same stack plus crop
only, scan only, or both. All use the same image order with tiler cursor/cost
state reset per mode. These measurements are not RPC/end-to-end latency and
are not guaranteed fleet-device performance.

Defaults: MM enabled when MobileCLIP-S1 `--embed` is supplied; no embed means
logged skip, not a synthetic fallback. `--marine-mammal off` disables MM without
an environment variable. `--mm JSON` overrides the dataclass. Candidate minimum
0.05, top four crops, context padding 0.2; whole-frame plus overlapping 2×3 grid,
overlap 0.2; **eight total CLIP crops/frame**, crop proposals taking priority;
**scan_stride=5**, first frame eligible. The cap can truncate scan tiles. There
is no MM wall-clock deadline. Live tiling separately defaults to at most two
640px tiles above 1280×720 pixels, overlap 0.2, 3500ms soft budget and cost
margin 1.2. In-flight ONNX calls cannot be preempted.

## 5. Limitations

* Model-selected and curator-filtered labels, especially distant v3 mammals,
  are noisy; this is not an independently adjudicated benchmark.
* Adjacent v3 frames are correlated. Commons title-parity splits can place
  cropped/derived images of a scene in different splits. Frame-level F1 is not
  independent-event F1, and possible source duplication limits generalization.
* Public positives are often close, salient photographs, not shipboard video;
  public hard-negative error rates are reported separately, not hidden by the
  larger easy-negative pool.
* Frame hits do not validate localization, tracking, correct subtype, sustained
  sightings, or an operator alert. Ocean domain shift and scan gating can hide
  mammals whenever another usable object is detected.
* The dev FP budget is a point estimate, not a statistical bound. No assertion
  of fleet safety, all-weather recall or guaranteed 5fps CPU operation follows.
* No prompt, embedding or model weights were trained or adjusted in this step;
  calibration changed only the two thresholds.

## 6. Reproduction and artifacts

Run from the repo root using `~/venvs/edgeml/bin/python` (`$PY` below):

```sh
DATA=$HOME/data/whale_eval
PY=$HOME/venvs/edgeml/bin/python
$PY edgeml/tools/mm_eval.py --live --input "$DATA/split_dev.jsonl" --out "$DATA/live_dev_scores.jsonl"
$PY edgeml/tools/mm_calibrate.py --dev "$DATA/live_dev_scores.jsonl" --frozen "$DATA/live_frozen.json" --out "$DATA/live_dev_metrics.json"
# Freeze defaults in MarineMammalConfig before the next invocation. Test: once only.
$PY edgeml/tools/mm_eval.py --live --input "$DATA/split_test.jsonl" --out "$DATA/live_test_scores.jsonl"
$PY edgeml/tools/mm_calibrate.py --dev "$DATA/live_dev_scores.jsonl" --frozen "$DATA/live_frozen.json" --test "$DATA/live_test_scores.jsonl" --out "$DATA/live_metrics.json"
$PY edgeml/tools/mm_latency.py --input "$DATA/split_dev.jsonl" --out "$DATA/live_latency.json"
```

The PR #271 rerun uses the existing frozen file, with **no calibration command**:

```sh
$PY edgeml/tools/mm_eval.py --live --input "$DATA/split_dev.jsonl" --out "$DATA/rebase271_dev_scores.jsonl"
$PY edgeml/tools/mm_eval.py --live --input "$DATA/split_test.jsonl" --out "$DATA/rebase271_test_scores.jsonl"
$PY edgeml/tools/mm_calibrate.py --dev "$DATA/rebase271_dev_scores.jsonl" --frozen "$DATA/live_frozen.json" --test "$DATA/rebase271_test_scores.jsonl" --out "$DATA/rebase271_metrics.json"
$PY edgeml/tools/mm_latency.py --input "$DATA/split_dev.jsonl" --out "$DATA/rebase271_latency.json"
```

All 1274 rerun dev crop maxima, scan maxima and tile counts are identical to the
initial cache; predictions at the frozen thresholds are unchanged. The initial
dev cache's `emitted` field used the pre-calibration thresholds, so regression
comparison replays its raw scores with the frozen thresholds rather than
comparing that old field directly. All 1470 held-out crop maxima, scan maxima,
tile counts and emitted predictions are also identical. Every frozen-threshold
metric group, including subtype recall, is unchanged; the results tables above
therefore remain the measured rebased values. The latency table is replaced by
the rebased measurement; timing differences are shared-VM variation, not added
open-vocabulary image work (the default request gate is empty in every mode).

Exports: `~/models/D/yolo11n-640/model.onnx` SHA-256
`cf094973e1f946b2e86883120ddc455a0e33bb07f89096ac1d8bac14ac9833b6`;
`~/models/D/mobileclip-s1/model.onnx` SHA-256
`4f88eb30d8e4701e4338f2ac38a063ad406066c9fb55c2ddbcd9db757113e0f5`.
Text prompts/512-D bundled embeddings are unchanged. Raw images, inputs, scores,
frozen JSON and logs stay under `~/data/whale_eval`, not in git.

An eval metadata collision initially placed the predicted subtype in the input
`subtype` field. The tool now writes `predicted_subtype` separately, covered by
a regression test. Cached metadata was restored by path from the untouched
split files; raw pre-repair caches are retained as `live_*_scores.raw.jsonl`.
No model was rerun. Pooled detection scores, dev selection and test emissions
were unaffected; subtype tables above use the supplied labels.

## 7. Verification

```text
python -m pytest -q edgeml/tests:
640 passed, 3 skipped in 7.20s

make test:
exit 0 (go vet ./... && go test ./...)
ok  github.com/fleethackerghostfleet/ghost-seeker/web  (cached)

node --test web/*.test.cjs:
# tests 387
# pass 387
# fail 0

make prod-gate:
prod-gate: PASS
```

Full rebased logs: `~/data/whale_eval/rebase271_{pytest,make_test,node_test}.txt` and
`~/data/whale_eval/prod_gate.txt`. All four commands exited zero. Go 1.26.10
and Node 22.16.0 were installed under `~/tools` because neither runtime was
initially on this VM; their bin directories were added to the checks' PATH,
with `GOTOOLCHAIN=local`. No build/CI configuration was changed. No UI surface
changed and no visual verification was performed in this step.

The rebased checks include the Go whale-directive regression in both normal and
prod builds, and the Python engine ordering test with an appended open-vocabulary
result. No changes to `service.py` were necessary on this rebase.
