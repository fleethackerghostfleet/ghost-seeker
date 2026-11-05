# Workstream G — fine-tuned vessel detector for the edge sidecar

**Goal.** Raise sighting recall on far/small vessels without raising false sightings or CPU cost
(C.md §6b: locked-test sighting recall 0.72 vs 0.88 naive / 0.92 oracle boxes → the COCO YOLO11n detector is the bottleneck).

**Result (one line).** `yolo11n-ft-640-v1` — YOLO11n fine-tuned single-class on dev-only maritime data — replaces D's COCO YOLO11n-640
as the sidecar default (`make demo-models`, `make demo-sidecar`, `python -m edgeml.sidecar`). Same latency (INT8 p95 38 ms vs 41 ms, 4 threads),
dev sighting recall 0.68 → 0.85 with false sightings/min 1.24 → 0.07. Locked test: **see §6 (single run)**.

## 1. Data discipline — which set was used for what

| Set | Used for | Notes |
|---|---|---|
| SeaShips `train` + `valid` (catalog `seaships7000`, 1 750 + 1 750 img) | training (`train`) / YOLO val (`valid`) | upstream `test` (3 500 img) LOCKED, never read |
| FVessel dev clips 01–06, 08, 10 (`edgeml/eval/fvessel_clip10`) | training (01–06), YOLO val holdout (08, 10) | `fvessel_clip07/09` LOCKED |
| Ghost-Fleet 5 fps dev clips with complete/partial GT (vessel classes only) | training; holdout `Submarine_surfacing`, `Ship_in_the_Distance_Utopia_of_the_Seas` → YOLO val | `Chase_N_Fish_Sharky` (incomplete GT) skipped; all `splits.yaml` test clips incl. `Spotted_A_Submarine` LOCKED |
| Singapore Maritime onshore `train` + `validation` (v2 only) | training / YOLO val | `test` split + `smd_vis_onboard_sample` MVI_0797/0801 LOCKED |
| INT8 calibration: 306 dev frames (SeaShips train, GF dev, FVessel dev) | NNCF PTQ | |
| **Model selection / sweeps** | C sighting metrics + B AP50 on **dev** only (§4) | |
| **Locked test** (`splits.yaml` test: GF test clips, `fvessel_clip07/09`, SeaShips test, SMD MVI_0797/0801) | evaluated **once**, §6 | |

Labels: class-agnostic `boat` (GF classes ship/boat/sailboat/submarine/jet-ski/kayak; FVessel `vessel`; SMD Vessel/ship, Ferry, Speed boat, Kayak, Sail boat, Boat;
buoys/animals/aircraft/people are *not* vessels). Teacher boxes for the GF dev clips are the QA'd `gt/gt.txt` already in the eval bucket (RT-DETRv2/D-FINE teacher + QA, see B/C).
Builder: `edgeml/tools/g_build_dataset.py` (symlinked images, `manifest.json` with per-source counts; v1 = 3 275 train / 2 353 val images, v2 adds SMD dev → 4 090 / 2 989).

## 2. Training (GPU VM `edgeml-g-train`, n1-standard-8 + T4, deleted)

`edgeml/tools/g_train.sh`: `yolo detect train` from COCO weights, 30 epochs, `single_cls=True`, mosaic off for the last 5, patience 8, seed 0.

| run | data | imgsz | YOLO-val mAP50 / mAP50-95 | wall |
|---|---|---|---|---|
| yolo11n-640 (v1) | SeaShips+FVessel+GF dev | 640 | 0.921 / 0.624 | 45 min |
| yolo11n-960 | same | 960 | 0.931 / 0.635 | 70 min |
| yolo11s-640 | same | 640 | TBD | |
| yolo11n-640-v2 | v1 + SMD onshore dev | 640 | TBD | |

(YOLO val = SeaShips valid + holdout clips; it is a *dev* number, used only to pick `best.pt`.)

## 3. Export + CPU latency (same procedure as D.md: ONNX opset 17 → OpenVINO FP32 → NNCF INT8, 4 pinned threads, 1080p frames, `edgeml.runtime.bench`)

