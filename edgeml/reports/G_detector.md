# Workstream G — fine-tuned vessel detector for the edge sidecar

**Goal.** Raise sighting recall on far/small vessels without raising false sightings or CPU cost
(C.md §6b: locked-test sighting recall 0.72 vs 0.88 naive / 0.92 oracle boxes → the COCO YOLO11n detector is the bottleneck).

**Result (one line).** **No win — defaults unchanged.** A single-class YOLO11n fine-tune (`yolo11n-ft-640`, SeaShips + FVessel + Ghost-Fleet dev clips)
looked like a clear win on dev (sighting recall 0.68 → 0.85, false/min 1.24 → 0.07, same latency) but on the **locked test, evaluated once**, sighting recall
fell 0.48 → 0.37 (naive per-frame 0.75 → 0.65) while false sightings dropped 40 → 9. It is far more precise but misses exactly the far/small single
vessels the task is about (SeaShips-test small-object recall 0.33 → 0.00). The sidecar therefore keeps D's COCO YOLO11n-640; the fine-tune, its
alternates and the tooling are kept as opt-in artifacts (§7) and the failure analysis + next steps are in §9.

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
| yolo11s-640 | same | 640 | 0.902 / 0.591 (epoch 19, **not completed**: CUDA OOM when v2 was started on the same T4; ~2× CPU cost, not pursued) | 35 min (partial) |
| yolo11n-640-v2 | v1 + SMD onshore dev | 640 | 0.390 / 0.254 (val now includes dense SMD onshore, not comparable) | 25 min |

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
| yolo11n-ft-640-v2 (+SMD dev) | 0.82 | 0.47 | 0.19 | 149 | 0.80 / 6 |
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
| yolo11n-ft-640-v2 | 0.955 | 0.600 | 0.216 |

SeaShips valid was the YOLO val set (dev, not trained on). The onboard-sample drop (40 images) is the one regression seen on dev and is why v2 adds the SMD onshore dev frames;
the onshore set (~57 boxes/image, many < 32²) is near-zero for every YOLO11n variant and is not a sidecar target.

## 5. Decision rule (fixed before the test run)

Win = dev sighting recall up vs baseline AND dev false/min not worse AND INT8 p95 ≤ 50 ms @ 4 threads → confirm on the locked test (recall up, false/min not worse).
Candidate picked on dev: **yolo11n-ft-640 (v1)** — best holdout sighting recall (0.90) with zero false sightings and p95 38 ms.
v2 repairs the SMD-onboard AP50 regression (0.43 → 0.60, Singapore onshore 0.05 → 0.22) but pays on the sidecar metric (holdout recall 0.80, 6 false
sightings — the dense onshore frames teach it to fire on shoreline clutter); 960 is below the 20 FPS target with no dev gain. Both alternates are
ablations only; INT8 bench of v2: p50 31.2 / p95 42.5 ms.

## 6. Locked test — evaluated once (`splits.yaml` test; nothing here was used for any choice above)

### 6a. C sighting metrics (same sidecar config as §4a; 8 GF test clips + `fvessel_clip07/09`, 52 GT objects, 35.3 min of video)

| detector | sighting recall | false sightings | false/min | dup/obj | reports | naive per-frame recall |
|---|---|---|---|---|---|---|
| yolo11n COCO 640 (default, unchanged) | **0.48** | 40 | 1.13 | 0.23 | 225 | 0.75 |
| yolo11n-ft-640 (v1) | 0.37 | **9** | **0.26** | 0.12 | 122 | 0.65 |

Per clip (GT objects: found, false) — baseline → fine-tune:
`Arriving_In_Norfolk_seg1h10` 6: 3,0 → 3,1 · `seg1h13` 4: 1,0 → 1,1 · `Cruise_Ship_Docked…Dingy` 29: 11,2 → 8,3 · `Ferry_Passing_By` 1: 1,0 → 1,2 ·
`Sailboat_on_the_horizon_seg0` 2: 2,5 → **0**,0 · `Ship_in_the_Distance_Ian` 1: 1,0 → **0**,0 · `Spotted_A_Submarine` 1: 0,10 → 0,0 ·
`US_Destroyer_POV` 1: 1,1 → **0**,1 · `fvessel_clip07` 4: 3,13 → **4**,0 · `fvessel_clip09` 3: 2,9 → 2,1.
The fine-tune wins on the FVessel/harbour-style clips it was trained for and on precision everywhere, and loses every open-sea far/small single-vessel clip.
(The baseline's 0.48 here differs from C.md §6b's 0.72 because C.md used its tuned every-frame/relink config and an earlier GT revision; both detectors
above share one config and one GT, which is what matters for the comparison.)

