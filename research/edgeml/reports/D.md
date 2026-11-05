# Workstream D — edge runtime on a laptop-class CPU, frame gate, per-track embedding/subtype, forms

Branch `devin/1793822395-edgeml-d-runtime` → PR into `edgeml/main`. Everything below was measured on **dev** sources only
(`configs/splits.yaml`); the locked `test` sources were not touched. Numbers marked *(local)* come from the dev VM, not
the proxy; everything in the leaderboard / sustained / E2E tables comes from the proxy VM with 4 pinned threads.

## 0. TL;DR

* **The pipeline fits the laptop budget with a wide margin — on the proxy.** Recommended Intel config
  *gate → YOLO11n-416 INT8 (OpenVINO) → ByteTrack → MobileCLIP-S1 INT8 per track → forms* processes the three dev clips at
  **59 processed FPS, 15.8 ms p50 / 25.5 ms p95 per frame, 613 MB peak RSS** on 4 pinned threads (§5). The detector alone
  sustains **120 FPS over 10 minutes (p50 8.1 / p95 9.2 ms)** at 416 px and 66 FPS (14.9 / 17.0 ms) at 640 px (§2.3).
  Scaled by the pessimistic 0.3× laptop factor (§1) that is still ≈18 processed FPS, i.e. ~9× the ≥2 FPS/camera
  requirement; every INT8 config measured, including D-FINE-N-640 and YOLO11s-416, clears it. RSS never exceeded
  0.67 GB of the 2 GB budget.
* **Detector choice.** YOLO11n is the only candidate whose INT8 loss is small (**−1.7 AP50 at 416, −2.2 at 640** on
  SeaShips dev, with the Detect head kept in FP32; NNCF defaults cost −3.8). YOLO26n is as fast but loses 5–7 AP50 under
  INT8; D-FINE-N has the best FP32 accuracy (0.652 AP50 at 640) but loses 7 points at INT8 and is 2× slower; YOLO-World-S
  loses 11; RT-DETR-R18 is 132 ms/frame at 640 → dropped after one measurement (§2). 640 px buys +5 AP50 over 416 for
  1.8× the latency; both fit.
* **Portable path.** ONNX Runtime CPU EP is the primary runtime; OpenVINO + NNCF INT8 is the Intel-only optimisation
  (1.6–2.3× faster than ORT FP32 on this CPU). Engine and execution provider are configuration
  (`engine=auto`, `EDGEML_ORT_PROVIDERS`), FP32 + FP16 ONNX are published next to the INT8 IRs. On Apple Silicon the
  FP16/FP32 ONNX path is expected to match or beat the proxy's OpenVINO FP32 numbers (inferred, §2).
* **Gate** (blank / no-change / pHash, horizon ROI, blur as signal only) costs 2–5 ms and removes 16 % of 1-fps clip
  frames (80 % on a static harbour camera, ~0 on moving bow cameras) with **zero GT-bearing SMD frames dropped**. The
  Reducto-style per-clip threshold saves more but is unsafe on moving cameras, so fixed thresholds ship. Detector
  cadence: 1 Hz floor, 5 Hz while tracks are young/tentative/near the border (`AdaptiveRate`) — 5–9 % of clip time (§3, §5).
* **Embedding / subtype / unknownness.** MobileCLIP-S1 is the best small tower (SeaShips dev zero-shot 0.74 on the 4
  distinguishable classes, 0.62 on 13 prompts; SigLIP-B/16 and OpenCLIP-B/32 are worse and 4× larger). Embedding only at
  track open + key-frame updates means **5–8 embedder calls per 175 frames** (17–24 ms each) — a rounding error in the
  budget; the embedder's real cost is **+330 MB RSS**. Unknownness = MCM ⊕ kNN-to-dev-bank separates detector false
  positives (AUROC 0.98) and sea/sky (0.995) from vessels; MCM alone cannot (0.53). The bank must contain crops from every
  camera (§4).
* **Forms (E1).** `crop`: 1.5× context, 96 px short side, JPEG q50 → **~1.5 KB**, MobileCLIP agrees with the full-res
  subtype on 76 % of crops; `thumb`: 128 px, q75 → ~3 KB, human-readable subtype. 64 px (0.9 KB) keeps "vessel" but
  drops to 66 % agreement; WebP saves 35 % bytes but reads worse at small sizes (§6).
* **Not measured:** the real Dell / MacBook, CoreML EP, D-FINE-S/RT-DETR at 320/416 (dropped as out of scope), locked
  `test` sources (untouched), any labelled accuracy for sightings on the 1-fps clips (unlabelled).

## 1. Laptop proxy and its limits

| | proxy VM | target laptop |
|---|---|---|
| machine | GCP `c3-standard-8` (`edgeml-d-cpu-proxy`, us-central1-a) | Dell Latitude 7230 Rugged Extreme |
| CPU | Intel Xeon Platinum 8481C (Sapphire Rapids) @ 2.7 GHz, 4 physical cores / 8 vCPU, AVX-512 + **AMX-INT8/BF16** | i7-1260U: 2 P-cores + 8 E-cores, 15 W (9–29 W cTDP), AVX2 only (no AVX-512 / AMX on Alder Lake client) |
| RAM | 31 GB | 16 GB shared with the Go edge + other workloads |
| thermal | server, sustained all-core clocks | throttles under sustained load; P-cores at ~4.7 GHz turbo, E-cores ~3.5 GHz |

**Pinning.** All proxy numbers use `taskset -c 0-3` (4 vCPUs = 2 physical cores × 2 SMT threads on c3, i.e. a
pessimistic core count) plus `pin_threads(4)` (OMP/MKL/TBB env, `sched_setaffinity`, `cv2.setNumThreads`, ORT
`intra_op_num_threads=4`, OpenVINO `INFERENCE_NUM_THREADS=4, NUM_STREAMS=1, PERFORMANCE_HINT=LATENCY`).

**Calibration point.** `sysbench cpu --time=20`: **2578 events/s single-thread**, 10 351 events/s on 4 pinned
threads (×4.0). Single-thread detector latency is in §2.3.

**Scaling to the laptop (estimate, not measured).** Per-thread integer/FP throughput of a Golden Cove P-core at
turbo is roughly on par with or slightly above a Sapphire Rapids core at 2.7–3.0 GHz *[recalled from public per-core
benchmarks; ±20 %]*. Two things cut the other way: (a) the laptop has only 2 P-cores, so a 4-thread job runs 2 threads
on E-cores (~0.5–0.6× a P-core per thread *[recalled]*) or shares P-cores via SMT; (b) at 15 W sustained, all-core clocks
fall well below turbo. (c) OpenVINO INT8 on the proxy can use AMX / VNNI-512; Alder Lake has AVX2-VNNI only, so the
INT8 speed-up measured here (§2) is an upper bound. **Working factor: laptop ≈ 0.4–0.7× the proxy's 4-thread
throughput for FP32, 0.3–0.6× for INT8** (uncertainty is dominated by sustained power and the AMX gap). Any config that
needs more than ~50 % of the proxy budget should be considered *not* laptop-safe. A real calibration run on the
Latitude (same `bench.py` command, §7) is the single most valuable follow-up.

## 2. Runtime: detector leaderboard

Code: `edgeml/src/edgeml/runtime/{backends,detectors,export,bench,threads}.py`. One `OnnxDetector` adapter handles
three families (ultralytics YOLO raw/E2E heads, HF RT-DETR / D-FINE, NanoDet-Plus GFL) behind the `Detector`
protocol; boxes come back as pixel `[x,y,w,h]` in original image coordinates.

