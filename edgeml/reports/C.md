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

