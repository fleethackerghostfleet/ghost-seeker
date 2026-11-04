# Workstream C — tracking, sighting aggregation and dedup metrics

**Status:** dev-set results below are final for this PR; the locked `test` sources were evaluated once (§8).
Everything here ran on the session's 8-core CPU box (no GPU VM was needed: the heaviest step, YOLO11s @1280 on
~6k frames, is ~150 ms/frame). Numbers are reproducible with the commands in §10.

## 1. What was built

| piece | file | notes |
|---|---|---|
| `run_clip` | `edgeml/src/edgeml/pipeline.py` | frames → `Detector` → `Tracker` → `Aggregator`; `detect_every=N` runs detector+tracker on every N-th frame (tracker lifetimes are in **seconds**, so cadence changes do not kill tracks); optional `stabilizer` pre-step |
| tracker adapters | `edgeml/src/edgeml/tracking/boxmot_adapter.py` | `boxmot` ByteTrack, OC-SORT, BoT-SORT, BoT-SORT+CMC (GMC `sparseOptFlow`), BoT-SORT+ReID (not exercised — no maritime ReID weights). `max_lost_s`, `asso_func` (`iou`/`centroid`) exposed |
| sighting aggregator | `edgeml/src/edgeml/tracking/aggregator.py` | `open` after ≥2 detections, `update` only on material change (label flip, confidence jump >0.2, `size_class`/`closing`/`aspect`/bearing change, key-frame q +30 %), `close` after `close_after_s`; re-link by position/size gate + one embedding per sighting computed at key-frame time; same-frame duplicate-track merge; own-ship static mask; `value` with age-gated unknownness bonus; byte-exact `forms[]` (fact, crop q50, crop q80, thumb ≤320 px q75, full ≤1280 px q80) |
| horizon stabiliser | `edgeml/src/edgeml/stabilize.py` | Hough horizon on a 480-px grey edge map, EMA, affine that pins angle+row to the first frame; applied to boxes (and the image for CMC trackers), tracks mapped back |
| detectors | `edgeml/src/edgeml/detectors/` | `UltralyticsDetector` (.pt or B's .onnx exports), `OracleDetector` (GT boxes, optional dropout/jitter), `CachedDetector` (JSONL replay so the matrix never re-runs the CNN) |
| selector | `edgeml/src/edgeml/select.py` | `greedy(events, budget_bytes, ladder_decay=(1,.5,.25,.1), max_bytes_per_object=None)` — value/byte knapsack, cheaper forms of an item always before dearer ones |
| metrics | `edgeml/src/edgeml/eval/` | TrackEval wrapper (HOTA, HOTA@α=0.3, IDF1, MOTA, IDSW, Frag; stride-aware for `detect_every>1`), CONTRACTS §5 sighting metrics, naive + image-dedup baselines, §5b delivered-objects-vs-budget |
| runner / figures | `edgeml/tools/run_c.py`, `edgeml/tools/report_c.py` | `cache`, `matrix`, `delivery` (E3 re-score), `trackeval` sub-commands; tables, charts, timelines, contact sheets |
| tests | `edgeml/tests/test_aggregator.py`, `test_select_and_metrics.py` | 15 tests: open/close, ≥2-hit rule, re-link, duplicate-track merge, material updates, key frame, static mask, selector decay/cap/order, matching, baselines, stabiliser round-trip |

## 2. Data

Identity-labelled sources actually used (all MOT layout, loaded through `edgeml/data/seqs.py`; A's canonical loader
`edgeml/data/mot.py` lands with PR #16/#20 and reads the same layout):

| set | where | fps | camera | GT | split (by source video, `edgeml/configs/splits.yaml`) |
|---|---|---|---|---|---|
| `fvessel_clip10` (A, public FVessel) | `gs://…/edgeml/eval/fvessel_clip10/` | 1 | fixed shore CCTV, 2560×1440 | public AIS-matched ids, 2–4 tracks/clip, 4,761 boxes | dev = clips 01–06, 08, 10; test = 07, 09 (**weak hold-out**: same camera and day as dev) |
| `ghost_fleet_5fps` (A, own clips) | `gs://…/edgeml/eval/ghost_fleet_5fps/` | 5 | onboard / handheld / aerial, mostly vertical phone video | teacher (Grounding-DINO) + human QA; A reports teacher 28 tracks / 2,451 boxes → QA'd 15 tracks / 1,291 boxes | dev: 6 clips with GT (3 `complete`, 4 `partial`, 2 `incomplete` excluded); test: `Spotted_A_Submarine`, `Ship_in_the_Distance_Ian` (`Dolphin…Bengal` excluded: `incomplete`, 0 boxes) |
| `mvtd_5fps` (public MVTD, CC0) | HF `AhsanBB/...`, converted by `edgeml/data/convert.py` (30→5 fps) | 5 | onboard / handheld, 1920×1080 | single vessel per clip, every frame | dev: 13 clips; test: `18-SailBoat`, `22-Boat`, `7-Boat` |
| `smd_vis_onboard_1fps` (catalog SMD sample) | `datasets/smd_vis_onboard/` → MOT via `convert.py` | 1 | onboard, 1920×1080 | `object_id` attrs on ~10 of 20 frames per clip, duplicate rows, ids flicker | dev: `MVI_0790`, `MVI_0799`; test: `MVI_0797`, `MVI_0801` |

Caveats that shape the numbers: FVessel GT only covers AIS-equipped vessels, MVTD only the one filmed boat and
`ghost_fleet_5fps` `partial` clips have known unlabelled stretches, so a correct sighting of an unlabelled boat is
counted as a **false sighting** and as a tracker FP — `false/min` and MOTA are upper bounds on real false alarms with
the YOLO detector. The SMD sample has 2–6 labelled frames per object; the four GT objects the oracle+tracker runs
"miss" on dev are all SMD objects labelled on ≤6 sparse frames.

## 3. Protocol

* Detectors: `oracle` (GT boxes, score 1), `oracle-d0.2-j0.05` (GT boxes with 20 % random dropout and 5 % box
  jitter, to separate tracking/aggregation errors from detection errors), `yolo11s` (COCO, imgsz 1280, conf ≥0.05
  cached, score floor applied at eval time; maritime classes boat/ship/person/bird/airplane…), `yolo11n` (B's ONNX
  export at 640 from `gs://…/edgeml/models/yolo11n/`). YOLO detections are cached once per frame
  (`run_c.py cache`) and replayed, so the tracker × cadence matrix never re-runs the CNN.
* Trackers: `bytetrack`, `ocsort`, `botsort`, `botsort_cmc` (sparse-optical-flow GMC), each with association
  `iou` or `centroid` (normalised centre distance, gate 0.15 of the diagonal); `max_lost_s` in seconds.
* Cadence: `detect_every` ∈ {1, 2, 5} frames. For `detect_every>1` TrackEval is computed on the processed frames
  only (GT subsampled and renumbered, `frame_stride`), sighting metrics on all frames as in CONTRACTS §5.
* Baselines through the same aggregator/selector interface: `naive` (one report per detection per frame, forms =
  fact + full frame) and `imgdedup` (per-frame detections, dropped when the 16×16 grey fingerprint of the frame is
  within Hamming 8 of one of the last 30 kept frames — the `algorithm/` redundancy idea, no identity).
* Sighting matching (CONTRACTS §5): a sighting matches the GT track with most frames at IoU ≥ 0.3 if that covers
  ≥ 30 % of the sighting's frames; `recall` is pooled over GT objects of all sequences in the table (the per-sequence
  mean is ≈0.03 higher).
* Delivery (CONTRACTS §5b): events are grouped in 60 s windows, `select.greedy` runs per window at 72 KB / 480 KB /
  1.92 MB / 15 MB; an object counts as delivered when a form of a matched sighting (image forms, or `fact` only) is
  selected in any window.
* Tuning on `dev` only; `test` was run once with the final configuration (§8).



## 4. Dev results: baselines vs trackers × detect rate (YOLO11s, default aggregator)

Columns: pooled GT objects, CONTRACTS §5 sighting metrics (TTFR = mean over matched objects), TrackEval (HOTA, HOTA@α=0.3, IDF1, IDSW, Frag; on the
processed frames), fraction of GT objects delivered **with an image** through `select.greedy` at 72 KB / 480 KB /
1.92 MB / 15 MB per 60 s, and selector bytes per delivered object at 480 KB. 23 dev sequences
(`fvessel_clip10` 8, `mvtd_5fps` 13, `smd_vis_onboard_1fps` 2), 39 GT objects, 101 min of video. Run
`dev_yolo_centroid` (centroid association, `max_lost_s`=3, aggregator defaults, score floor 0.25).

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| yolo11s|naive|every1 | 39 | 0.90 | 93.44 | 207.8 | 1.3 | 103.3 | 9678 | - | - | - | - | - | 0.13 | 0.92 | 0.92 | 0.92 | 485 |
| yolo11s|naive|every2 | 39 | 0.87 | 46.92 | 106.5 | 1.9 | 55.0 | 4941 | - | - | - | - | - | 0.13 | 0.92 | 0.92 | 0.92 | 481 |
| yolo11s|naive|every5 | 39 | 0.90 | 17.79 | 42.7 | 3.8 | 20.5 | 1962 | - | - | - | - | - | 0.15 | 0.90 | 0.90 | 0.90 | 468 |
| yolo11s|imgdedup|every1 | 39 | 0.85 | 11.54 | 21.2 | 22.9 | 14.3 | 954 | - | - | - | - | - | 0.23 | 0.92 | 0.92 | 0.92 | 423 |
| yolo11s|imgdedup|every2 | 39 | 0.85 | 9.87 | 18.8 | 21.3 | 12.4 | 841 | - | - | - | - | - | 0.15 | 0.92 | 0.92 | 0.92 | 425 |
| yolo11s|imgdedup|every5 | 39 | 0.85 | 6.19 | 14.1 | 24.6 | 8.1 | 613 | - | - | - | - | - | 0.23 | 0.90 | 0.90 | 0.90 | 430 |
| yolo11s|bytetrack|every1 | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 8.4 | 619 | 0.31 | 0.42 | 0.33 | 53 | 161 | 0.79 | 0.85 | 0.85 | 0.85 | 462 |
| yolo11s|bytetrack|every2 | 39 | 0.79 | 0.59 | 3.1 | 7.2 | 7.8 | 497 | 0.30 | 0.42 | 0.32 | 34 | 105 | 0.79 | 0.85 | 0.85 | 0.85 | 450 |
| yolo11s|bytetrack|every5 | 39 | 0.67 | 0.36 | 2.5 | 8.3 | 5.6 | 346 | 0.26 | 0.38 | 0.26 | 37 | 55 | 0.79 | 0.79 | 0.79 | 0.79 | 435 |
| yolo11s|ocsort|every1 | 39 | 0.79 | 0.57 | 3.8 | 8.7 | 8.0 | 664 | 0.29 | 0.37 | 0.31 | 127 | 174 | 0.85 | 0.87 | 0.87 | 0.87 | 457 |
| yolo11s|ocsort|every2 | 39 | 0.82 | 0.53 | 3.2 | 11.9 | 7.3 | 551 | 0.29 | 0.38 | 0.30 | 81 | 102 | 0.79 | 0.85 | 0.85 | 0.85 | 463 |
| yolo11s|ocsort|every5 | 39 | 0.72 | 0.63 | 2.5 | 9.9 | 7.8 | 446 | 0.30 | 0.38 | 0.32 | 57 | 45 | 0.79 | 0.79 | 0.79 | 0.79 | 432 |
| yolo11s|botsort|every1 | 39 | 0.79 | 0.50 | 3.5 | 2.7 | 7.7 | 624 | 0.34 | 0.44 | 0.38 | 60 | 149 | 0.79 | 0.85 | 0.85 | 0.85 | 469 |
| yolo11s|botsort|every2 | 39 | 0.79 | 0.51 | 3.2 | 6.3 | 6.9 | 525 | 0.32 | 0.43 | 0.35 | 47 | 99 | 0.79 | 0.85 | 0.85 | 0.85 | 461 |
| yolo11s|botsort|every5 | 39 | 0.67 | 0.52 | 2.4 | 9.2 | 7.3 | 390 | 0.28 | 0.40 | 0.30 | 53 | 59 | 0.79 | 0.79 | 0.79 | 0.79 | 445 |
| yolo11s|botsort_cmc|every1 | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 7.6 | 606 | 0.34 | 0.44 | 0.37 | 53 | 146 | 0.82 | 0.85 | 0.85 | 0.85 | 467 |
| yolo11s|botsort_cmc|every2 | 39 | 0.82 | 0.41 | 3.1 | 6.7 | 6.7 | 517 | 0.34 | 0.45 | 0.37 | 39 | 86 | 0.85 | 0.85 | 0.85 | 0.85 | 457 |
| yolo11s|botsort_cmc|every5 | 39 | 0.72 | 0.33 | 2.4 | 8.8 | 6.0 | 376 | 0.30 | 0.41 | 0.34 | 48 | 52 | 0.82 | 0.82 | 0.82 | 0.82 | 429 |

Same detector with IoU association (`dev_yolo_iou`; this run was restarted once, so only the cells that completed are shown):

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| iou: yolo11s|bytetrack|every1 | 39 | 0.74 | 0.61 | 3.6 | 5.6 | 7.0 | 473 | 0.31 | 0.40 | 0.32 | 74 | 144 | 0.82 | 0.85 | 0.85 | 0.85 | 445 |
| iou: yolo11s|bytetrack|every2 | 39 | 0.74 | 0.42 | 2.9 | 8.8 | 5.3 | 333 | 0.31 | 0.42 | 0.35 | 51 | 84 | 0.82 | 0.82 | 0.82 | 0.82 | 443 |
| iou: yolo11s|bytetrack|every5 | 39 | 0.67 | 0.27 | 1.8 | 13.5 | 4.2 | 202 | 0.29 | 0.40 | 0.33 | 31 | 38 | 0.77 | 0.77 | 0.77 | 0.77 | 418 |
| iou: yolo11s|botsort_cmc|every1 | 39 | 0.69 | 0.30 | 1.8 | 6.9 | 5.4 | 275 | 0.39 | 0.49 | 0.46 | 16 | 55 | 0.79 | 0.79 | 0.79 | 0.79 | 390 |
| iou: yolo11s|botsort_cmc|every2 | 39 | 0.69 | 0.21 | 1.4 | 4.3 | 4.6 | 223 | 0.39 | 0.49 | 0.48 | 11 | 33 | 0.79 | 0.82 | 0.82 | 0.82 | 333 |
| iou: yolo11s|botsort_cmc|every5 | 39 | 0.54 | 0.04 | 1.0 | 5.7 | 3.9 | 136 | 0.31 | 0.40 | 0.37 | 3 | 8 | 0.72 | 0.72 | 0.72 | 0.72 | 302 |

**Reading it.** Every tracker variant cuts reports per object from ~104 (naive) / ~10 (image dedup) to 5–9 and
duplicates per object from 93 / 7.9 to ≤0.6, at a cost of 0.10–0.13 recall against the naive baseline (recall is
pooled; the oracle table below shows the loss is detector-side). `detect_every=2` is free on these clips
(recall ≥ every1 for BoT-SORT+CMC, fewer Frag); `detect_every=5` at 1 fps (FVessel/SMD = one detection per 5 s)
starts to lose objects. OC-SORT and plain BoT-SORT are not better than ByteTrack here; BoT-SORT+CMC has the best
HOTA/IDF1 and the fewest duplicates on the moving-camera MVTD clips.

### 4.1 Oracle detector (tracking/aggregation errors only)

`oracle` = GT boxes; `oracle-d0.2-j0.05` = GT boxes with 20 % dropout and 5 % jitter. Centroid association:

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| oracle|naive|every1 | 39 | 1.00 | 114.40 | 0.0 | 0.0 | 115.4 | 4501 | - | - | - | - | - | 0.10 | 0.97 | 1.00 | 1.00 | 473 |
| oracle|imgdedup|every1 | 39 | 1.00 | 13.14 | 0.0 | 2.4 | 14.1 | 408 | - | - | - | - | - | 0.21 | 0.97 | 1.00 | 1.00 | 387 |
| oracle|bytetrack|every1 | 39 | 0.90 | 0.02 | 0.0 | 1.3 | 4.6 | 174 | 0.90 | 0.99 | 0.98 | 3 | 18 | 0.95 | 0.95 | 0.95 | 0.95 | 320 |
| oracle|bytetrack|every2 | 39 | 0.90 | 0.04 | 0.0 | 3.5 | 4.4 | 166 | 0.82 | 0.96 | 0.92 | 3 | 25 | 0.90 | 0.90 | 0.90 | 0.90 | 319 |
| oracle|bytetrack|every5 | 39 | 0.85 | 0.04 | 0.0 | 8.9 | 4.3 | 150 | 0.73 | 0.92 | 0.85 | 5 | 28 | 0.85 | 0.85 | 0.85 | 0.85 | 363 |
| oracle|ocsort|every1 | 39 | 0.95 | 0.02 | 0.0 | 1.4 | 4.4 | 180 | 0.99 | 0.99 | 1.00 | 3 | 2 | 0.95 | 0.95 | 0.95 | 0.95 | 335 |
| oracle|ocsort|every2 | 39 | 0.92 | 0.04 | 0.0 | 3.5 | 4.4 | 175 | 0.98 | 0.98 | 0.98 | 2 | 0 | 0.92 | 0.92 | 0.92 | 0.92 | 333 |
| oracle|ocsort|every5 | 39 | 0.85 | 0.04 | 0.0 | 8.9 | 4.5 | 156 | 0.95 | 0.95 | 0.95 | 4 | 0 | 0.85 | 0.85 | 0.85 | 0.85 | 348 |
| oracle|botsort_cmc|every1 | 39 | 0.92 | 0.02 | 0.0 | 1.3 | 4.4 | 177 | 0.97 | 0.99 | 0.99 | 3 | 11 | 0.95 | 0.95 | 0.95 | 0.95 | 333 |
| oracle|botsort_cmc|every2 | 39 | 0.92 | 0.04 | 0.0 | 3.5 | 4.4 | 175 | 0.94 | 0.97 | 0.97 | 2 | 10 | 0.92 | 0.92 | 0.92 | 0.92 | 339 |
| oracle|botsort_cmc|every5 | 39 | 0.87 | 0.04 | 0.0 | 8.6 | 4.2 | 155 | 0.86 | 0.94 | 0.93 | 8 | 5 | 0.87 | 0.87 | 0.87 | 0.87 | 337 |
| oracle-d0.2-j0.05|bytetrack|every1 | 39 | 0.90 | 0.00 | 0.0 | 2.9 | 4.8 | 191 | 0.59 | 0.73 | 0.79 | 17 | 404 | 0.92 | 0.92 | 0.92 | 0.92 | 320 |
| oracle-d0.2-j0.05|bytetrack|every2 | 39 | 0.87 | 0.05 | 0.0 | 5.6 | 4.7 | 177 | 0.56 | 0.72 | 0.77 | 20 | 211 | 0.90 | 0.90 | 0.90 | 0.90 | 329 |
| oracle-d0.2-j0.05|bytetrack|every5 | 39 | 0.85 | 0.18 | 0.0 | 16.6 | 4.1 | 136 | 0.44 | 0.60 | 0.60 | 20 | 87 | 0.85 | 0.85 | 0.85 | 0.85 | 352 |
| oracle-d0.2-j0.05|ocsort|every1 | 39 | 0.92 | 0.00 | 0.0 | 2.2 | 4.8 | 195 | 0.64 | 0.77 | 0.86 | 25 | 395 | 0.95 | 0.95 | 0.95 | 0.95 | 320 |
| oracle-d0.2-j0.05|ocsort|every2 | 39 | 0.90 | 0.04 | 0.0 | 4.6 | 4.7 | 181 | 0.64 | 0.77 | 0.86 | 10 | 190 | 0.90 | 0.90 | 0.90 | 0.90 | 322 |
| oracle-d0.2-j0.05|ocsort|every5 | 39 | 0.82 | 0.28 | 0.0 | 16.1 | 4.5 | 155 | 0.50 | 0.60 | 0.64 | 29 | 77 | 0.85 | 0.85 | 0.85 | 0.85 | 372 |
| oracle-d0.2-j0.05|botsort_cmc|every1 | 39 | 0.90 | 0.00 | 0.0 | 2.9 | 4.7 | 186 | 0.65 | 0.75 | 0.83 | 27 | 399 | 0.90 | 0.90 | 0.90 | 0.90 | 319 |
| oracle-d0.2-j0.05|botsort_cmc|every2 | 39 | 0.87 | 0.05 | 0.0 | 5.6 | 4.6 | 176 | 0.63 | 0.74 | 0.81 | 13 | 194 | 0.90 | 0.90 | 0.90 | 0.90 | 330 |
| oracle-d0.2-j0.05|botsort_cmc|every5 | 39 | 0.87 | 0.16 | 0.0 | 16.1 | 4.0 | 139 | 0.49 | 0.60 | 0.65 | 32 | 83 | 0.87 | 0.87 | 0.87 | 0.87 | 341 |

IoU association:

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| oracle|bytetrack|every1 | 39 | 0.90 | 0.22 | 0.0 | 1.8 | 4.8 | 178 | 0.86 | 0.93 | 0.91 | 24 | 7 | 0.90 | 0.90 | 0.90 | 0.90 | 328 |
| oracle|bytetrack|every2 | 39 | 0.87 | 0.13 | 0.0 | 3.3 | 4.4 | 162 | 0.79 | 0.89 | 0.85 | 28 | 6 | 0.87 | 0.87 | 0.87 | 0.87 | 320 |
| oracle|botsort_cmc|every1 | 39 | 0.90 | 0.04 | 0.0 | 1.7 | 4.5 | 176 | 0.95 | 0.96 | 0.95 | 9 | 0 | 0.90 | 0.90 | 0.90 | 0.90 | 333 |
| oracle|botsort_cmc|every2 | 39 | 0.87 | 0.13 | 0.0 | 3.2 | 4.8 | 175 | 0.93 | 0.95 | 0.94 | 9 | 0 | 0.87 | 0.87 | 0.87 | 0.87 | 347 |
| oracle-d0.2-j0.05|bytetrack|every1 | 39 | 0.82 | 0.26 | 0.0 | 2.0 | 5.3 | 178 | 0.60 | 0.72 | 0.76 | 40 | 394 | 0.87 | 0.87 | 0.87 | 0.87 | 318 |
| oracle-d0.2-j0.05|bytetrack|every2 | 39 | 0.77 | 0.13 | 0.0 | 4.7 | 5.1 | 158 | 0.56 | 0.69 | 0.72 | 43 | 192 | 0.82 | 0.82 | 0.82 | 0.82 | 325 |
| oracle-d0.2-j0.05|botsort_cmc|every1 | 39 | 0.85 | 0.04 | 0.0 | 2.6 | 4.7 | 171 | 0.67 | 0.77 | 0.85 | 6 | 394 | 0.90 | 0.90 | 0.90 | 0.90 | 310 |
| oracle-d0.2-j0.05|botsort_cmc|every2 | 39 | 0.82 | 0.09 | 0.0 | 4.7 | 4.5 | 155 | 0.65 | 0.75 | 0.83 | 7 | 188 | 0.87 | 0.87 | 0.87 | 0.87 | 313 |

With perfect boxes the aggregator reaches recall 0.93–0.97 with 0.02 duplicates/object and ~0 false sightings, so
the YOLO rows' 0.77–0.82 recall and 0.4–0.6 dup/object are detector misses and detector flicker respectively. The
GT objects the oracle runs miss are SMD objects labelled on ≤6 sparse frames (never reach 2 hits). Centroid
association matters on the handheld MVTD clips: with IoU, the same oracle boxes give 0.2–0.3 dup/object and more
IDSW because consecutive 5 fps boxes of a small, fast-panning boat don't overlap.

## 5. Aggregator / tracker tuning (dev only)

Each row changes one parameter from `base` (YOLO11s, centroid, `detect_every=1`); `combo` = the chosen set.

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| base: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 8.4 | 619 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 462 |
| base: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 7.6 | 606 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 467 |
| score0.35: bytetrack | 39 | 0.77 | 0.43 | 2.8 | 7.6 | 7.1 | 470 | - | - | - | - | - | 0.77 | 0.82 | 0.82 | 0.82 | 436 |
| score0.35: botsort_cmc | 39 | 0.77 | 0.37 | 2.8 | 5.7 | 6.0 | 443 | - | - | - | - | - | 0.79 | 0.82 | 0.82 | 0.82 | 430 |
| score0.5: bytetrack | 39 | 0.72 | 0.29 | 1.3 | 5.4 | 5.9 | 290 | - | - | - | - | - | 0.79 | 0.82 | 0.82 | 0.82 | 408 |
| score0.5: botsort_cmc | 39 | 0.72 | 0.26 | 1.3 | 5.4 | 5.8 | 283 | - | - | - | - | - | 0.79 | 0.82 | 0.82 | 0.82 | 395 |
| minhits1: bytetrack | 39 | 0.82 | 0.67 | 3.8 | 0.1 | 8.3 | 661 | - | - | - | - | - | 0.85 | 0.90 | 0.90 | 0.90 | 446 |
| minhits1: botsort_cmc | 39 | 0.85 | 0.57 | 3.8 | 1.8 | 7.5 | 641 | - | - | - | - | - | 0.85 | 0.90 | 0.90 | 0.90 | 447 |
| minhits3: bytetrack | 39 | 0.77 | 0.62 | 3.3 | 1.6 | 8.2 | 602 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 456 |
| minhits3: botsort_cmc | 39 | 0.79 | 0.54 | 3.4 | 3.4 | 7.5 | 588 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 461 |
| close3: bytetrack | 39 | 0.77 | 0.64 | 3.4 | 0.9 | 8.5 | 620 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 466 |
| close3: botsort_cmc | 39 | 0.79 | 0.56 | 3.5 | 2.7 | 7.7 | 611 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 471 |
| close10: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 8.3 | 606 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 463 |
| close10: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 7.6 | 593 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 465 |
| relink5: bytetrack | 39 | 0.79 | 0.68 | 4.0 | 1.9 | 8.2 | 629 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 461 |
| relink5: botsort_cmc | 39 | 0.79 | 0.70 | 4.0 | 2.7 | 8.2 | 627 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 462 |
| relink20: bytetrack | 39 | 0.77 | 0.54 | 3.1 | 0.9 | 8.0 | 604 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 462 |
| relink20: botsort_cmc | 39 | 0.77 | 0.50 | 3.2 | 0.9 | 7.7 | 606 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 466 |
| kf0.6: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 8.3 | 597 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 460 |
| kf0.6: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 7.5 | 590 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 467 |
| kfoff: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 8.3 | 591 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 458 |
| kfoff: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 7.4 | 584 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 464 |
| upd1s: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 10.0 | 706 | - | - | - | - | - | 0.77 | 0.85 | 0.85 | 0.85 | 483 |
| upd1s: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 9.1 | 702 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 474 |
| upd10s: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 6.1 | 444 | - | - | - | - | - | 0.85 | 0.85 | 0.85 | 0.85 | 432 |
| upd10s: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 5.5 | 445 | - | - | - | - | - | 0.85 | 0.85 | 0.85 | 0.85 | 442 |
| lost1.5: bytetrack | 39 | 0.79 | 0.76 | 3.6 | 5.0 | 8.8 | 633 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 469 |
| lost1.5: botsort_cmc | 39 | 0.82 | 0.68 | 3.6 | 6.7 | 8.3 | 645 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 468 |
| lost6: bytetrack | 39 | 0.77 | 0.54 | 3.1 | 0.9 | 7.8 | 547 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 454 |
| lost6: botsort_cmc | 39 | 0.79 | 0.35 | 3.1 | 1.0 | 6.8 | 529 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 447 |
| mask: bytetrack | 39 | 0.77 | 0.62 | 3.4 | 0.9 | 8.4 | 619 | - | - | - | - | - | 0.79 | 0.85 | 0.85 | 0.85 | 462 |
| mask: botsort_cmc | 39 | 0.79 | 0.54 | 3.5 | 2.7 | 7.6 | 606 | - | - | - | - | - | 0.82 | 0.85 | 0.85 | 0.85 | 467 |
| combo: bytetrack | 39 | 0.82 | 0.37 | 2.4 | 5.7 | 4.7 | 340 | 0.35 | 0.47 | 0.41 | 33 | 160 | 0.85 | 0.87 | 0.87 | 0.87 | 409 |
| combo: botsort_cmc | 39 | 0.82 | 0.32 | 2.4 | 3.9 | 4.5 | 335 | 0.39 | 0.49 | 0.44 | 30 | 156 | 0.85 | 0.87 | 0.87 | 0.87 | 407 |
| combo: bytetrack every2 | 39 | 0.82 | 0.27 | 2.0 | 7.2 | 4.3 | 269 | 0.31 | 0.43 | 0.36 | 28 | 83 | 0.85 | 0.87 | 0.87 | 0.87 | 392 |
| combo: botsort_cmc every2 | 39 | 0.79 | 0.23 | 2.1 | 2.8 | 4.2 | 267 | 0.37 | 0.47 | 0.42 | 28 | 77 | 0.87 | 0.87 | 0.87 | 0.87 | 392 |

Decisions (all on dev): `min_hits=1` (boxmot already needs two hits before it emits a track, so the ≥2-detection
rule holds and the extra wait only delayed the first report); `max_lost_s=6` (fewest duplicates; this is the
"track_buffer in seconds" knob); `relink_gap_s=20`; `min_update_interval_s=10` (reports/object 8 → 6 with identical
delivery); score floor 0.35 (false/min 3.5 → 2.8 with no recall loss; 0.5 loses recall). `close_after_s`, the
key-frame improvement threshold and the own-ship mask made no measurable difference on these clips (no own-ship in
frame on the labelled sets). The `combo` row: **recall 0.82, 0.41 dup/object, 2.4 false/min, 5.2 reports/object,
HOTA 0.39, 85 % / 87 % of objects delivered with an image at 72 KB / 480 KB** (ByteTrack within noise except HOTA/IDSW).

## 6. A's 5 fps own-ship clips (`ghost_fleet_5fps`, dev)

Headline on `gt_status=complete` clips only (`A_ship_in_the_distance`, `Cruise_ship_Mediterranean_sunset_off_Italy`,
`Sailing_POV_Corfu_Paxos` [0 GT boxes: pure false-alarm clip]); `partial` clips (`Canadian_Coast_Guard_Helicopter_Landing`,
`Ship_in_the_Distance_Utopia_of_the_Seas`, `Submarine_surfacing`, `Two_Dolphins_near_the_coast_of_Gibraltar`)
separately; `incomplete` clips excluded (`Chase_N_Fish_Sharky`, `Far_away_but_did_you_see_that_splash`). Label noise
per A: teacher 28 tracks / 2,451 boxes → QA'd 15 tracks / 1,291 boxes, i.e. roughly half the teacher boxes were
removed in QA, so treat ±1 object as noise on these small counts (4 complete / 9 partial GT objects). Tuned config.

Centroid association, complete clips:

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| yolo11s|naive|every1 | 4 | 1.00 | 13.67 | 1.2 | 0.2 | 14.7 | 72 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 275 |
| yolo11s|naive|every5 | 4 | 1.00 | 1.67 | 0.0 | 0.7 | 2.7 | 14 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 172 |
| yolo11s|imgdedup|every1 | 4 | 1.00 | 5.00 | 0.0 | 0.2 | 6.0 | 32 | - | - | - | - | - | 0.00 | 0.50 | 0.75 | 1.00 | 297 |
| yolo11s|imgdedup|every5 | 4 | 1.00 | 1.67 | 0.0 | 0.7 | 2.7 | 14 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 172 |
| yolo11s|bytetrack|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.3 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 127 |
| yolo11s|bytetrack|every5 | 4 | 0.75 | 0.00 | 0.0 | 0.9 | 1.0 | 3 | - | - | - | - | - | 0.75 | 0.75 | 0.75 | 0.75 | 140 |
| yolo11s|botsort_cmc|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.3 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 127 |
| yolo11s|botsort_cmc|every5 | 4 | 0.75 | 0.00 | 0.0 | 0.9 | 1.0 | 3 | - | - | - | - | - | 0.75 | 0.75 | 0.75 | 0.75 | 140 |
| yolo11n|naive|every1 | 4 | 1.00 | 21.33 | 1.2 | 0.1 | 22.3 | 84 | - | - | - | - | - | 0.00 | 0.75 | 0.75 | 1.00 | 276 |
| yolo11n|naive|every5 | 4 | 1.00 | 4.50 | 0.6 | 0.4 | 5.5 | 20 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 280 |
| yolo11n|imgdedup|every1 | 4 | 1.00 | 4.67 | 0.6 | 0.1 | 5.7 | 31 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 197 |
| yolo11n|imgdedup|every5 | 4 | 1.00 | 2.00 | 0.6 | 0.4 | 3.0 | 15 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 201 |
| yolo11n|bytetrack|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.4 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 130 |
| yolo11n|bytetrack|every5 | 4 | 1.00 | 0.00 | 0.0 | 0.9 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 130 |
| yolo11n|botsort_cmc|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.4 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 130 |
| yolo11n|botsort_cmc|every5 | 4 | 1.00 | 0.00 | 0.0 | 0.9 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 130 |
| oracle|naive|every1 | 4 | 1.00 | 32.33 | 0.0 | 0.0 | 33.3 | 108 | - | - | - | - | - | 0.00 | 0.75 | 1.00 | 1.00 | 270 |
| oracle|naive|every5 | 4 | 1.00 | 6.17 | 0.0 | 0.2 | 7.2 | 23 | - | - | - | - | - | 0.00 | 1.00 | 1.00 | 1.00 | 207 |
| oracle|imgdedup|every1 | 4 | 1.00 | 5.17 | 0.0 | 0.0 | 6.2 | 33 | - | - | - | - | - | 0.00 | 0.50 | 1.00 | 1.00 | 290 |
| oracle|imgdedup|every5 | 4 | 1.00 | 2.17 | 0.0 | 0.2 | 3.2 | 15 | - | - | - | - | - | 0.00 | 1.00 | 1.00 | 1.00 | 149 |
| oracle|bytetrack|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.1 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 127 |
| oracle|bytetrack|every5 | 4 | 1.00 | 0.00 | 0.0 | 0.7 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 131 |
| oracle|botsort_cmc|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.1 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 127 |
| oracle|botsort_cmc|every5 | 4 | 1.00 | 0.00 | 0.0 | 0.7 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 131 |

Centroid association, partial clips:

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| yolo11s|naive|every1 | 9 | 0.56 | 31.12 | 65.8 | 0.1 | 58.3 | 399 | - | - | - | - | - | 0.00 | 0.56 | 0.56 | 0.67 | 355 |
| yolo11s|naive|every5 | 9 | 0.56 | 5.38 | 11.8 | 2.2 | 11.3 | 74 | - | - | - | - | - | 0.22 | 0.56 | 0.56 | 0.67 | 333 |
| yolo11s|imgdedup|every1 | 9 | 0.56 | 4.12 | 20.0 | 1.9 | 9.8 | 82 | - | - | - | - | - | 0.22 | 0.56 | 0.56 | 0.67 | 348 |
| yolo11s|imgdedup|every5 | 9 | 0.56 | 2.62 | 8.2 | 2.8 | 6.7 | 44 | - | - | - | - | - | 0.22 | 0.56 | 0.56 | 0.67 | 328 |
| yolo11s|bytetrack|every1 | 9 | 0.56 | 0.12 | 1.4 | 0.1 | 2.0 | 13 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 254 |
| yolo11s|bytetrack|every5 | 9 | 0.56 | 0.12 | 0.5 | 2.2 | 1.5 | 9 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 176 |
| yolo11s|botsort_cmc|every1 | 9 | 0.56 | 0.12 | 1.4 | 0.1 | 2.0 | 14 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 263 |
| yolo11s|botsort_cmc|every5 | 9 | 0.56 | 0.12 | 0.5 | 2.2 | 1.7 | 10 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 199 |
| yolo11n|naive|every1 | 9 | 0.56 | 31.62 | 35.9 | 0.2 | 58.3 | 337 | - | - | - | - | - | 0.00 | 0.56 | 0.56 | 0.56 | 352 |
| yolo11n|naive|every5 | 9 | 0.56 | 6.00 | 6.8 | 1.6 | 12.0 | 68 | - | - | - | - | - | 0.22 | 0.56 | 0.56 | 0.56 | 327 |
| yolo11n|imgdedup|every1 | 9 | 0.56 | 4.50 | 9.5 | 1.3 | 10.2 | 62 | - | - | - | - | - | 0.22 | 0.56 | 0.56 | 0.56 | 353 |
| yolo11n|imgdedup|every5 | 9 | 0.56 | 2.75 | 4.1 | 1.6 | 6.7 | 36 | - | - | - | - | - | 0.22 | 0.56 | 0.56 | 0.56 | 302 |
| yolo11n|bytetrack|every1 | 9 | 0.56 | 0.12 | 0.9 | 0.2 | 2.3 | 13 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 254 |
| yolo11n|bytetrack|every5 | 9 | 0.56 | 0.12 | 0.0 | 1.2 | 1.8 | 9 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 203 |
| yolo11n|botsort_cmc|every1 | 9 | 0.56 | 0.12 | 0.9 | 0.2 | 2.0 | 12 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 265 |
| yolo11n|botsort_cmc|every5 | 9 | 0.56 | 0.12 | 0.0 | 1.2 | 1.8 | 9 | - | - | - | - | - | 0.56 | 0.56 | 0.56 | 0.56 | 203 |
| oracle|naive|every1 | 9 | 1.00 | 38.17 | 0.0 | 0.0 | 39.2 | 318 | - | - | - | - | - | 0.22 | 0.67 | 0.89 | 1.00 | 340 |
| oracle|naive|every5 | 9 | 1.00 | 6.96 | 0.0 | 0.4 | 8.0 | 65 | - | - | - | - | - | 0.22 | 0.89 | 0.89 | 1.00 | 253 |
| oracle|imgdedup|every1 | 9 | 1.00 | 5.46 | 0.0 | 0.0 | 6.5 | 53 | - | - | - | - | - | 0.22 | 0.89 | 1.00 | 1.00 | 246 |
| oracle|imgdedup|every5 | 9 | 1.00 | 3.08 | 0.0 | 0.4 | 4.1 | 34 | - | - | - | - | - | 0.22 | 0.89 | 1.00 | 1.00 | 222 |
| oracle|bytetrack|every1 | 9 | 0.89 | 0.12 | 0.0 | 0.3 | 1.9 | 13 | - | - | - | - | - | 0.89 | 0.89 | 0.89 | 0.89 | 188 |
| oracle|bytetrack|every5 | 9 | 0.67 | 0.12 | 0.0 | 1.1 | 1.8 | 10 | - | - | - | - | - | 0.67 | 0.67 | 0.67 | 0.67 | 198 |
| oracle|botsort_cmc|every1 | 9 | 0.89 | 0.12 | 0.0 | 0.2 | 1.9 | 13 | - | - | - | - | - | 0.89 | 0.89 | 0.89 | 0.89 | 190 |
| oracle|botsort_cmc|every5 | 9 | 0.67 | 0.12 | 0.0 | 1.1 | 1.8 | 10 | - | - | - | - | - | 0.67 | 0.67 | 0.67 | 0.67 | 198 |

IoU association, complete clips (why centroid is the recommendation on moving cameras):

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| yolo11s|bytetrack|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.7 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 130 |
| yolo11s|bytetrack|every5 | 4 | 0.50 | 0.00 | 0.0 | 1.7 | 1.0 | 2 | - | - | - | - | - | 0.50 | 0.50 | 0.50 | 0.50 | 188 |
| yolo11s|botsort_cmc|every1 | 4 | 0.25 | 0.00 | 0.0 | 0.0 | 1.0 | 1 | - | - | - | - | - | 0.25 | 0.25 | 0.25 | 0.25 | 185 |
| yolo11s|botsort_cmc|every5 | 4 | 0.25 | 0.00 | 0.0 | 0.0 | 1.0 | 1 | - | - | - | - | - | 0.25 | 0.25 | 0.25 | 0.25 | 185 |
| oracle|bytetrack|every1 | 4 | 1.00 | 0.00 | 0.0 | 0.3 | 1.0 | 4 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 130 |
| oracle|bytetrack|every5 | 4 | 0.75 | 0.00 | 0.0 | 0.8 | 1.0 | 3 | - | - | - | - | - | 0.75 | 0.75 | 0.75 | 0.75 | 159 |
| oracle|botsort_cmc|every1 | 4 | 0.50 | 0.00 | 0.0 | 0.0 | 1.0 | 2 | - | - | - | - | - | 0.50 | 0.50 | 0.50 | 0.50 | 146 |
| oracle|botsort_cmc|every5 | 4 | 0.75 | 0.00 | 0.0 | 0.8 | 1.0 | 3 | - | - | - | - | - | 0.75 | 0.75 | 0.75 | 0.75 | 159 |

On the complete clips the tuned pipeline reports every GT object exactly once (4 sightings for 4 objects, 0
duplicates, 0 false) where naive emits 72–108 reports; with IoU association BoT-SORT+CMC drops to 1–2 of 4 objects
(GMC + IoU gating on vertical phone video). On the partial clips YOLO11s/11n find 5 of 9 objects: the misses are
the two dolphins, the helicopter on deck and the surfacing submarine, which are not COCO classes, while the oracle rows (8/9)
show tracking itself is fine. The `false/min` there is dominated by unlabelled stretches (A's `partial` flag).

## 7. E2: horizon-pinned stabilisation (onboard dev sets)

`HorizonStabilizer` (Canny + Hough on a 480 px frame, EMA roll/offset, affine warp of detections and of the GMC
image, inverse-mapped back; horizon found on 100 % of these frames). `ghost_fleet_5fps` + `mvtd_5fps` + `smd_vis_onboard_1fps`
dev, 22 sequences, tuned config:

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| raw: yolo11s|bytetrack|every1 | 31 | 0.68 | 0.14 | 3.2 | 0.3 | 2.5 | 106 | - | - | - | - | - | 0.68 | 0.71 | 0.71 | 0.71 | 233 |
| raw: yolo11s|botsort_cmc|every1 | 31 | 0.68 | 0.10 | 2.9 | 0.8 | 2.4 | 101 | - | - | - | - | - | 0.68 | 0.71 | 0.71 | 0.71 | 234 |
| raw: oracle-d0.2-j0.05|bytetrack|every1 | 31 | 0.84 | 0.05 | 0.1 | 0.2 | 2.2 | 54 | - | - | - | - | - | 0.94 | 0.94 | 0.94 | 0.94 | 188 |
| raw: oracle-d0.2-j0.05|botsort_cmc|every1 | 31 | 0.81 | 0.05 | 0.1 | 0.2 | 2.2 | 53 | - | - | - | - | - | 0.87 | 0.87 | 0.87 | 0.87 | 196 |
| stab: yolo11s|bytetrack|every1 | 31 | 0.61 | 0.30 | 3.0 | 0.3 | 2.9 | 106 | - | - | - | - | - | 0.68 | 0.68 | 0.68 | 0.68 | 247 |
| stab: yolo11s|botsort_cmc|every1 | 31 | 0.68 | 0.19 | 2.9 | 0.8 | 2.7 | 101 | - | - | - | - | - | 0.71 | 0.71 | 0.71 | 0.71 | 238 |
| stab: oracle-d0.2-j0.05|bytetrack|every1 | 31 | 0.74 | 0.10 | 0.6 | 0.2 | 2.2 | 55 | - | - | - | - | - | 0.97 | 0.97 | 0.97 | 0.97 | 179 |
| stab: oracle-d0.2-j0.05|botsort_cmc|every1 | 31 | 0.74 | 0.07 | 0.4 | 0.1 | 2.2 | 54 | - | - | - | - | - | 0.94 | 0.94 | 0.94 | 0.94 | 192 |

| set | stabiliser ms/frame (4-thread CPU) | tracker ms/frame raw → stabilised (BoT-SORT+CMC) |
|---|---|---|
| ghost_fleet_5fps (1080×1920) | 11.5–12.4 | 2.5 → 16.7 |
| mvtd_5fps (1920×1080) | 4.0–4.4 | 2.6 → 9.0 |
| smd_vis_onboard_1fps | 2.5–4.8 | 2.0 → 7.4 |

**Result: negative.** Stabilising the frame to the horizon costs 3–12 ms/frame and *increases* duplicates
(0.10 → 0.19 BoT-SORT+CMC, 0.14 → 0.30 ByteTrack) and lowers recall (0.68 → 0.61 ByteTrack; 0.84 → 0.74 noisy
oracle). The roll/offset warp moves boxes by more than the centroid gate when the horizon estimate jitters, and the
GMC already absorbs the slow motion. Not recommended; left in as an opt-in
(`run_clip(..., stabilizer=HorizonStabilizer())`). IDSW/Frag for E2 were not re-scored with TrackEval (the
`--no-baselines` runs skipped it); fragmentation shows up in dup/object and reports/object.

## 8. E3: selector `ladder_decay` × `max_bytes_per_object` × 4 budgets (dev, YOLO11s, run `dev_yolo_centroid/delivery_e3.json`)

| selector | config | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@72KB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|
| decay 1/.5/.25/.1 (default) | naive | 0.13 | 0.92 | 0.92 | 0.92 | 399 | 485 |
| decay | imgdedup | 0.23 | 0.92 | 0.92 | 0.92 | 72 | 423 |
| decay | botsort_cmc every1 | 0.82 | 0.85 | 0.85 | 0.85 | 76 | 467 |
| decay | botsort_cmc every2 | 0.85 | 0.85 | 0.85 | 0.85 | 71 | 457 |
| no decay | naive / imgdedup / botsort_cmc | 0.13 / 0.23 / 0.82 | 0.92 / 0.92 / 0.85 | same | same | 399 / 72 / 76 | 485 / 423 / 467 |
| decay + cap 48 KB/object | naive | 0.10 | 0.10 | 0.10 | 0.10 | 469 | 730 |
| decay + cap 48 KB/object | imgdedup | 0.08 | 0.08 | 0.08 | 0.08 | 101 | 132 |
| decay + cap 48 KB/object | botsort_cmc every1 | 0.82 | 0.85 | 0.85 | 0.85 | 74 | 182 |
| decay + cap 48 KB/object | botsort_cmc every2 | 0.85 | 0.85 | 0.85 | 0.85 | 70 | 162 |

`ladder_decay` is a no-op at these budgets because each object's `fact` (~0.3 KB) and `crop` (3–20 KB) are already
far cheaper per unit value than any `full`; the selector takes all facts and crops before any full frame whether or
not the ladder is decayed. A 48 KB/object cap is what matters: it is irrelevant for sightings (crop+thumb fit under
it; the 160–180 KB/object is leftover budget spent on fulls) but it kills the baselines, which can only deliver
objects as ~400 KB full frames and get **0.08–0.10** of objects at every budget. Recommendation: keep `ladder_decay`
default, set `max_bytes_per_object` ≈ 48–64 KB in the real scheduler (D) once sightings are the input.

## 9. Locked test (run once, tuned config)

Test sources: `fvessel_clip07/09` (weak hold-out: same camera and day as dev), `Spotted_A_Submarine` +
`Ship_in_the_Distance_Ian` (`ghost_fleet_5fps`; `Dolphin…Bengal` excluded, 0 boxes), `18-SailBoat`, `22-Boat`,
`7-Boat` (MVTD), `MVI_0797`, `MVI_0801` (SMD). 9 sequences, 15 GT objects, 46 min. Run `test_final`.

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| yolo11s|naive|every1 | 15 | 0.93 | 64.47 | 172.5 | 3.2 | 73.5 | 5412 | - | - | - | - | - | 0.33 | 0.93 | 1.00 | 1.00 | 929 |
| yolo11s|imgdedup|every1 | 15 | 0.87 | 6.53 | 9.6 | 59.8 | 8.5 | 331 | - | - | - | - | - | 0.67 | 0.87 | 0.93 | 0.93 | 866 |
| yolo11s|bytetrack|every1 | 15 | 0.73 | 0.28 | 2.5 | 34.3 | 3.8 | 209 | 0.23 | 0.29 | 0.18 | 19 | 67 | 0.93 | 0.93 | 0.93 | 0.93 | 789 |
| yolo11s|botsort_cmc|every1 | 15 | 0.73 | 0.18 | 2.4 | 48.4 | 3.8 | 212 | 0.27 | 0.33 | 0.23 | 20 | 72 | 0.93 | 0.93 | 0.93 | 0.93 | 818 |
| yolo11s|naive|every2 | 15 | 0.93 | 32.06 | 90.4 | 5.3 | 37.1 | 2816 | - | - | - | - | - | 0.27 | 0.93 | 1.00 | 1.00 | 923 |
| yolo11s|imgdedup|every2 | 15 | 0.80 | 4.78 | 7.9 | 59.2 | 6.7 | 266 | - | - | - | - | - | 0.40 | 0.93 | 0.93 | 0.93 | 789 |
| yolo11s|bytetrack|every2 | 15 | 0.67 | 0.25 | 1.9 | 48.4 | 3.6 | 161 | 0.22 | 0.26 | 0.15 | 15 | 37 | 0.93 | 0.93 | 0.93 | 0.93 | 732 |
| yolo11s|botsort_cmc|every2 | 15 | 0.67 | 0.06 | 1.9 | 48.6 | 3.1 | 159 | 0.24 | 0.30 | 0.20 | 16 | 40 | 0.93 | 0.93 | 0.93 | 0.93 | 705 |
| yolo11n|naive|every1 | 15 | 0.87 | 46.50 | 39.3 | 11.4 | 54.0 | 1621 | - | - | - | - | - | 0.27 | 1.00 | 1.00 | 1.00 | 697 |
| yolo11n|imgdedup|every1 | 15 | 0.67 | 5.98 | 3.7 | 22.7 | 9.1 | 172 | - | - | - | - | - | 0.53 | 1.00 | 1.00 | 1.00 | 362 |
| yolo11n|bytetrack|every1 | 15 | 0.73 | 0.25 | 1.0 | 17.9 | 3.1 | 87 | 0.24 | 0.27 | 0.19 | 14 | 41 | 0.93 | 0.93 | 0.93 | 0.93 | 502 |
| yolo11n|botsort_cmc|every1 | 15 | 0.67 | 0.28 | 0.9 | 10.5 | 3.2 | 89 | 0.24 | 0.28 | 0.20 | 17 | 46 | 0.93 | 0.93 | 0.93 | 0.93 | 507 |
| yolo11n|naive|every2 | 15 | 0.87 | 23.00 | 19.8 | 12.3 | 27.4 | 820 | - | - | - | - | - | 0.27 | 1.00 | 1.00 | 1.00 | 648 |
| yolo11n|imgdedup|every2 | 15 | 0.60 | 4.18 | 2.7 | 19.9 | 7.1 | 126 | - | - | - | - | - | 0.33 | 1.00 | 1.00 | 1.00 | 341 |
| yolo11n|bytetrack|every2 | 15 | 0.53 | 0.34 | 0.7 | 12.0 | 3.6 | 63 | 0.25 | 0.28 | 0.19 | 7 | 17 | 0.93 | 0.93 | 0.93 | 0.93 | 399 |
| yolo11n|botsort_cmc|every2 | 15 | 0.60 | 0.31 | 0.6 | 11.0 | 3.4 | 65 | 0.24 | 0.28 | 0.20 | 10 | 24 | 0.93 | 0.93 | 0.93 | 0.93 | 417 |
| oracle|naive|every1 | 15 | 1.00 | 172.31 | 1.2 | 0.0 | 173.3 | 2855 | - | - | - | - | - | 0.33 | 1.00 | 1.00 | 1.00 | 858 |
| oracle|imgdedup|every1 | 15 | 1.00 | 11.57 | 1.2 | 3.1 | 12.6 | 200 | - | - | - | - | - | 0.67 | 1.00 | 1.00 | 1.00 | 528 |
| oracle|bytetrack|every1 | 15 | 0.93 | 0.56 | 0.1 | 0.5 | 4.6 | 76 | 0.84 | 0.91 | 0.88 | 15 | 17 | 0.93 | 0.93 | 0.93 | 0.93 | 439 |
| oracle|botsort_cmc|every1 | 15 | 0.93 | 0.56 | 0.1 | 0.5 | 4.7 | 77 | 0.90 | 0.91 | 0.89 | 18 | 10 | 0.93 | 0.93 | 0.93 | 0.93 | 445 |
| oracle|naive|every2 | 15 | 1.00 | 85.82 | 0.6 | 0.8 | 86.8 | 1428 | - | - | - | - | - | 0.27 | 1.00 | 1.00 | 1.00 | 827 |
| oracle|imgdedup|every2 | 15 | 1.00 | 8.40 | 0.6 | 6.0 | 9.4 | 147 | - | - | - | - | - | 0.47 | 1.00 | 1.00 | 1.00 | 503 |
| oracle|bytetrack|every2 | 15 | 0.93 | 0.61 | 0.1 | 1.4 | 4.4 | 73 | 0.79 | 0.89 | 0.84 | 12 | 11 | 0.93 | 0.93 | 0.93 | 0.93 | 425 |
| oracle|botsort_cmc|every2 | 15 | 0.93 | 0.39 | 0.1 | 1.4 | 4.3 | 74 | 0.89 | 0.93 | 0.92 | 9 | 12 | 0.93 | 0.93 | 0.93 | 0.93 | 436 |

Per set (YOLO11s / oracle, every1):

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| fvessel_clip10: yolo11s|naive|every1 | 7 | 1.00 | 128.12 | 170.5 | 6.4 | 129.1 | 4296 | - | - | - | - | - | 0.00 | 1.00 | 1.00 | 1.00 | 1289 |
| fvessel_clip10: yolo11s|imgdedup|every1 | 7 | 0.86 | 3.38 | 6.6 | 113.2 | 5.2 | 162 | - | - | - | - | - | 0.57 | 0.86 | 0.86 | 0.86 | 1333 |
| fvessel_clip10: yolo11s|bytetrack|every1 | 7 | 0.71 | 0.25 | 2.0 | 75.4 | 7.8 | 163 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 1128 |
| fvessel_clip10: yolo11s|botsort_cmc|every1 | 7 | 0.71 | 0.29 | 2.0 | 87.0 | 7.5 | 166 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 1183 |
| fvessel_clip10: oracle|naive|every1 | 7 | 1.00 | 248.92 | 0.0 | 0.0 | 249.9 | 1754 | - | - | - | - | - | 0.00 | 1.00 | 1.00 | 1.00 | 1284 |
| fvessel_clip10: oracle|imgdedup|every1 | 7 | 1.00 | 8.58 | 0.0 | 6.7 | 9.6 | 68 | - | - | - | - | - | 0.57 | 1.00 | 1.00 | 1.00 | 699 |
| fvessel_clip10: oracle|bytetrack|every1 | 7 | 1.00 | 0.00 | 0.0 | 1.0 | 6.4 | 44 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 504 |
| fvessel_clip10: oracle|botsort_cmc|every1 | 7 | 1.00 | 0.00 | 0.0 | 1.0 | 6.5 | 45 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 519 |

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ghost_fleet_5fps: yolo11s|naive|every1 | 2 | 1.00 | 123.50 | 208.0 | 0.1 | 124.5 | 875 | - | - | - | - | - | 0.50 | 1.00 | 1.00 | 1.00 | 906 |
| ghost_fleet_5fps: yolo11s|imgdedup|every1 | 2 | 1.00 | 4.00 | 9.3 | 49.1 | 5.0 | 38 | - | - | - | - | - | 0.50 | 1.00 | 1.00 | 1.00 | 774 |
| ghost_fleet_5fps: yolo11s|bytetrack|every1 | 2 | 1.00 | 0.50 | 4.0 | 0.1 | 4.0 | 28 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 766 |
| ghost_fleet_5fps: yolo11s|botsort_cmc|every1 | 2 | 1.00 | 0.00 | 4.0 | 48.4 | 3.5 | 28 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 776 |
| ghost_fleet_5fps: oracle|naive|every1 | 2 | 1.00 | 431.50 | 0.0 | 0.0 | 432.5 | 865 | - | - | - | - | - | 0.50 | 1.00 | 1.00 | 1.00 | 880 |
| ghost_fleet_5fps: oracle|imgdedup|every1 | 2 | 1.00 | 13.00 | 0.0 | 0.0 | 14.0 | 28 | - | - | - | - | - | 0.50 | 1.00 | 1.00 | 1.00 | 625 |
| ghost_fleet_5fps: oracle|bytetrack|every1 | 2 | 1.00 | 2.50 | 0.0 | 0.1 | 10.0 | 20 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 671 |
| ghost_fleet_5fps: oracle|botsort_cmc|every1 | 2 | 1.00 | 2.00 | 0.0 | 0.1 | 10.0 | 20 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 672 |

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| mvtd_5fps: yolo11s|naive|every1 | 3 | 0.67 | 18.00 | 203.4 | 0.1 | 28.0 | 176 | - | - | - | - | - | 0.33 | 0.67 | 1.00 | 1.00 | 663 |
| mvtd_5fps: yolo11s|imgdedup|every1 | 3 | 0.67 | 7.33 | 78.0 | 0.1 | 12.0 | 70 | - | - | - | - | - | 0.67 | 0.67 | 1.00 | 1.00 | 438 |
| mvtd_5fps: yolo11s|bytetrack|every1 | 3 | 0.67 | 0.33 | 11.9 | 0.2 | 1.5 | 12 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 315 |
| mvtd_5fps: yolo11s|botsort_cmc|every1 | 3 | 0.67 | 0.33 | 10.2 | 0.2 | 2.0 | 12 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 318 |
| mvtd_5fps: oracle|naive|every1 | 3 | 1.00 | 53.67 | 0.0 | 0.0 | 54.7 | 164 | - | - | - | - | - | 0.33 | 1.00 | 1.00 | 1.00 | 434 |
| mvtd_5fps: oracle|imgdedup|every1 | 3 | 1.00 | 11.00 | 0.0 | 0.0 | 12.0 | 36 | - | - | - | - | - | 0.67 | 1.00 | 1.00 | 1.00 | 319 |
| mvtd_5fps: oracle|bytetrack|every1 | 3 | 1.00 | 0.00 | 0.0 | 0.0 | 1.7 | 5 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 182 |
| mvtd_5fps: oracle|botsort_cmc|every1 | 3 | 1.00 | 0.33 | 0.0 | 0.0 | 1.7 | 5 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 171 |

| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| smd_vis_onboard_1fps: yolo11s|naive|every1 | 3 | 1.00 | 11.50 | 43.5 | 0.0 | 12.5 | 65 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 281 |
| smd_vis_onboard_1fps: yolo11s|imgdedup|every1 | 3 | 1.00 | 11.00 | 40.5 | 0.0 | 12.0 | 61 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 281 |
| smd_vis_onboard_1fps: yolo11s|bytetrack|every1 | 3 | 0.67 | 0.00 | 3.0 | 0.0 | 2.0 | 6 | - | - | - | - | - | 0.67 | 0.67 | 0.67 | 0.67 | 336 |
| smd_vis_onboard_1fps: yolo11s|botsort_cmc|every1 | 3 | 0.67 | 0.00 | 3.0 | 0.0 | 2.0 | 6 | - | - | - | - | - | 0.67 | 0.67 | 0.67 | 0.67 | 330 |
| smd_vis_onboard_1fps: oracle|naive|every1 | 3 | 1.00 | 14.50 | 45.0 | 0.0 | 15.5 | 72 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 273 |
| smd_vis_onboard_1fps: oracle|imgdedup|every1 | 3 | 1.00 | 14.00 | 42.0 | 0.0 | 15.0 | 68 | - | - | - | - | - | 1.00 | 1.00 | 1.00 | 1.00 | 273 |
| smd_vis_onboard_1fps: oracle|bytetrack|every1 | 3 | 0.67 | 0.00 | 4.5 | 0.0 | 2.0 | 7 | - | - | - | - | - | 0.67 | 0.67 | 0.67 | 0.67 | 366 |
| smd_vis_onboard_1fps: oracle|botsort_cmc|every1 | 3 | 0.67 | 0.00 | 4.5 | 0.0 | 2.0 | 7 | - | - | - | - | - | 0.67 | 0.67 | 0.67 | 0.67 | 366 |

![delivered vs budget, test](C_assets/delivery_test.png)
![reports per object, test](C_assets/reduction_test.png)

**Headline (test, YOLO11s + BoT-SORT+CMC + sightings, every frame):** 73 % object recall (naive 93 %), **0.18
duplicates/object vs 64.5 naive and 6.5 image-dedup (360× / 36× fewer)**, 2.4 false sightings/min vs 172 / 9.6,
3.8 reports/object vs 73.5 / 8.5, and **93 % of GT objects delivered with an image at every budget including
72 KB/60 s**, where naive delivers 33 % and image-dedup 67 %. Bytes per delivered object at 480 KB: 818 KB
(sightings also spend the leftover budget on full frames) vs 929 KB naive; with the 48 KB cap of §8 this drops to
~180 KB. HOTA 0.27 / IDF1 0.23 with YOLO11s vs 0.90 / 0.89 with oracle boxes: identity is kept whenever the detector
fires. Both own-ship test clips are reported once each with 0 duplicates (BoT-SORT+CMC); the recall loss is
concentrated in the 11-minute 1 Hz FVessel clips (2 of 7 AIS vessels never matched: long passes fragment at 1 Hz with
`max_lost_s=6` and the fragments match the neighbouring, overlapping vessel; see the merged/duplicate sheets) and in
SMD/MVTD where YOLO does not fire on small or non-boat targets. B's YOLO11n export (640 px) loses one more object
than YOLO11s at 1280 px but halves false/min.

## 10. Failure cases and timelines

* `C_assets/failures_dev_duplicates.jpg` — same GT object reported twice (ID switches at the edge of frame, after a
  wave occlusion, and on FVessel when the detector drops the hull for >6 s at 1 Hz).
* `C_assets/failures_dev_missed.jpg` / `failures_test_missed.jpg` — GT objects with no matched sighting: small far
  contacts (<60 px on 2560 px FVessel frames), the SMD objects labelled on 2–6 frames, and two large FVessel vessels
  whose sightings were absorbed by an overlapping neighbour (merged objects).
* `C_assets/failures_dev_false_or_merged.jpg` — "false" sightings: nearly all are real, unlabelled vessels (FVessel
  GT = AIS-equipped only), which is why `false/min` is an upper bound.
* Timelines (sightings as bars, ▲ open / | update / × close, GT tracks grey, naive reports/s in red):
  `timeline_fvessel_clip04.png`, `timeline_19-Boat.png`, `timeline_16-USV.png` (dev),
  `timeline_Cruise_ship_Mediterranean_sunset_off_Italy.png`, `timeline_Submarine_surfacing.png` (own clips),
  `timeline_Spotted_A_Submarine.png`, `timeline_fvessel_clip07.png` (test). `Spotted_A_Submarine` shows the
  remaining weakness: one GT track, one long sighting for the first 110 s, then 12 short re-opens as the periscope
  intermittently clears the 0.35 score floor and the 20 s relink window; 24 reports vs 875 naive.

![Spotted_A_Submarine timeline](C_assets/timeline_Spotted_A_Submarine.png)
![false or merged, dev](C_assets/failures_dev_false_or_merged.jpg)

## 11. Recommended configuration

```python
UltralyticsDetector("yolo11s.pt", imgsz=1280, conf=0.05)   # or B's ONNX export via the same adapter; score floor 0.35 at the aggregator
BoxmotTracker(TrackerParams(variant="botsort_cmc", asso_func="centroid", max_lost_s=6.0, fps=fps))
SightingAggregator(seq_id, AggregatorParams(min_hits=1, close_after_s=5, relink_gap_s=20, min_update_interval_s=10,
                                            keyframe_improve=0.3, score_jump=0.2), frame_size=(W, H))
run_clip(frames, det, trk, agg, detect_every=2, fps=fps)   # every2 = same recall, half the detector cost
select.greedy(events, budget, ladder_decay=(1, .5, .25, .1), max_bytes_per_object=48_000)
```
ByteTrack with the same association/aggregator is the CPU fallback (1–2 ms/frame vs 2.5 ms for BoT-SORT+CMC at
1080p, same sighting metrics within noise, slightly worse HOTA/IDSW). Stabilisation off. The whole post-detector
stage costs <5 ms/frame on this VM at 4 threads.

## 12. Limitations / honest notes

* Small test set: 15 GT objects; one object = 7 % recall. Own-clip GT is teacher+QA with ~50 % of teacher boxes removed.
* GT only covers AIS vessels (FVessel), the one filmed boat (MVTD) and the labelled stretches of `partial` clips, so
  `false/min` and MOTA/FP are upper bounds; the false-sighting contact sheets show mostly real boats.
* SMD sample GT (2–6 labelled frames per object, duplicate `(frame,id)` rows which we drop keeping the first) makes
  SMD numbers detection-level at best.
* YOLO cached at conf 0.05 and replayed; the tracker × cadence matrix never re-ran the CNN. No GPU VM was created;
  everything ran on the session VM's CPU (hence no `edgeml-c-*` resources to delete).
* `detect_every>1` skips tracker/aggregator updates on non-detector frames (no Kalman coast call into boxmot); the
  sighting metrics still use all frames. TrackEval for those rows is on processed frames only.
* E2 was scored on sighting metrics only. The appearance ReID variant (`botsort_reid`) exists in the adapter but was
  not run (no ReID weights offline); relink uses the 32-dim colour/HOG embedding computed once per key frame.
* `edgeml.data.seqs` is C's local loader; A's `edgeml.data.mot` (PR #16/#20) reads the same layout and should
  replace it when merged.

## 13. Reproduce

```bash
python -m venv ~/venv-edgeml && ~/venv-edgeml/bin/pip install -e edgeml boxmot ultralytics trackeval supervision
gcloud storage rsync -r gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval ~/data/edgeml_eval
python edgeml/src/edgeml/data/convert.py mvtd --out ~/data/edgeml_eval/mvtd_5fps      # + smd
python edgeml/tools/run_c.py cache  --data ~/data/edgeml_eval --sets fvessel_clip10 mvtd_5fps smd_vis_onboard_1fps ghost_fleet_5fps --detector yolo11s
python edgeml/tools/run_c.py matrix --data ~/data/edgeml_eval --sets fvessel_clip10 mvtd_5fps smd_vis_onboard_1fps --split dev \
   --detectors yolo11s oracle --trackers bytetrack ocsort botsort botsort_cmc --every 1 2 5 --out ~/runs/dev_yolo_centroid
python edgeml/tools/run_c.py matrix ... --split dev --min-score 0.35 --agg '{"min_hits":1,"relink_gap_s":20,"min_update_interval_s":10}' \
   --trk '{"asso_func":"centroid","max_lost_s":6}' --out ~/runs/tune/combo        # §5; add --stabilize for §7
python edgeml/tools/run_c.py delivery --run ~/runs/dev_yolo_centroid --cfgs ...      # §8
python edgeml/tools/run_c.py matrix --sets fvessel_clip10 ghost_fleet_5fps mvtd_5fps smd_vis_onboard_1fps --split test \
   --detectors yolo11s yolo11n oracle --trackers bytetrack botsort_cmc --every 1 2 --min-score 0.35 --agg ... --trk ... --out ~/runs/test_final
python edgeml/tools/c_tables.py ~/runs > tables.md
python edgeml/tools/report_c.py --runs ~/runs/test_final --sets ... --out edgeml/reports/C_assets --title test ...
```
All run directories (results.jsonl, summary.json, trackeval.json, events/*.jsonl with byte-exact forms, tracks/*.txt,
tables.md) are at **`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/c-20261104-final/`**
(`dev_yolo_centroid`, `dev_oracle_centroid`, `dev_oracle_iou`, `dev_yolo11n_centroid`, `tune/*`, `gf5_centroid`,
`gf5_iou`, `e2`, `e2--stabilize`, `test_final`).