**Engines and portability.** ONNX Runtime with the CPU execution provider is the *primary, portable* path (any x86 or
Apple Silicon host); OpenVINO is the *Intel-specific optimisation* and the only way to run the NNCF INT8 IRs. The
choice is configuration, not code: `engine=auto` picks ORT for `*.onnx` and OpenVINO for `*.xml`; ORT execution
providers come from `EDGEML_ORT_PROVIDERS` (or `--ort-providers`), e.g. `CPUExecutionProvider` (default),
`CoreMLExecutionProvider,CPUExecutionProvider` on a MacBook, `OpenVINOExecutionProvider,CPUExecutionProvider` with the
Intel ORT build. Published per model under `gs://…/edgeml/models/D/<model>-<size>/`: `model.onnx` (FP32),
`model_fp16.onnx` (FP16 weights, FP32 I/O; loads in stock ORT, −2–10 % latency on the proxy CPU, half the bytes),
`int8/model.{xml,bin}` (OpenVINO IR, NNCF) and `meta.json`. FP16 ONNX could not be produced for D-FINE / NanoDet
(converter output rejected by ORT); FP32 ONNX is there for all.

**Apple Silicon (qualitative, not measured — no Mac in GCP).** The same `model.onnx` runs unchanged via ORT CPU EP; the
M-series performance cores are, per public per-core benchmarks *[recalled, ±25 %]*, ≥1.5× a Sapphire Rapids core at
2.7 GHz and do not throttle in a 14" chassis, so FP32 ORT on 4 M-series threads should land at or above the proxy's
OpenVINO FP32 numbers (§2.1) — i.e. YOLO11n-416 FP32 well under 15 ms and YOLO11n-640 under 30 ms. Adding
`CoreMLExecutionProvider` (ANE/GPU) would cut detector latency further but is untested here; INT8 via NNCF does **not**
transfer (OpenVINO IR is x86-only), so on a Mac the shipped precision is FP16/FP32, which is accuracy-neutral (§2.2).

**Workstream B exports.** B published FP32 ONNX under `gs://…/edgeml/models/<name>/` with an `export.json` sidecar;
`load_detector` reads that sidecar when no `meta.json` is present. B's `yolo11n` loads and detects through the adapter;
B's `dfine-n` ONNX contains a `Cos` op that stock ORT 1.19 CPU has no kernel for (OpenVINO runs it) — a runtime
compatibility note, not a model-quality one.

**INT8 calibration** uses 300 dev frames (`~/data/calib`: 200 SeaShips *train* images, SMD dev frames MVI_0790/0799,
and 1-fps dev clip frames), NNCF `MIXED` preset, fast bias correction. The YOLO Detect head (`/model.23/*`,
`Multiply/Subtract/Sigmoid`) is kept in FP32 (`ignored_scope`), see §2.2 for why.

### 2.1 Leaderboard (proxy, 4 pinned threads, 60 s per config, 1920×1080 SMD frames end-to-end incl. pre/post)