### 6b. B harness, class-agnostic AP50 at the dev-chosen τ (`bench_detectors.py --split test --taus` from §4b)

| detector | SeaShips test (stride 5, 700 img) | R_small / R_med @τ | SMD onboard test (MVI_0797/0801) |
|---|---|---|---|
| yolo11n COCO 640 | 0.461 | 0.33 / 0.61 | 0.817 |
| yolo11n-ft-640 (v1) | **0.958** | **0.00** / 0.75 | 0.554 |

SeaShips AP50 doubles (in-domain), SMD onboard drops, and small-object recall on SeaShips goes to zero — the same story as 6a.

## 7. Integration — nothing wins, so no default changed

* `make demo-models` / `make demo-sidecar` / `python -m edgeml.sidecar` still serve D's `yolo11n-640` (the only `Makefile`/`docs` edits were reverted).
  `python -m edgeml.sidecar` now defaults `--detector` to `$EDGEML_DETECTOR` or `~/models/D/yolo11n-640/model.onnx` instead of failing when the flag is omitted.
* Opt-in artifacts (not referenced by any default): `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/G/yolo11n-ft-640-v1/{model.onnx,meta.json,fp32/,int8/,weights/best.pt}`
  (`meta.json` `class_names: ["boat"]`, so the sidecar's `MARITIME_KEEP` filter and B/C label mapping work unchanged). Verified end-to-end:
  `EDGEML_DETECTOR=~/models/.../yolo11n-ft-640-v1/model.onnx python -m edgeml.sidecar` loads as `yolo11n-ft-640-v1-fp32-640-ort+mobileclip-s1`, and replaying
  the dev clip `A_ship_in_the_distance` (46 frames, 5 fps) through `Analyze` gives 1 sighting (1 OPEN + 44 DUPLICATE events, 46 detections).
* Reusable tooling landed in this PR: `edgeml/tools/g_build_dataset.py` (dev-only YOLO dataset from catalog + MOT sets, `--with-smd`), `g_train.sh`, `g_export.py`
  (ONNX → OpenVINO FP32/INT8 with dev calibration, rewrites `class_names`); `edgeml.runtime.export.export_ultralytics` keeps a fine-tune's own class names
  instead of assuming COCO-80; `edgeml.detectors.zoo.register_extra` (`EDGEML_ZOO_EXTRA=name=/path/best.pt`) lets the B harness score fine-tunes next to the
  zoo; `UltralyticsDetector` no longer applies the COCO `boat` id filter to single-class models.

## 8. Cost

VM `edgeml-g-train` (n1-standard-8 + T4 + 300 GB SSD), 02:43–04:25 UTC ≈ 1.7 h ≈ $1.2; deleted. Export/bench/eval on the local 8-vCPU box.

## 9. Why it failed, and what to do next

* **Training distribution has almost no far/small vessels.** SeaShips (the bulk of the data) is near-shore CCTV with large boats; the GF dev clips that contain
  far targets contribute ~200 boxes. Mosaic + `single_cls` on that mix produced a detector that is very confident on mid/large hulls and silent on a 20-px
  sailboat on the horizon (SeaShips-test `R_small` 0.33 → 0.00, §6b). COCO's `boat` class, trained on far more varied imagery, keeps those.
* **The dev holdout was too small to catch it** (10 objects in 4 clips, none open-sea far targets) — it predicted +0.20 recall, the test gave −0.11. Future
  G-style work should hold out a *far/small-specific* dev slice (e.g. `A_ship_in_the_distance`, `Far_away_but_did_you_see_that_splash`, `Sailing_POV_Corfu_Paxos`)
  rather than train on it, and size the holdout to ≥ 30 objects.
* Cheap next steps, all dev-measurable before another single test run: (1) fine-tune **without** dropping COCO — keep the 80-class head and add the maritime
  data as extra `boat` examples so far-target recall is not unlearned; (2) oversample / tile the far-target frames (SAHI-style crops at train time, 960 or
  1280 crops at inference only on the horizon band); (3) a lower score threshold for the fine-tune (its scores are better calibrated — τ=0.5 on SeaShips vs
  0.25 for COCO) tuned on dev, not on test; (4) use the fine-tune's precision as a *second-stage verifier* for COCO-boat tracks instead of as the detector.
* v2 (+SMD onshore dev) shows the data fix is directionally right for AP50 on other domains (SMD onboard 0.43 → 0.60) but adds shoreline false sightings;
  it was not test-evaluated.