Measured on the same 8-vCPU Xeon box (AVX2, no AVX-512/VNNI, so INT8 ≈ FP32 here; D's c3 proxy has VNNI and shows the INT8 win).

| model | precision / engine | p50 ms | p95 ms | FPS | RSS MB |
|---|---|---|---|---|---|
| D yolo11n-640 (COCO, current default) | INT8 OpenVINO | 32.4 | 40.9 | 29.2 | 657 |
| **G yolo11n-ft-640** | INT8 OpenVINO | **31.2** | **38.4** | 30.6 | 653 |
| G yolo11n-ft-640 | FP32 ORT (what `make demo-sidecar` runs) | 34.4 | 42.1 | 28.0 | 653 |
| G yolo11n-ft-960 | INT8 OpenVINO | 77.4 | 95.3 | 12.4 | 665 |
| G yolo11n-ft-960 | FP32 ORT | 74.5 | 89.9 | 12.9 | 753 |

INT8 sanity (ft-640, 100 dev frames, score ≥ 0.25): 124 FP32 boxes, 126 INT8 boxes, 124/124 FP32 boxes matched at IoU 0.5 → no measurable INT8 loss.
Budget: ≥ 2 FPS required, ≥ 20 FPS wanted → 640 passes with margin; 960 passes the hard budget only.

## 4. Dev evaluation (model selection)

### 4a. C end-to-end sighting metrics (sidecar config: ByteTrack, centroid association, max_lost 6 s, every frame, score ≥ 0.25; `run_c.py matrix`, dev = 9 GF clips + 8 FVessel clips, 34 GT objects)

| detector | recall | false/min | dup/obj | reports | holdout-only clips (10 obj): recall / false |
|---|---|---|---|---|---|
| yolo11n COCO 640 (baseline) | 0.68 | 1.24 | 0.44 | 191 | 0.70 / 11 |
| **yolo11n-ft-640 (v1)** | **0.85** | **0.07** | 0.18 | 143 | **0.90 / 0** |
| yolo11n-ft-960 | 0.82 | 0.22 | 0.15 | 134 | 0.80 / 3 |
| yolo11s-ft-640 | TBD | | | | |
| yolo11n-ft-640-v2 | TBD | | | | |
| oracle boxes | 0.97 | 0.00 | 0.03 | 131 | 1.00 / 0 |

"holdout-only" = the 4 dev clips the model never trained on (`fvessel_clip08/10`, `Submarine_surfacing`, `Ship_in_the_Distance_Utopia_of_the_Seas`);
the other dev clips were in the training set, so the all-dev row is optimistic for the fine-tunes — the locked test (§6) is the honest number.
Naive per-frame recall for ft-640 is 0.85 as well: the tracker no longer loses recall on top of the detector.

### 4b. B harness, class-agnostic AP50 (`bench_detectors.py --split dev`)

| detector | SeaShips dev (valid, stride 5) | SMD onboard sample dev (MVI_0790/0799) | Singapore Maritime onshore dev |
|---|---|---|---|
| yolo11n COCO 640 | 0.452 | 0.639 | 0.067 |
| yolo11n-ft-640 (v1) | 0.958 | 0.429 | 0.051 |
| yolo11n-ft-960 | 0.969 (at 960) | — | 0.045 |
| yolo11n-ft-640-v2 | TBD | TBD | TBD |

SeaShips valid was the YOLO val set (dev, not trained on). The onboard-sample drop (40 images) is the one regression seen on dev and is why v2 adds the SMD onshore dev frames;
the onshore set (~57 boxes/image, many < 32²) is near-zero for every YOLO11n variant and is not a sidecar target.

## 5. Decision rule (fixed before the test run)

Win = dev sighting recall up vs baseline AND dev false/min not worse AND INT8 p95 ≤ 50 ms @ 4 threads. Candidate picked on dev: **TBD**.

## 6. Locked test — evaluated once

TBD

## 7. Integration

* Artifacts: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/G/yolo11n-ft-640-v1/{model.onnx,meta.json,fp32/,int8/}` (`meta.json` `class_names: ["boat"]`, so the sidecar's `MARITIME_KEEP` filter and C/B label mapping work unchanged).
* `Makefile`: `DET_MODEL ?= yolo11n-ft-640-v1`, `make demo-models` pulls `edgeml/models/G/$(DET_MODEL)`, `make demo-sidecar` serves `$(MODELS)/$(DET_MODEL)/model.onnx`.
* `python -m edgeml.sidecar` without `--detector` now uses `$EDGEML_DETECTOR`, else `~/models/D/yolo11n-ft-640-v1/model.onnx`, else D's `yolo11n-640/model.onnx`.
* `edgeml.runtime.export.export_ultralytics` keeps a fine-tune's own class names instead of assuming COCO-80; `edgeml.detectors.zoo.register_extra` (`EDGEML_ZOO_EXTRA=name=/path/best.pt`) lets the B harness score fine-tunes; `UltralyticsDetector` no longer applies the COCO `boat` id filter to single-class models.
* Verified: TBD (sidecar on a dev clip).

## 8. Cost

VM `edgeml-g-train` (n1-standard-8 + T4 + 300 GB SSD) ≈ 3 h ≈ $2.5; deleted. Local CPU box for export/bench/eval.