| model | engine | prec | in | ms p50 | ms p95 | infer p50 | FPS | peak RSS MB | MB | iters |
|---|---|---|---|---|---|---|---|---|---|---|
| yolo26n-int8-320-openvino | openvino | int8 | 320 | 5.6 | 5.9 | 4.7 | 177.1 | 564 | 4.9 | 10628 |
| yolo11n-int8-320-openvino | openvino | int8 | 320 | 5.9 | 6.0 | 4.9 | 170.3 | 559 | 5.7 | 10217 |
| yolo26n-fp32-320-openvino | openvino | fp32 | 320 | 6.2 | 6.3 | 5.3 | 161.7 | 544 | 10.0 | 9700 |
| yolo26n-fp16-320-openvino | openvino | fp16 | 320 | 6.3 | 6.6 | 5.4 | 158.6 | 541 | 5.3 | 9518 |
| yolo11n-fp32-320-openvino | openvino | fp32 | 320 | 6.8 | 7.5 | 5.9 | 144.7 | 544 | 10.8 | 8682 |
| yolo11n-fp16-320-openvino | openvino | fp16 | 320 | 6.9 | 7.7 | 5.9 | 143.1 | 540 | 5.6 | 8587 |
| yolo26n-int8-416-openvino | openvino | int8 | 416 | 8.3 | 8.5 | 6.9 | 120.1 | 572 | 5.0 | 7205 |
| dfine-n-int8-320-openvino | openvino | int8 | 320 | 8.4 | 8.8 | 7.7 | 118.3 | 599 | 5.4 | 7098 |
| yolo11s-int8-320-openvino | openvino | int8 | 320 | 8.9 | 9.1 | 8.0 | 111.9 | 583 | 17.1 | 6716 |
| nanodet-plus-m-1.5x-int8-416-openvino | openvino | int8 | 416 | 9.0 | 9.5 | 5.6 | 110.6 | 534 | 1.6 | 6634 |
| yolo11n-int8-416-openvino | openvino | int8 | 416 | 9.5 | 10.1 | 7.8 | 105.9 | 568 | 5.7 | 6354 |
| yolo26n-fp32-320-ort | ort | fp32 | 320 | 9.6 | 10.2 | 8.7 | 102.7 | 495 | 9.8 | 6160 |
| nanodet-plus-m-1.5x-fp32-416-openvino | openvino | fp32 | 416 | 9.7 | 10.0 | 6.5 | 102.9 | 528 | 4.0 | 6174 |
| yolo26n-fp16-416-openvino | openvino | fp16 | 416 | 9.7 | 10.1 | 8.3 | 102.6 | 548 | 5.3 | 6154 |
| yolo26n-fp32-416-openvino | openvino | fp32 | 416 | 9.7 | 10.1 | 8.3 | 102.5 | 552 | 10.0 | 6152 |
| nanodet-plus-m-1.5x-fp16-416-openvino | openvino | fp16 | 416 | 9.8 | 10.1 | 6.6 | 101.8 | 527 | 2.2 | 6111 |
| yolo11n-fp16-416-openvino | openvino | fp16 | 416 | 10.7 | 11.4 | 9.2 | 92.5 | 548 | 5.7 | 5551 |
| yolo11n-fp32-416-openvino | openvino | fp32 | 416 | 11.0 | 11.7 | 9.4 | 90.2 | 552 | 10.8 | 5412 |
| yolo11n-fp32-320-ort | ort | fp32 | 320 | 11.1 | 11.6 | 10.1 | 89.3 | 497 | 10.6 | 5361 |
| dfine-n-int8-416-openvino | openvino | int8 | 416 | 11.3 | 11.9 | 9.9 | 90.2 | 613 | 5.4 | 5410 |
| dfine-n-fp32-320-openvino | openvino | fp32 | 320 | 11.4 | 11.6 | 10.7 | 87.3 | 583 | 15.9 | 5236 |
| dfine-n-fp16-320-openvino | openvino | fp16 | 320 | 12.1 | 12.4 | 11.3 | 83.4 | 581 | 8.6 | 5004 |
| yolov8s-worldv2-int8-320-openvino | openvino | int8 | 320 | 13.0 | 13.3 | 11.8 | 77.5 | 595 | 20.0 | 4652 |
| nanodet-plus-m-1.5x-fp32-416-ort | ort | fp32 | 416 | 13.4 | 13.9 | 10.0 | 74.4 | 475 | 3.8 | 4465 |
| yolo11s-int8-416-openvino | openvino | int8 | 416 | 14.0 | 14.2 | 12.5 | 71.4 | 596 | 17.2 | 4286 |
| yolo26n-fp32-416-ort | ort | fp32 | 416 | 14.4 | 14.9 | 12.8 | 69.1 | 504 | 9.8 | 4145 |
| dfine-n-fp32-416-openvino | openvino | fp32 | 416 | 14.8 | 15.2 | 13.8 | 67.2 | 593 | 15.9 | 4031 |
| dfine-n-fp16-416-openvino | openvino | fp16 | 416 | 15.5 | 16.2 | 14.3 | 64.4 | 592 | 8.6 | 3865 |
| yolo11s-fp16-320-openvino | openvino | fp16 | 320 | 15.9 | 16.5 | 14.6 | 63.1 | 581 | 19.3 | 3786 |
| dfine-n-int8-640-openvino | openvino | int8 | 640 | 16.0 | 16.9 | 13.5 | 62.1 | 636 | 5.5 | 3727 |
| yolo11s-fp32-320-openvino | openvino | fp32 | 320 | 16.2 | 17.3 | 14.9 | 61.5 | 598 | 38.1 | 3690 |
| yolo11n-int8-640-openvino | openvino | int8 | 640 | 17.6 | 19.7 | 14.8 | 55.8 | 588 | 5.8 | 3346 |
| dfine-s-int8-416-openvino | openvino | int8 | 416 | 17.8 | 18.2 | 16.6 | 56.2 | 638 | 12.5 | 3371 |
| yolo26n-int8-640-openvino | openvino | int8 | 640 | 18.0 | 18.3 | 14.7 | 55.5 | 595 | 5.0 | 3328 |
| yolo11n-fp32-416-ort | ort | fp32 | 416 | 18.2 | 19.4 | 16.0 | 54.4 | 507 | 10.6 | 3265 |
| yolov8s-worldv2-fp32-320-openvino | openvino | fp32 | 320 | 20.2 | 21.5 | 19.2 | 49.1 | 622 | 50.2 | 2944 |
| yolov8s-worldv2-fp16-320-openvino | openvino | fp16 | 320 | 20.3 | 20.9 | 19.2 | 49.1 | 600 | 25.3 | 2947 |
| yolo11s-fp32-320-ort | ort | fp32 | 320 | 21.0 | 21.9 | 19.6 | 47.5 | 581 | 37.9 | 2849 |
| yolov8s-worldv2-int8-416-openvino | openvino | int8 | 416 | 21.0 | 21.2 | 19.1 | 47.7 | 611 | 20.0 | 2864 |
| yolo26n-fp32-640-openvino | openvino | fp32 | 640 | 21.1 | 22.0 | 18.0 | 47.3 | 573 | 10.1 | 2838 |
| yolo26n-fp16-640-openvino | openvino | fp16 | 640 | 22.0 | 22.3 | 18.6 | 45.6 | 569 | 5.3 | 2738 |
| yolo11n-fp32-640-openvino | openvino | fp32 | 640 | 24.1 | 25.6 | 20.7 | 41.3 | 572 | 10.9 | 2479 |
| yolo11n-fp16-640-openvino | openvino | fp16 | 640 | 24.2 | 25.4 | 20.7 | 41.2 | 568 | 5.7 | 2471 |
| yolov8s-worldv2-fp32-320-ort | ort | fp32 | 320 | 24.9 | 25.7 | 23.6 | 40.1 | 626 | 50.0 | 2404 |
| yolo11s-fp32-416-openvino | openvino | fp32 | 416 | 25.5 | 26.7 | 23.4 | 39.1 | 608 | 38.1 | 2346 |
| yolo11s-fp16-416-openvino | openvino | fp16 | 416 | 25.9 | 27.0 | 23.8 | 38.5 | 591 | 19.3 | 2309 |
| dfine-n-fp16-640-openvino | openvino | fp16 | 640 | 27.3 | 27.5 | 24.2 | 36.7 | 616 | 8.6 | 2202 |
| dfine-n-fp32-640-openvino | openvino | fp32 | 640 | 27.3 | 27.6 | 24.3 | 36.6 | 618 | 15.9 | 2195 |
| yolo11s-int8-640-openvino | openvino | int8 | 640 | 29.7 | 30.5 | 26.5 | 33.6 | 616 | 17.2 | 2019 |
| dfine-s-int8-640-openvino | openvino | int8 | 640 | 30.9 | 31.5 | 27.9 | 32.4 | 667 | 12.6 | 1943 |
| yolo26n-fp32-640-ort | ort | fp32 | 640 | 32.1 | 32.5 | 28.1 | 31.2 | 564 | 9.9 | 1870 |
| yolov8s-worldv2-fp32-416-openvino | openvino | fp32 | 416 | 32.4 | 33.5 | 30.8 | 30.5 | 634 | 50.2 | 1832 |
| yolov8s-worldv2-fp16-416-openvino | openvino | fp16 | 416 | 33.3 | 33.5 | 31.4 | 30.0 | 611 | 25.3 | 1803 |
| yolo11s-fp32-416-ort | ort | fp32 | 416 | 33.7 | 35.5 | 31.4 | 29.4 | 613 | 38.0 | 1767 |
| yolo11n-fp32-640-ort | ort | fp32 | 640 | 39.4 | 42.0 | 34.9 | 25.1 | 563 | 10.7 | 1504 |
| yolov8s-worldv2-fp32-416-ort | ort | fp32 | 416 | 39.6 | 40.0 | 37.8 | 25.2 | 638 | 50.0 | 1513 |
| yolov8s-worldv2-int8-640-openvino | openvino | int8 | 640 | 39.9 | 40.2 | 36.5 | 25.1 | 637 | 20.1 | 1504 |
| dfine-s-fp16-416-openvino | openvino | fp16 | 416 | 40.0 | 40.3 | 38.5 | 25.0 | 639 | 21.8 | 1501 |
| dfine-s-fp32-416-openvino | openvino | fp32 | 416 | 40.0 | 40.3 | 38.6 | 25.0 | 654 | 42.1 | 1501 |
| yolo11s-fp32-640-openvino | openvino | fp32 | 640 | 55.0 | 56.8 | 51.6 | 18.1 | 637 | 38.2 | 1087 |
| yolo11s-fp16-640-openvino | openvino | fp16 | 640 | 55.8 | 57.2 | 52.2 | 17.9 | 620 | 19.3 | 1076 |
| yolov8s-worldv2-fp16-640-openvino | openvino | fp16 | 640 | 73.0 | 73.4 | 69.5 | 13.7 | 641 | 25.4 | 822 |
| yolov8s-worldv2-fp32-640-openvino | openvino | fp32 | 640 | 73.0 | 73.4 | 69.5 | 13.7 | 663 | 50.3 | 822 |
| dfine-s-fp32-640-openvino | openvino | fp32 | 640 | 74.2 | 74.6 | 71.1 | 13.5 | 687 | 42.1 | 809 |
| dfine-s-fp16-640-openvino | openvino | fp16 | 640 | 74.5 | 75.2 | 71.4 | 13.4 | 673 | 21.8 | 805 |
| yolo11s-fp32-640-ort | ort | fp32 | 640 | 75.9 | 79.0 | 71.4 | 13.2 | 698 | 38.1 | 790 |
| yolov8s-worldv2-fp32-640-ort | ort | fp32 | 640 | 90.8 | 91.8 | 87.0 | 11.0 | 734 | 50.1 | 661 |
| rtdetr-r18-fp16-640-openvino | openvino | fp16 | 640 | 132.0 | 133.4 | 128.9 | 7.6 | 708 | 41.4 | 455 |
| rtdetr-r18-fp32-640-openvino | openvino | fp32 | 640 | 132.2 | 133.5 | 129.1 | 7.6 | 741 | 81.9 | 454 |
| rtdetr-r18-fp32-640-ort | ort | fp32 | 640 | 157.6 | 162.0 | 154.0 | 6.3 | 790 | 82.5 | 380 |

### 2.2 Accuracy: FP32 vs INT8 on dev (class-agnostic "vessel", AP50, recall at score ≥ 0.25)

SeaShips dev = 480 stratified images from the upstream *valid* split (624 boxes, all large/medium, shore CCTV).
SMD dev = MVI_0790 + MVI_0799 (40 frames, 30 boxes, onboard; **tiny sample, treat ±0.1 AP as noise**).

