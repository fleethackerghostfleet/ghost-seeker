# Workstream E — end-to-end runnable demo

One command turns a clip into deduplicated, byte-exact sighting reports and shows what shore would receive at
each link budget. It integrates C (BoxMOT trackers, `SightingAggregator`, `select.greedy`, sighting metrics,
naive / image-dedup baselines) and D (ONNX Runtime / OpenVINO detectors, frame gate, MobileCLIP-S1 zero-shot
subtype + unknownness) with no re-implementation; the glue is `edgeml/scripts/demo.py` (~550 lines).

## Exact command (run e_20261104_2145)

```
D=~/data/edgeml_eval   # gcloud storage cp -r gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/{ghost_fleet_5fps,fvessel_clip10}/<seq> ...
taskset -c 0-3 python edgeml/scripts/demo.py \
  --clip $D/ghost_fleet_5fps/A_ship_in_the_distance \
  --clip $D/ghost_fleet_5fps/Cruise_ship_Mediterranean_sunset_off_Italy \
  --clip $D/ghost_fleet_5fps/Submarine_surfacing \
  --clip $D/ghost_fleet_5fps/Ship_in_the_Distance_Utopia_of_the_Seas \
  --clip $D/fvessel_clip10/fvessel_clip01 \
  --budgets 72000 480000 1920000 15000000 --out ~/runs/e_20261104_2145
```

Models (from `gs://…/edgeml/models/`, D's exports): detector `D/yolo11n-640/int8/model.xml` (YOLO11n INT8 OpenVINO,
5.8 MB; the script falls back to FP32 `model.onnx` on ONNX Runtime CPU EP if no IR is present — `--det`/`--engine`
override), embedder `D/mobileclip-s1/model.onnx` + `text_cache.json` on ONNX Runtime. 4 threads, CPUs 0–3, all dev
sequences (`edgeml/configs/splits.yaml`); no test sequence was read.

Pipeline per frame: D `FrameGate` (blank / no-change / pHash, forced every 10 s) → detector at `--detect-every`
→ C `make_tracker` (ByteTrack, centroid association; `--tracker botsort_cmc` for BoT-SORT + camera-motion
compensation) → C `SightingAggregator` (min_hits 2, relink 20 s, update interval 10 s) which embeds the key-frame
crop once per sighting via D's `ClipEmbedder`; `DemoAggregator` adds D's `ZeroShotClassifier` subtype /
unknownness to each open/update report. Forms are C's fact / crop@q50 / crop@q80 / thumb / full with exact JPEG byte
counts. Selection is C's `select.greedy` per 60 s window at each budget.

## Artifacts

Small ones are committed under `edgeml/reports/E_demo/`; the full run (events, detections, tracks, selections)
is at `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/e_20261104_2145/`.

| file | what it shows |
|---|---|
| `summary.md` / `summary.json` | per-clip table (frames, detections, tracks, open/update/close, reports per GT object, object recall, ms/frame p50/p95) and the delivered-objects-vs-budget table for sightings vs naive vs image-dedup |
| `<clip>/sightings.jsonl` (GCS) | the open/update/close events, CONTRACTS §1 schema, each with `value`, `subtype`, `unknownness`, `bearing_deg`, and byte-exact `forms` |
| `<clip>/selection_<budget>.json` (GCS) | per 60 s window: which (sighting, form) the greedy selector sends, bytes used, gain; plus GT objects delivered (image / fact) for sightings, naive and image-dedup |
| `<clip>/contact_sheet_<budget>.jpg` | what shore receives at that budget: one tile per sighting, the fact as text and the *best image form that was actually sent*, decoded from a byte-identical re-encode (crop / thumb / full); "FACT ONLY" when no image fit |
| `<clip>/timeline.png` | top: detections per frame and GT objects visible; bottom: one row per sighting with open ● / update ▲ / close ×. Hundreds of detections collapse to a handful of events — the visual proof of dedup |
| `<clip>/metrics.json` | everything in the tables plus gate reasons, embed counts, timing percentiles |
| `config.json` | models, tracker/aggregator/selector params, CPU affinity, git SHA |

## Headline numbers (run e_20261104_2145, 5 dev clips, 10 labelled GT objects)

| | sightings | image_dedup | naive |
|---|---|---|---|
| reports | 21 | 73 | 358 |
| GT objects with an image delivered @ 72 KB / 60 s | **10/10** (33 KB/obj) | 2/10 | 2/10 |
| @ 480 KB | 10/10 (210 KB/obj) | 9/10 | 9/10 |
| @ 1.92 MB | 10/10 (248 KB/obj) | 10/10 (529 KB/obj) | 9/10 |
| @ 15 MB | 10/10 (248 KB/obj) | 10/10 (828 KB/obj) | 10/10 (3.4 MB/obj) |

Object recall 1.00 on all 5 clips; 1.0–3.5 reports per GT object; 36–42 ms/frame p50, 88–108 ms p95 on the 5 fps
clips (detector 67–78 ms p50 for frames where it runs, ~4 ms when the gate skips). 0 false sightings/min on 3 clips,
2–4/min on the two "partial"-label sunset/distance clips.

## Honest caveats

- **Tiny GT set.** 10 objects over 5 short clips (9–75 s). The 10/10 vs 2/10 at 72 KB is a demonstration that
  the mechanism works, not a statistically meaningful comparison. C's experiment report covers the full dev set.
- **False sightings are mostly unlabelled boats.** `Cruise_ship…` (2/min) and `Utopia…` (4/min) are "partial"
  clips (A.md): the extra sightings are real distant vessels / a birds-and-boat ambiguity, not hallucinations — we
  have not verified every one.
- **Subtype is weak.** Zero-shot MobileCLIP-S1 on 64–320 px crops: the surfacing submarine is labelled `airplane`
  by COCO YOLO and `submarine` p=0.53 by CLIP; the cruise ship comes out `cargo ship`/`tanker` p≈0.3; unknownness
  0.5–0.8 almost everywhere. Treat `subtype` as a hint and `unknownness` as uncalibrated.
- **Gate vs static cameras.** On `fvessel_clip01` (fixed shore camera, 1 fps) the gate suppressed 99/111 frames
  (`no_change`), so the detector ran 12 times, TTFR was 9 s and the key frame never improved. Correct for bandwidth,
  but `--no-gate` (or a lower `diff_thr`) is the right setting for fixed cameras; we did not tune this.
- **Timing variance.** The same command measured 71–79 ms/frame p50 in an earlier run on this VM and 36–42 ms in the
  committed one (shared cloud CPU; `taskset` pins cores but not neighbours). Both are well under the 200 ms budget
  for 5 fps. Not Pi/Jetson numbers — D's report has those.
- **Image-delivered metric tolerance.** `eval/delivery.py` now looks at frame ±1 when the key frame has no GT row
  (FVessel labels start at frame 2); without it fvessel counted 0/2 image-delivered despite crops being sent.
- **Video input is implemented but untested** (only MOT dirs were run). Plain frame dirs need `--fps`.
- One 1.92 MB and 15 MB column are identical for sightings: with ≤5 sightings per window everything fits by
  ~250 KB/object, so larger budgets buy nothing — the aggregator caps reports, which is the point.

## Small fixes made in C/D code

- D `embed/clip_embedder.py`: `import torch` at the top of `ClipEmbedder.__init__` made the ORT/OpenVINO-only edge
  path fail without torch installed; moved into the torch branch.
- C `eval/delivery.py`: image-delivered check tolerates a 1-frame GT gap (see caveat above).
