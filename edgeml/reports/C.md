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