| model | set | prec | AP50 | ΔAP50 pts | recall@0.25 | FP/img | recall by box bucket (n) | GT |
|---|---|---|---|---|---|---|---|---|
| dfine-n-416 | seaships_dev | fp32 | 0.567 |  | 0.771 | 5.07 | large:0.76(561) medium:0.86(63) | 624 |
| dfine-n-416 | seaships_dev | int8 | 0.507 | -6.0 | 0.721 | 5.30 | large:0.72(561) medium:0.73(63) | 624 |
| dfine-n-640 | seaships_dev | fp32 | 0.652 |  | 0.861 | 5.29 | large:0.85(561) medium:0.95(63) | 624 |
| dfine-n-640 | seaships_dev | int8 | 0.579 | -7.3 | 0.825 | 6.54 | large:0.82(561) medium:0.90(63) | 624 |
| dfine-n-640 | smd_dev | fp32 | 0.645 |  | 0.700 | 2.27 | large:0.90(21) medium:0.22(9) | 30 |
| dfine-n-640 | smd_dev | int8 | 0.605 | -4.1 | 0.633 | 1.90 | large:0.86(21) medium:0.11(9) | 30 |
| nanodet-plus-416 | seaships_dev | fp32 | 0.292 |  | 0.412 | 1.26 | large:0.42(561) medium:0.35(63) | 624 |
| nanodet-plus-416 | seaships_dev | int8 | 0.244 | -4.9 | 0.367 | 1.24 | large:0.37(561) medium:0.37(63) | 624 |
| yolo11n-320 | seaships_dev | fp32 | 0.432 |  | 0.385 | 0.23 | large:0.41(561) medium:0.19(63) | 624 |
| yolo11n-320 | seaships_dev | int8 | 0.420 | -1.2 | 0.349 | 0.17 | large:0.38(561) medium:0.11(63) | 624 |
| yolo11n-416 | seaships_dev | fp32 | 0.470 |  | 0.447 | 0.33 | large:0.46(561) medium:0.35(63) | 624 |
| yolo11n-416 | seaships_dev | int8 | 0.453 | -1.7 | 0.410 | 0.27 | large:0.42(561) medium:0.29(63) | 624 |
| yolo11n-416 | smd_dev | fp32 | 0.553 |  | 0.533 | 0.05 | large:0.76(21) medium:0.00(9) | 30 |
| yolo11n-416 | smd_dev | int8 | 0.556 | +0.2 | 0.467 | 0.03 | large:0.67(21) medium:0.00(9) | 30 |
| yolo11n-640 | seaships_dev | fp32 | 0.523 |  | 0.521 | 0.42 | large:0.52(561) medium:0.51(63) | 624 |
| yolo11n-640 | seaships_dev | int8 | 0.501 | -2.2 | 0.492 | 0.40 | large:0.50(561) medium:0.46(63) | 624 |
| yolo11n-640 | smd_dev | fp32 | 0.553 |  | 0.567 | 0.07 | large:0.81(21) medium:0.00(9) | 30 |
| yolo11n-640 | smd_dev | int8 | 0.545 | -0.8 | 0.500 | 0.05 | large:0.71(21) medium:0.00(9) | 30 |
| yolo11s-416 | seaships_dev | fp32 | 0.554 |  | 0.566 | 0.46 | large:0.58(561) medium:0.48(63) | 624 |
| yolo11s-416 | seaships_dev | int8 | 0.562 | +0.8 | 0.556 | 0.43 | large:0.57(561) medium:0.43(63) | 624 |
| yolo11s-416 | smd_dev | fp32 | 0.580 |  | 0.600 | 0.05 | large:0.81(21) medium:0.11(9) | 30 |
| yolo11s-416 | smd_dev | int8 | 0.581 | +0.1 | 0.600 | 0.05 | large:0.81(21) medium:0.11(9) | 30 |
| yolo26n-320 | seaships_dev | fp32 | 0.467 |  | 0.351 | 0.16 | large:0.37(561) medium:0.17(63) | 624 |
| yolo26n-320 | seaships_dev | int8 | 0.400 | -6.7 | 0.276 | 0.11 | large:0.29(561) medium:0.14(63) | 624 |
| yolo26n-416 | seaships_dev | fp32 | 0.504 |  | 0.415 | 0.21 | large:0.43(561) medium:0.27(63) | 624 |
| yolo26n-416 | seaships_dev | int8 | 0.446 | -5.8 | 0.351 | 0.19 | large:0.37(561) medium:0.19(63) | 624 |
| yolo26n-416 | smd_dev | fp32 | 0.544 |  | 0.400 | 0.03 | large:0.57(21) medium:0.00(9) | 30 |
| yolo26n-416 | smd_dev | int8 | 0.506 | -3.8 | 0.300 | 0.03 | large:0.43(21) medium:0.00(9) | 30 |
| yolo26n-640 | seaships_dev | fp32 | 0.553 |  | 0.492 | 0.31 | large:0.49(561) medium:0.48(63) | 624 |
| yolo26n-640 | seaships_dev | int8 | 0.504 | -4.9 | 0.449 | 0.30 | large:0.45(561) medium:0.44(63) | 624 |
| yolo26n-640 | smd_dev | fp32 | 0.583 |  | 0.567 | 0.05 | large:0.81(21) medium:0.00(9) | 30 |
| yolo26n-640 | smd_dev | int8 | 0.532 | -5.0 | 0.367 | 0.03 | large:0.52(21) medium:0.00(9) | 30 |
| yolov8s-world-416 | seaships_dev | fp32 | 0.439 |  | 0.551 | 0.79 | large:0.53(561) medium:0.75(63) | 624 |
| yolov8s-world-416 | seaships_dev | int8 | 0.328 | -11.1 | 0.413 | 0.78 | large:0.38(561) medium:0.70(63) | 624 |

What we learned about quantization (SeaShips dev, yolo11n-416): NNCF defaults (`MIXED`, head quantized) cost
−3.8 AP50; `PERFORMANCE` preset −8.4; keeping the Detect head in FP32 cuts the loss to **−1.7**. All shipped YOLO INT8
models use that recipe. Medium/small boxes (<96² px) lose the most, which matches the literature (§4 of
`literature.md`): zero-shot COCO detectors collapse on distant vessels; INT8 removes what little margin they had.

### 2.3 Sustained runs and single-thread calibration

The 10-minute runs are *faster* than the 60-s leaderboard entries for the same model (e.g. yolo11n-416 INT8 8.1 vs
9.8 ms p50): the proxy warms its caches/clocks over minutes and nothing throttles it. On the laptop the sign flips.

| model | iters | ms p50 | ms p95 | ms mean | FPS | peak RSS MB |
|---|---|---|---|---|---|---|
| yolo11n-int8-416-openvino | 72201 | 8.1 | 9.2 | 8.3 | 120.3 | 579 |
| yolo11n-int8-640-openvino | 39557 | 14.9 | 17.0 | 15.2 | 65.9 | 597 |
| yolo26n-int8-416-openvino | 70451 | 8.4 | 9.0 | 8.5 | 117.4 | 577 |
| yolo26n-int8-640-openvino | 37369 | 15.9 | 17.2 | 16.1 | 62.3 | 597 |

| model | threads | ms p50 | ms p95 | FPS |
|---|---|---|---|---|
| yolo11n-fp32-416-openvino | 1 | 29.1 | 30.4 | 34.2 |
| yolo11n-int8-416-openvino | 1 | 19.7 | 22.0 | 49.9 |

## 3. Frame gate (`runtime/gate.py`)

Signals on a 320-px grey working copy: std / dark-fraction / bright-fraction (blank), Laplacian sharpness (blur),
mean-abs-diff and 64-bit pHash vs the **last processed** frame (no-change), and a Hough-based horizon estimate
(→ ROI band `[x,y,w,h]`). A `force_every_s` floor guarantees the detector still runs on a static scene. Reducto-style
per-clip threshold calibration (`FrameGate.calibrate`) is implemented and measured.

| clip | frames | kept | saved | reasons | cost p50 ms | p95 |
|---|---|---|---|---|---|---|
| 30_Days_at_Sea_Timelapse | 300 | 290 | 3% | {"pass": 290, "blank": 5, "no_change": 5} | 2.9 | 4.5 |
| A_ship_in_the_distance | 9 | 9 | 0% | {"pass": 9} | 4.2 | 4.3 |
| Arriving_In_Norfolk_USA_3hr_uncut | 300 | 60 | 80% | {"blank": 2, "pass": 60, "no_change": 238} | 4.0 | 4.8 |
| Canadian_Coast_Guard_Helicopter_Landing | 30 | 30 | 0% | {"pass": 30} | 4.0 | 6.8 |
| Cargo_Ship_Uncut_View | 300 | 297 | 1% | {"blank": 1, "pass": 297, "no_change": 2} | 1.7 | 2.6 |
| Chase_N_Fish_Sharky | 15 | 15 | 0% | {"pass": 15} | 3.3 | 5.4 |
| Cruise_Ship_POV | 14 | 14 | 0% | {"pass": 14} | 5.8 | 15.7 |
| Cruise_ship_Mediterranean_sunset_off_Italy | 31 | 31 | 0% | {"pass": 31} | 5.1 | 5.5 |
| Dolphins_Pungo_Ferry_Bridge | 5 | 5 | 0% | {"pass": 5} | 2.5 | 2.6 |
| Far_away_but_did_you_see_that_splash | 21 | 21 | 0% | {"pass": 21} | 1.9 | 2.3 |
| Pacific_Ocean_at_night_from_Cruise_Balcony | 15 | 15 | 0% | {"pass": 15} | 3.0 | 5.8 |
| Partially_Obstructed_Balcony | 17 | 17 | 0% | {"pass": 17} | 4.3 | 4.9 |
| Sailboat_on_the_horizon | 300 | 290 | 3% | {"pass": 290, "no_change": 10} | 2.5 | 5.5 |
| Sailing_POV_Corfu_Paxos | 60 | 60 | 0% | {"pass": 60} | 3.5 | 3.8 |
| Ship_crossing_Mariana_Trench | 13 | 13 | 0% | {"pass": 13} | 3.7 | 4.3 |
| Ship_in_the_Distance_Utopia_of_the_Seas | 15 | 15 | 0% | {"pass": 15} | 3.0 | 3.8 |
| Submarine_surfacing | 74 | 74 | 0% | {"pass": 74} | 2.1 | 2.6 |
| Sunrise_Somewhere_in_the_Ocean | 40 | 40 | 0% | {"pass": 40} | 4.0 | 9.7 |
| Tarpon_Offshore | 40 | 40 | 0% | {"pass": 40} | 5.0 | 7.9 |
| Two_Dolphins_near_the_coast_of_Gibraltar | 13 | 13 | 0% | {"pass": 13} | 3.7 | 5.0 |
| Whale_Watching_in_Northern_Peru | 41 | 41 | 0% | {"pass": 41} | 3.2 | 4.2 |

| video | frames | kept | frames w/ GT | dropped w/ GT | unsafe drops | reasons |
|---|---|---|---|---|---|---|
| MVI_0790 | 20 | 20 | 10 | 0 | 0 | {"pass": 20} |
| MVI_0799 | 20 | 20 | 16 | 0 | 0 | {"pass": 20} |

Decisions from the data:
* **Blur never drops a frame.** With `blur_thr=0.15` the gate dropped 5/20 MVI_0799 frames, one with a GT vessel; those
  frames are hazy (sharpness 0.008) but the vessel is there. Blur is kept as a signal (for key-frame quality) only.
* **Blank = flat AND almost all black/saturated**, not either: hazy grey frames with a vessel on the horizon have std < 4.
* **Per-clip calibrated threshold saves more (21 % vs 16 % of frames) but is unsafe on moving clips** (drops 6/74
  Submarine_surfacing frames); on a bow camera the MAD baseline is motion, not content. We ship the fixed threshold.
* Savings are concentrated where expected: 80 % on the static harbour camera (`Arriving_In_Norfolk`), ≤3 % on
  moving/open-sea clips. Cost 2–5 ms p50 per 1080p frame *(local)*. With a 1 Hz detector floor on 1-fps clips the gate
  cannot save more than that; its value is in the 5→1 Hz regime between frames (not measurable on 1-fps data).

## 4. Embedding, zero-shot subtype, unknownness (`embed/`)

`ClipEmbedder` wraps open_clip towers (MobileCLIP2-S0, MobileCLIP-S1, SigLIP-B/16, OpenCLIP ViT-B/32), exports the
**image tower only** to ONNX → OpenVINO fp32/fp16/INT8 (NNCF, 200 SeaShips dev crops) and caches the text bank, so the
laptop never loads torch or the text tower. `ZeroShotClassifier` uses 13 subtypes × 2–4 prompts (mean text prototype per
class) + 5 sea/sky "background" prompts.

| embedder | MB | dim | acc 13-way | acc 4-way | per native class | SMD per label acc(n) |
|---|---|---|---|---|---|---|
| mobileclip-s0 | 46 | 512 | 0.535 | 0.664 | bulk:0.87 container:0.26 fishing:0.28 general:0.80 ore:0.59 passenger:0.35 | Boat:0.60(5) Buoy:0.00(3) Speed boat:0.00(6) Vessel/ship:1.00(16) |
| mobileclip-s1 | 86 | 512 | 0.616 | 0.740 | bulk:0.93 container:0.35 fishing:0.33 general:0.95 ore:0.86 passenger:0.18 | Boat:0.20(5) Buoy:1.00(3) Speed boat:0.00(6) Vessel/ship:1.00(16) |
| openclip-b32 | 351 | 512 | 0.500 | 0.642 | bulk:0.92 container:0.12 fishing:0.23 general:0.86 ore:0.55 passenger:0.25 | Boat:0.40(5) Buoy:0.67(3) Speed boat:0.33(6) Vessel/ship:1.00(16) |
| siglip-b16 | 372 | 768 | 0.543 | 0.682 | bulk:0.57 container:0.62 fishing:0.67 general:0.65 ore:0.39 passenger:0.34 | Boat:0.00(5) Buoy:1.00(3) Speed boat:0.00(6) Vessel/ship:1.00(16) |

#### Confusion (rows=GT subtype, cols=predicted, open 13-way) — mobileclip-s0-torch-fp32
| GT \ pred | cargo shi | container | fishing b | passenger | buoy | coast gua | speedboat | submarine | warship |
|---|---|---|---|---|---|---|---|---|---|
| cargo ship | 237 | 1 | 7 | 36 | 2 | 2 | 8 | 14 | 7 |
| container ship | 57 | 22 | 0 | 6 | 0 | 0 | 0 | 0 | 0 |
| fishing boat | 35 | 0 | 34 | 6 | 3 | 8 | 20 | 8 | 6 |
| passenger ship | 7 | 0 | 0 | 30 | 0 | 10 | 30 | 2 | 6 |

#### Confusion (rows=GT subtype, cols=predicted, open 13-way) — mobileclip-s1-torch-fp32
| GT \ pred | cargo shi | container | fishing b | passenger | buoy | coast gua | speedboat | submarine | tanker | warship |
|---|---|---|---|---|---|---|---|---|---|---|
| cargo ship | 287 | 0 | 6 | 8 | 0 | 0 | 8 | 2 | 2 | 1 |
| container ship | 55 | 30 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| fishing boat | 38 | 0 | 40 | 3 | 1 | 7 | 27 | 2 | 0 | 2 |
| passenger ship | 10 | 0 | 1 | 15 | 0 | 16 | 41 | 1 | 0 | 1 |

#### Confusion (rows=GT subtype, cols=predicted, open 13-way) — openclip-b32-torch-fp32
| GT \ pred | cargo shi | container | fishing b | passenger | aircraft | coast gua | speedboat | submarine | tanker | warship |
|---|---|---|---|---|---|---|---|---|---|---|
| cargo ship | 244 | 0 | 7 | 32 | 0 | 1 | 13 | 14 | 0 | 3 |
| container ship | 72 | 10 | 0 | 0 | 1 | 0 | 2 | 0 | 0 | 0 |
| fishing boat | 45 | 0 | 27 | 3 | 0 | 1 | 35 | 7 | 1 | 1 |
| passenger ship | 8 | 0 | 0 | 21 | 0 | 9 | 41 | 1 | 0 | 5 |

#### Confusion (rows=GT subtype, cols=predicted, open 13-way) — siglip-b16-torch-fp32
| GT \ pred | cargo shi | container | fishing b | passenger | aircraft | buoy | coast gua | marine ma | sailboat | speedboat | submarine | tanker | warship |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| cargo ship | 166 | 1 | 58 | 25 | 3 | 7 | 1 | 1 | 1 | 7 | 3 | 31 | 10 |
| container ship | 28 | 53 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | 0 | 0 |
| fishing boat | 10 | 0 | 80 | 3 | 0 | 3 | 2 | 1 | 0 | 14 | 3 | 2 | 2 |
| passenger ship | 0 | 0 | 5 | 29 | 0 | 0 | 15 | 0 | 1 | 29 | 3 | 1 | 2 |

### 4.1 Unknownness (E5)

`unknownness = ½·MCM + ½·kNN`, with MCM = ½(1−p_max) + ½·gap(best cosine vs dev-calibrated reference) and kNN =
normalised mean distance to the 5 nearest crops of a **dev bank** (even-indexed SeaShips dev crops + even-indexed SMD dev
crops; odd-indexed ones are scored). Negatives: detector boxes with no GT overlap on dev frames/clips ("det-FP",
n=56) and random sea/sky/deck patches (n=90).

| embedder | known SeaShips | known SMD | det-FP unk | AUROC vs SeaShips | vs SMD | sea/sky unk | AUROC | flag@0.5 SeaShips/SMD |
|---|---|---|---|---|---|---|---|---|
| mobileclip-s0 | 0.167 | 0.176 | 0.635 | 0.981 | 0.935 | 0.800 | 0.995 | 0.04/0.07 |
| mobileclip-s1 | 0.137 | 0.182 | 0.588 | 0.984 | 0.940 | 0.703 | 0.995 | 0.03/0.07 |
| openclip-b32 | 0.100 | 0.117 | 0.583 | 0.945 | 0.936 | 0.723 | 0.986 | 0.11/0.07 |
| siglip-b16 | 0.130 | 0.159 | 0.571 | 0.939 | 0.801 | 0.863 | 0.992 | 0.09/0.33 |

* **MCM alone does not separate detector false positives from vessels** (AUROC ≈ 0.53–0.59 for every embedder): a
  glare blob still has "ship" as its most likely prompt. The kNN term does the work (AUROC 0.97–0.99).
* **The bank must span cameras.** Ablation with a SeaShips-only bank: SMD vessels got median unknownness 0.58 and 77 %
  were flagged at 0.5. Adding 15 SMD dev crops fixed it (7 %). On the laptop the bank should be seeded from every
  camera's first confirmed sightings.
* The contact sheet (`reports/D/unknownness_mobileclip-s1.jpg`) shows the "det-FP" set is not pure: a tug and a dredger
  on the cargo-ship clip score **low** (0.32–0.42, correct), night deck lights score 0.9. Flag rates on known dev
  vessels at threshold 0.5: 3 % (SeaShips) / 7 % (SMD).
* Recommendation to WS C stands: unknownness is a *bonus* in `value`, gated on track age ≥ 2 s, threshold 0.6.

## 5. End-to-end CPU budget

`runtime/e2e.py`: gate → INT8 detector → ByteTrack (`supervision` 0.26.1, adapter in `runtime/tracker.py`) →
`TrackSightings` (open / material-update / close; embedding + subtype **once per track at open and on key-frame
updates**: box area change ≥1.5×, score jump ≥0.2, or 30 s) → `forms` encoded only for open/update events.

Three dev clips at 1 fps (`Submarine_surfacing` 74 frames, `Sailing_POV_Corfu_Paxos` 60, `Whale_Watching_in_Northern_Peru`
41; 1080p), 4 pinned threads, detector floor 1 Hz / max 5 Hz, MobileCLIP INT8 IR for the embedder (the raw JSON labels the
embedder `fp32` because of a naming bug fixed in this PR; the `int8/` directories were loaded). "none / n" rows run the
detector + tracker + forms only, without gate or embedder, to isolate their cost. "laptop est." applies the pessimistic
0.3× factor from §1 to the proxy FPS.

| detector | embedder | gate | frames (3 clips) | gated out | frame ms p50 (wtd) | worst p95 | det ms p50 | proc FPS | peak RSS MB | sightings / updates | embed calls (ms each) | forms calls (ms each) | mean det Hz / % at 5 Hz | proxy budget | laptop est. (×0.3) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| dfine-n-int8-640-openvino | mobileclip-s1-int8 | y | 175 | 0 | 25.9 | 57.4 | 18.1 | 36.4 | 668 | 5 / 3 | 8 (23.7) | 8 (8.8) | 1.34 / 9 % | yes | 11 FPS ok |
| yolo11n-int8-416-openvino | mobileclip-s0-int8 | y | 175 | 0 | 15.8 | 23.4 | 8.9 | 60.0 | 550 | 3 / 2 | 5 (17.2) | 5 (8.7) | 1.25 / 6 % | yes | 18 FPS ok |
| yolo11n-int8-416-openvino | mobileclip-s1-int8 | y | 175 | 0 | 15.8 | 25.5 | 8.9 | 59.3 | 613 | 3 / 2 | 5 (22.8) | 5 (8.8) | 1.25 / 6 % | yes | 18 FPS ok |
| yolo11n-int8-416-openvino | none | n | 175 | 0 | 12.9 | 17.4 | 9.0 | 75.5 | 272 | 3 / 2 | 0 (0.0) | 5 (8.7) | 1.25 / 6 % | yes | 23 FPS ok |
| yolo11n-int8-640-openvino | mobileclip-s0-int8 | y | 175 | 0 | 25.9 | 35.2 | 18.6 | 37.1 | 564 | 4 / 2 | 6 (18.0) | 6 (8.9) | 1.30 / 7 % | yes | 11 FPS ok |
| yolo11n-int8-640-openvino | mobileclip-s1-int8 | y | 175 | 0 | 25.9 | 37.4 | 18.6 | 36.9 | 628 | 4 / 2 | 6 (22.9) | 6 (8.9) | 1.30 / 7 % | yes | 11 FPS ok |
| yolo11n-int8-640-openvino | none | n | 175 | 0 | 22.5 | 26.9 | 18.8 | 43.7 | 284 | 4 / 2 | 0 (0.0) | 6 (8.8) | 1.30 / 7 % | yes | 13 FPS ok |
| yolo11s-int8-416-openvino | mobileclip-s0-int8 | y | 175 | 0 | 21.4 | 44.4 | 14.3 | 44.2 | 578 | 4 / 3 | 7 (18.2) | 7 (8.7) | 1.30 / 7 % | yes | 13 FPS ok |
| yolo11s-int8-416-openvino | mobileclip-s1-int8 | y | 175 | 0 | 21.4 | 50.8 | 14.3 | 43.7 | 641 | 4 / 3 | 7 (23.7) | 7 (8.7) | 1.30 / 7 % | yes | 13 FPS ok |
| yolo11s-int8-416-openvino | none | n | 175 | 0 | 18.2 | 25.0 | 14.2 | 53.8 | 300 | 4 / 3 | 0 (0.0) | 7 (8.7) | 1.30 / 7 % | yes | 16 FPS ok |
| yolo26n-int8-416-openvino | mobileclip-s0-int8 | y | 175 | 0 | 15.3 | 37.1 | 8.4 | 61.9 | 553 | 3 / 2 | 5 (17.3) | 5 (7.0) | 1.21 / 5 % | yes | 19 FPS ok |
| yolo26n-int8-416-openvino | mobileclip-s1-int8 | y | 175 | 0 | 15.4 | 43.9 | 8.4 | 60.9 | 617 | 3 / 2 | 5 (23.2) | 5 (7.0) | 1.21 / 5 % | yes | 18 FPS ok |
| yolo26n-int8-416-openvino | none | n | 175 | 0 | 11.8 | 18.1 | 8.3 | 82.4 | 275 | 3 / 2 | 0 (0.0) | 5 (6.9) | 1.21 / 5 % | yes | 25 FPS ok |
| yolo26n-int8-640-openvino | mobileclip-s0-int8 | y | 175 | 0 | 24.4 | 34.4 | 17.2 | 39.2 | 572 | 4 / 2 | 6 (17.8) | 6 (9.0) | 1.27 / 7 % | yes | 12 FPS ok |
| yolo26n-int8-640-openvino | mobileclip-s1-int8 | y | 175 | 0 | 24.5 | 36.4 | 17.2 | 38.9 | 635 | 4 / 2 | 6 (23.3) | 6 (8.9) | 1.27 / 7 % | yes | 12 FPS ok |
| yolo26n-int8-640-openvino | none | n | 175 | 0 | 21.0 | 25.7 | 17.2 | 46.6 | 293 | 4 / 2 | 0 (0.0) | 6 (8.7) | 1.27 / 7 % | yes | 14 FPS ok |

Reading the table: the detector is 55–70 % of frame time; the embedder adds ~3 ms *per frame on average* (5–8 calls
per clip set at 17–24 ms each) and **+330 MB RSS**; the gate costs ~0 on these moving-camera clips (it never fired) and
would only pay off on static cameras (§3). Sightings: 3–5 per clip set with 2–3 material updates — e.g. one submarine
sighting with surfacing updates, one sailboat, nothing on the whale clip (COCO detectors do not see cetaceans; the
cheap path to that is the unknownness branch on a motion/horizon-anomaly proposer, out of scope here). The frame-level
p95 spikes (37–57 ms) are the frames where open + embed + forms coincide.

| embedder | prec | ms/crop p50 | p95 | ms/crop batch8 | MB | RSS MB |
|---|---|---|---|---|---|---|
| mobileclip-s0 | fp32 | 16.2 | 16.6 | 16.2 | 46 | 663 |
| mobileclip-s0 | fp16 | 16.4 | 16.7 | 16.3 | 24 | 654 |
| mobileclip-s0 | int8 | 16.2 | 16.4 | 16.3 | 14 | 651 |
| mobileclip-s1 | fp32 | 28.9 | 29.2 | 28.8 | 87 | 791 |
| mobileclip-s1 | fp16 | 28.2 | 28.4 | 28.0 | 44 | 742 |
| mobileclip-s1 | int8 | 22.1 | 22.5 | 20.8 | 25 | 733 |
| siglip-b16 | fp32 | 77.9 | 78.2 | 77.7 | 370 | 1322 |
| siglip-b16 | fp16 | 77.7 | 78.7 | 77.5 | 185 | 1157 |
| siglip-b16 | int8 | 19.9 | 20.1 | 19.7 | 94 | 832 |
| openclip-b32 | fp32 | 26.7 | 27.0 | 26.8 | 352 | 1305 |
| openclip-b32 | fp16 | 26.2 | 26.4 | 26.2 | 176 | 1131 |
| openclip-b32 | int8 | 8.3 | 8.5 | 8.3 | 90 | 840 |

## 6. Bandwidth forms (`forms.py`) and the E1 legibility grid

`encode_forms` produces `fact` (compact JSON), `crop` (box × context, max side, JPEG/WebP q), `thumb` (key frame, max
side 256) and `full` with exact `bytes`, `w`, `h`, `quality`, `codec`, `src_box`.

MobileCLIP-S1, margin 1.5× (all 60 crops; the full 128-cell grid for S0 and S1 is in `reports/D/e1_grid.md`):

| margin | short side | codec | q | bytes p50 | agree w/ full-res | acc | encode ms |
|---|---|---|---|---|---|---|---|
| 1.5 | 48 | jpeg | 40 | 588 | 0.57 | 0.42 | 0.04 |
| 1.5 | 48 | jpeg | 50 | 634 | 0.62 | 0.47 | 0.04 |
| 1.5 | 48 | jpeg | 75 | 810 | 0.55 | 0.50 | 0.05 |
| 1.5 | 48 | webp | 50 | 351 | 0.53 | 0.42 | 0.28 |
| 1.5 | 64 | jpeg | 40 | 753 | 0.65 | 0.50 | 0.05 |
| 1.5 | 64 | jpeg | 50 | 824 | 0.68 | 0.58 | 0.05 |
| 1.5 | 64 | jpeg | 75 | 1108 | 0.70 | 0.53 | 0.06 |
| 1.5 | 64 | webp | 50 | 502 | 0.63 | 0.50 | 0.42 |
| 1.5 | 96 | jpeg | 40 | 1192 | 0.73 | 0.55 | 0.08 |
| 1.5 | 96 | jpeg | 50 | 1338 | 0.75 | 0.60 | 0.09 |
| 1.5 | 96 | jpeg | 75 | 1926 | 0.83 | 0.60 | 0.10 |
| 1.5 | 96 | webp | 50 | 893 | 0.67 | 0.50 | 0.88 |
| 1.5 | 128 | jpeg | 40 | 1682 | 0.78 | 0.57 | 0.11 |
| 1.5 | 128 | jpeg | 50 | 1926 | 0.82 | 0.55 | 0.13 |
| 1.5 | 128 | jpeg | 75 | 2850 | 0.88 | 0.58 | 0.14 |
| 1.5 | 128 | webp | 50 | 1314 | 0.80 | 0.57 | 1.28 |

Agreement with the full-res prediction averaged over the JPEG grid — by margin: 1.0×: 0.69 · 1.25×: 0.73 · 1.5×: 0.71 · 2.0×: 0.69; by short side (median bytes): 48px: 0.60 (669 B) · 64px: 0.66 (901 B) · 96px: 0.76 (1495 B) · 128px: 0.81 (2211 B); by quality: q40: 0.67 · q50: 0.70 · q75: 0.75.

**Recommended defaults** (from the grid above; bytes are medians over 60 SeaShips dev crops):

| form | setting | median bytes | what it buys |
|---|---|---|---|
| `crop` (machine + human) | 1.5× box context, **short side 96 px**, JPEG **q50** | ~1.3–1.5 KB | MobileCLIP-S1 agrees with the full-res subtype on ~76 % of crops; a human can tell hull type and colour |
| `crop` minimum (ML only) | 1.5×, 64 px, q50 | ~0.9 KB | ~66 % agreement: enough to confirm "vessel" and coarse class, not subtype |
| `thumb` (human key frame) | 1.5×, **128 px**, JPEG **q75** | ~2.5–3 KB | ~81 % agreement at q50, visibly cleaner at q75; subtype readable by eye |
| `full` | key frame, max side 1280, q75 | 100–250 KB | only on explicit request |

Margin: 1.25–1.5× context is best (1.0× clips hulls, 2.0× dilutes the object at small short sides). WebP q50 is ~35 %
smaller than JPEG q50 at equal size but 5–8× slower to encode (0.3 vs 0.05 ms — both negligible) and agrees *less*
with the full-res prediction at 48–64 px, so JPEG stays the default. The literature's expected answer (1.5×, ≥64 px ML /
≥96–128 px human, q≈50 crop / q≈75 thumb) is confirmed; the ML floor is softer than expected (64 px costs 10 points of
agreement vs 96 px), so 96 px is the recommended `crop` default. The legibility read-off was done by eye on the
contact sheets `reports/D/e1_contact_mobileclip-s1.jpg` (60 crops × 4 sizes); it is a spot-check, not a blinded study.

## 7. Reproduce

```bash
# env (local or proxy)
python -m venv ~/venv && ~/venv/bin/pip install -e edgeml ultralytics transformers open_clip_torch openvino nncf onnxruntime "supervision==0.26.1" psutil
# export + INT8 (dev calibration frames in ~/data/calib)
python -m edgeml_research.runtime.export --out ~/models/export --models yolo11n yolo11s yolo26n yolov8s-world rtdetr-r18 dfine-s dfine-n nanodet-plus --sizes 320 416 640 --calib ~/data/calib --n-calib 300 --weights-dir ~/models
python edgeml/tools/d_export_embed.py --out ~/models/clip --calib-ann ~/data/catalog/ann/seaships7000.jsonl --calib-ids ~/data/seaships_calib_ids.txt --calib-images ~/data/seaships/calib
# proxy leaderboard (4 pinned threads), sustained, single-thread calibration
bash edgeml/tools/d_bench_all.sh ~/models/export ~/data/smd/all results/bench60 60 0-3 4
taskset -c 0-3 python -m edgeml_research.runtime.bench --model ~/models/export/yolo11n-416/int8/model.xml --engine openvino --frames ~/data/smd/all --threads 4 --duration 600 --out results/sustained/yolo11n-416-int8-600s.json
taskset -c 0   python -m edgeml_research.runtime.bench --model ~/models/export/yolo11n-416/int8/model.xml --engine openvino --frames ~/data/smd/all --threads 1 --duration 60
# accuracy, gate, zero-shot / E5, E1
python edgeml/tools/d_eval_det.py --export ~/models/export --images ~/data/seaships/dev --ann ~/data/catalog/ann/seaships7000.jsonl --ids ~/data/seaships_dev_ids.txt --set seaships_dev --out results/det_acc.jsonl
python edgeml/tools/d_eval_gate.py --clips ~/data/clips/* --smd-images ~/data/smd/all --smd-ann ~/data/catalog/ann/smd_vis_onboard_sample.jsonl --videos MVI_0790 MVI_0799 --out results/gate.json
# zero-shot subtype + E5 unknownness (torch fp32 towers; det-FP negatives mined with the yolo11n-416 fp32 IR on three dev clips)
python edgeml/tools/d_eval_zeroshot.py --embedders mobileclip-s0 mobileclip-s1 siglip-b16 openclip-b32 --seaships --smd --e5 \
  --seaships-ann ~/data/catalog/ann/seaships7000.jsonl --seaships-ids ~/data/seaships_dev_ids.txt --seaships-images ~/data/seaships/dev \
  --smd-ann ~/data/catalog/ann/smd_vis_onboard_sample.jsonl --smd-images ~/data/smd/all --smd-videos MVI_0790 MVI_0799 \
  --det ~/models/export/yolo11n-416/fp32/model.xml --clips ~/data/clips/Cargo_Ship_Uncut_View ~/data/clips/Sailing_POV_Corfu_Paxos ~/data/clips/30_Days_at_Sea_Timelapse \
  --max-per-class 120 --context 1.5 --out results/zeroshot3
# E5 ablation (SeaShips-only kNN bank): same command with --embedders mobileclip-s1 --e5-seaships-bank-only --out results/zeroshot3_ablation
# E1 crop grid (60 SeaShips dev crops)
python edgeml/tools/d_eval_zeroshot.py --embedders mobileclip-s0 mobileclip-s1 --seaships --e1 --e1-n 60 \
  --seaships-ann ~/data/catalog/ann/seaships7000.jsonl --seaships-ids ~/data/seaships_dev_ids.txt --seaships-images ~/data/seaships/dev --out results/e1
# end to end
taskset -c 0-3 python -m edgeml_research.runtime.e2e --det ~/models/export/yolo11n-416/int8/model.xml --embed ~/models/clip/mobileclip-s1/int8 --clips ~/data/clips/Submarine_surfacing ~/data/clips/Sailing_POV_Corfu_Paxos ~/data/clips/Whale_Watching_in_Northern_Peru --detect-hz 1 --threads 4 --out results/e2e/yolo11n-416-mobileclip-s1-int8.json --events-out results/e2e/yolo11n-416-mobileclip-s1-int8.jsonl
# tables for this report
python edgeml/tools/d_report_tables.py --bench results/bench_merged --acc results/det_acc*.jsonl --gate results/gate.json --zeroshot results/zeroshot3 --e1 results/e1 --e2e results/e2e --embed results/embed/embed_cost.json --sustained results/sustained --calib results/calib
```

Run artefacts: ``gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/` —
`20261104-d-cpu-leaderboard/` (bench60 + bench60b raw JSON, single-thread `calib/`, VM queue logs),
`20261104-d-sustained/` (four 600-s runs), `20261104-d-e2e/` (per-clip JSON + sighting event JSONL for 16 configs),
`20261104-d-int8-accuracy/`, `20261104-d-gate/`, `20261104-d-zeroshot-e5/` (incl. unknownness contact sheets),
`20261104-d-e1-forms/`, `20261104-d-embed-cost/`. Models: `gs://…/edgeml/models/D/<model>-<size>/{model.onnx,model_fp16.onnx,int8/,meta.json}`
and `gs://…/edgeml/models/D/clip/mobileclip-{s0,s1}/{fp32,fp16,int8}/` (image tower + `text_cache.json`). Each run dir has a `config.json` with the git sha and hardware.` (raw JSON/JSONL for every table, event logs, contact sheets, the shipped INT8 models under
`.../models/`).

## 8. Honest limits / open items

* Proxy ≠ laptop (§1). No measurement on a U-series part; the 0.4–0.7× factor is an estimate.
* SMD dev is 30 boxes; the SeaShips dev numbers carry the INT8 conclusions. SeaShips is shore CCTV of large ships: it
  says little about distant small targets, where every detector here has ~0 recall below 96² px without tiling.
* Clips are unlabelled and 1 fps: E2E sightings counts are plausibility checks, not accuracy; the 1–5 Hz adaptive rate
  can only be costed (per-frame ms), not exercised.
* The "det-FP" negatives for E5 on unlabelled clips contain some real vessels (visible in the contact sheet).
* The leaderboard uses our own exports (same upstream checkpoints as workstream B); B's ONNX exports appeared late in
  the run and were only smoke-tested through the adapter (§2), not re-benchmarked.
* Apple Silicon numbers are inferred, not measured; CoreML EP untested (out of scope by instruction).
* `model_fp16.onnx` is missing for D-FINE and NanoDet (FP16 converter output rejected by ORT); their FP32 ONNX is published.
* Proposed contract tweaks (not applied): add `mcm`/`knn` components and `embedder` id to the sighting record; make
  `forms[].codec` and `src_box` official (already emitted).
