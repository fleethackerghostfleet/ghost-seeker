# Maritime model bake-off — morning extension

**UNCLASSIFIED / public-data prototype evaluation. No operational or field-accuracy claims.**
Measured overnight 4–5 November 2026. New results extend [B](B.md), [C](C.md), [D](D.md), and [E](E.md); they do not replace their measurements. All new local inference is **CPU FP32**, four pinned CPUs/threads. **No new GPU result exists.**

## 1. Decision brief

- **Edge: retain D's optimized YOLO11n + MobileCLIP-S1 pipeline.** D measured OpenVINO INT8 end-to-end on its separate CPU proxy; the new raw-PyTorch speed tests do not establish a better quantized, memory-bounded, sustained edge pipeline.
- **Shore / known-vessel box teacher: RF-DETR-Small, selected on dev only.** It leads the new candidates on mean SeaShips/SMD dev AP50. Use it for proposed boxes with human review, not automatic ground truth. RT-DETRv2-R34 remains a useful alternative; rankings differ by dataset and IoU requirement.
- **Subtype suggestions:** MobileCLIP-S2 leads the six new models on dev 13-prompt accuracy, but only slightly beats the matched S1 baseline and loses to S1 on restricted-four dev accuracy. Retain S1 at the edge; S2 is an optional shore comparison, not a demonstrated identification solution.
- **Hosted localization:** this particular schema/prompt favors Gemini Pro over Flash, but Pro is slow and local RF-DETR is stronger here. These results are **not general vision ratings**. No hosted API is recommended for the edge.
- **Far contacts remain unproven:** the dev detector sample has only one small box and no tiny boxes; the SMD test has neither. Do not turn the small-box recall column into an operational claim.

## 2. Protocol and hardware

Detectors reuse `bench_detectors.py` and its unchanged evaluation code through `tools/bakeoff.py`. Scores below 0.05 are filtered as in B. AP50 and AP50–95 use the vessel/class-agnostic protocol; recall/precision/FP columns use IoU 0.5 at the frozen threshold. Thresholds maximize dev F1 over 0.05…0.95 in 0.05 steps, separately per dataset. The completed dev thresholds were saved before locked-test execution. The ranking is the **unweighted mean of SeaShips and SMD dev AP50**, not test AP50, not latency, and not a significance claim. Detector selection was recorded before reading test metrics. Every new detector ran the SMD test once; only the already-dev-selected RF-Small subsequently ran the full SeaShips test once. Classifier prompts/context were fixed from D; all six new models and one matched S1 baseline were evaluated once on their locked splits. No test-based prompt or threshold changes were made.

Hardware: session VM, Intel Xeon virtual CPU, 8 visible vCPUs, **CPUs 0–3 pinned / 4 threads**, no GPU; Python 3.10.12, torch 2.6.0+cpu, torchvision 0.21.0+cpu. Models were run serially. Ultralytics normally resets torch thread count; this runner fixes its CPU `NUM_THREADS` to 4 before device selection. Detections use batch 1. CPU latency is `Detector.detect()` wall time including preprocessing/postprocessing, excluding disk read and weight download/load, with the first five samples excluded. Hosted latency is remote end-to-end request timing, including client encoding/pre/postprocessing; it is neither CPU nor GPU model inference. Classifier timing is mean torch embedding time per crop in the existing evaluator (batches of 16), not p50 batch-one latency. The proxy edge budget is ≤4 threads, ≤2GB RSS and ≥2 processed FPS/camera; latency alone does not establish compliance. New model-specific peak RSS, thermal/sustained performance, quantized exports, and actual target-laptop latency were not measured. No hardware-scaling estimates are used below.

| set / role | images / crops | vessel GT | tiny / small / medium / large | vessel-sea-only frames |
|---|---|---|---|---|
| SeaShips detector dev, stride5 | 700 | 905 | 0 / 1 / 72 / 832 | 0 |
| SMD detector dev | 40 | 27 | 0 / 0 / 6 / 21 | 17 |
| SMD locked detector test | 40 | 72 | 0 / 0 / 20 / 52 | 0 |
| SeaShips selected RF-Small locked detector test | 3500 | 4683 | 0 / 9 / 426 / 4248 | 0 |
| SeaShips classifier dev | 720 labelled crops | 6 native → 4 coarse types | balanced:120 per native class | N/A |
| SeaShips locked classifier test | 4,683 labelled crops | 6 native → 4 coarse types | all available locked-test labelled crops | N/A |

Area buckets use original-image pixel area: tiny <16², small <32², medium 32²…<96², large ≥96². **Small includes tiny**, per CONTRACTS. A “sea-only” frame here means no **vessel** GT, not a certified empty scene: SMD has 17 vessel-empty dev frames versus 14 all-object-empty frames. No SMD test sea-only frames means test FP/sea is **N/A**, never zero. Singapore Maritime remains excluded because its annotation joins are invalid. SeaShips uses the catalog/upstream split, not a newly established independent-camera split. SMD uses the locked source-video split in `configs/splits.yaml`.

## 3. Ranked new local detectors — dev

AP cells are **AP50 / AP50–95**. CPU latency is SeaShips p50 / p95 ms. Parameters are measured inference-model counts in millions, not checkpoint file size. Native resolutions are retained for RF-DETR, OWL-ViT, and Florence, so this is an obtainable-checkpoint comparison, not an equal-input-size architecture experiment.

| rank | model | params M | native input | SeaShips dev AP | SMD dev AP | mean dev AP50 | CPU ms p50 / p95 |
|---|---|---|---|---|---|---|---|
| 1 | rfdetr-small | 32.11 | 512 | 0.740 / 0.454 | 0.756 / 0.382 | 0.748 | 195.0 / 205.3 |
| 2 | rfdetr-nano | 30.47 | 384 | 0.738 / 0.439 | 0.729 / 0.352 | 0.733 | 121.6 / 125.7 |
| 3 | gdino-base | 232.31 | 640 | 0.700 / 0.421 | 0.693 / 0.428 | 0.696 | 4626.5 / 4733.7 |
| 4 | rtdetrv2-r34 | 31.42 | 640 | 0.660 / 0.355 | 0.713 / 0.412 | 0.687 | 305.9 / 336.5 |
| 5 | rfdetr-base | 32.17 | 560 | 0.656 / 0.392 | 0.716 / 0.417 | 0.686 | 292.0 / 304.2 |
| 6 | owlvit-b32 | 153.23 | 768×768 | 0.657 / 0.323 | 0.692 / 0.401 | 0.675 | 385.0 / 441.8 |
| 7 | conditional-detr-r50 | 43.42 | 640 | 0.578 / 0.285 | 0.687 / 0.387 | 0.633 | 378.8 / 432.7 |
| 8 | deformable-detr | 40.07 | 640 | 0.631 / 0.321 | 0.625 / 0.357 | 0.628 | 1264.4 / 1331.5 |
| 9 | yolov9s | 7.32 | 640 | 0.556 / 0.321 | 0.663 / 0.407 | 0.610 | 78.6 / 119.5 |
| 10 | owlvit-b16 | 152.79 | 768×768 | 0.542 / 0.278 | 0.671 / 0.384 | 0.606 | 3099.2 / 3201.9 |
| 11 | yolo12s | 9.29 | 640 | 0.535 / 0.303 | 0.673 / 0.384 | 0.604 | 65.4 / 132.6 |
| 12 | yolov10s | 8.13 | 640 | 0.499 / 0.274 | 0.601 / 0.357 | 0.550 | 72.7 / 110.8 |
| 13 | detr-r50 | 41.52 | 640 | 0.503 / 0.242 | 0.596 / 0.342 | 0.549 | 333.9 / 400.1 |
| 14 | yolo12n | 2.60 | 640 | 0.435 / 0.241 | 0.585 / 0.324 | 0.510 | 33.1 / 40.6 |
| 15 | yolov9t | 2.13 | 640 | 0.425 / 0.234 | 0.577 / 0.332 | 0.501 | 50.7 / 74.9 |
| 16 | yolov10n | 2.78 | 640 | 0.387 / 0.219 | 0.473 / 0.294 | 0.430 | 26.3 / 32.8 |
| 17 | florence2-base-ft | 231.44 | 768×768 | 0.483 / 0.285 | 0.216 / 0.141 | 0.350 | 1543.8 / 1781.4 |

### Size recall and false sightings at frozen dev thresholds

All tiny recall is N/A. **SeaShips small recall has denominator ONE** and is not a reliable ranking signal. SMD dev medium recall has denominator six. SeaShips FP/sea is N/A for every model because there are no sea-only frames.

| model | τ Sea / SMD | Sea R small / med / large | SMD R med / large | SMD FP / vessel-empty frame (17) |
|---|---|---|---|---|
| rfdetr-small | 0.60 / 0.30 | 1.000 / 0.736 / 0.638 | 0.833 / 0.810 | 0.000 |
| rfdetr-nano | 0.50 / 0.40 | 1.000 / 0.750 / 0.681 | 0.000 / 0.905 | 0.000 |
| gdino-base | 0.35 / 0.40 | 1.000 / 0.778 / 0.722 | 0.000 / 0.810 | 0.000 |
| rtdetrv2-r34 | 0.65 / 0.75 | 1.000 / 0.736 / 0.632 | 0.000 / 0.762 | 0.000 |
| rfdetr-base | 0.65 / 0.35 | 1.000 / 0.750 / 0.558 | 0.167 / 0.857 | 0.000 |
| owlvit-b32 | 0.10 / 0.05 | 0.000 / 0.208 / 0.548 | 0.000 / 0.905 | 0.000 |
| conditional-detr-r50 | 0.40 / 0.55 | 0.000 / 0.750 / 0.554 | 0.000 / 0.810 | 0.000 |
| deformable-detr | 0.45 / 0.60 | 1.000 / 0.708 / 0.593 | 0.000 / 0.762 | 0.000 |
| yolov9s | 0.35 / 0.25 | 0.000 / 0.444 / 0.498 | 0.167 / 0.810 | 0.000 |
| owlvit-b16 | 0.05 / 0.05 | 0.000 / 0.375 / 0.725 | 0.000 / 0.905 | 0.000 |
| yolo12s | 0.35 / 0.25 | 0.000 / 0.556 / 0.514 | 0.167 / 0.810 | 0.000 |
| yolov10s | 0.20 / 0.20 | 0.000 / 0.583 / 0.552 | 0.167 / 0.762 | 0.000 |
| detr-r50 | 0.95 / 0.95 | 1.000 / 0.694 / 0.459 | 0.000 / 0.810 | 0.176 |
| yolo12n | 0.25 / 0.15 | 0.000 / 0.556 / 0.438 | 0.000 / 0.714 | 0.000 |
| yolov9t | 0.20 / 0.15 | 1.000 / 0.583 / 0.434 | 0.000 / 0.714 | 0.000 |
| yolov10n | 0.15 / 0.30 | 1.000 / 0.431 / 0.431 | 0.000 / 0.476 | 0.000 |
| florence2-base-ft | 0.05 / 0.05 | 0.000 / 0.236 / 0.617 | 0.167 / 0.381 | 0.235 |

## 4. Locked detector tests — confirmation, not model selection

### SMD: all 17 candidates, 40 frames / 72 vessel boxes

Rows remain in the **dev** ranking order. No small/tiny GT and no sea-only test frames; those metrics are N/A throughout. FP/image is provided but must not be relabelled FP/sea.

| model | frozen τ | AP50 / AP50–95 | R@τ | P@τ | R med / large | FP/image | CPU p50 / p95 ms |
|---|---|---|---|---|---|---|---|
| rfdetr-small | 0.30 | 0.913 / 0.491 | 0.889 | 0.877 | 0.700 / 0.962 | 0.225 | 196.2 / 207.6 |
| rfdetr-nano | 0.40 | 0.858 / 0.455 | 0.847 | 0.897 | 0.550 / 0.962 | 0.175 | 122.2 / 125.9 |
| gdino-base | 0.40 | 0.824 / 0.501 | 0.736 | 0.855 | 0.150 / 0.962 | 0.225 | 5199.2 / 5266.5 |
| rtdetrv2-r34 | 0.75 | 0.908 / 0.509 | 0.681 | 0.961 | 0.100 / 0.904 | 0.050 | 290.4 / 302.7 |
| rfdetr-base | 0.35 | 0.893 / 0.486 | 0.847 | 0.871 | 0.550 / 0.962 | 0.225 | 291.1 / 295.1 |
| owlvit-b32 | 0.05 | 0.640 / 0.357 | 0.694 | 0.735 | 0.200 / 0.885 | 0.450 | 374.9 / 382.3 |
| conditional-detr-r50 | 0.55 | 0.751 / 0.395 | 0.583 | 0.933 | 0.150 / 0.750 | 0.075 | 313.1 / 326.6 |
| deformable-detr | 0.60 | 0.853 / 0.443 | 0.611 | 0.957 | 0.000 / 0.846 | 0.050 | 1160.4 / 1180.2 |
| yolov9s | 0.25 | 0.839 / 0.461 | 0.833 | 0.822 | 0.600 / 0.923 | 0.325 | 70.4 / 73.1 |
| owlvit-b16 | 0.05 | 0.523 / 0.296 | 0.528 | 0.792 | 0.250 / 0.635 | 0.250 | 3036.3 / 3086.3 |
| yolo12s | 0.25 | 0.798 / 0.457 | 0.778 | 0.824 | 0.450 / 0.904 | 0.300 | 60.5 / 63.2 |
| yolov10s | 0.20 | 0.773 / 0.439 | 0.681 | 0.831 | 0.350 / 0.808 | 0.250 | 52.6 / 54.6 |
| detr-r50 | 0.95 | 0.749 / 0.428 | 0.764 | 0.873 | 0.300 / 0.942 | 0.200 | 286.2 / 299.3 |
| yolo12n | 0.15 | 0.793 / 0.461 | 0.806 | 0.773 | 0.550 / 0.904 | 0.425 | 31.4 / 35.3 |
| yolov9t | 0.15 | 0.731 / 0.410 | 0.736 | 0.671 | 0.450 / 0.846 | 0.650 | 38.0 / 38.5 |
| yolov10n | 0.30 | 0.730 / 0.439 | 0.472 | 0.850 | 0.000 / 0.654 | 0.150 | 25.2 / 25.8 |
| florence2-base-ft | 0.05 | 0.805 / 0.417 | 0.861 | 0.899 | 0.600 / 0.962 | 0.175 | 1579.6 / 1642.8 |

### SeaShips: dev-selected RF-DETR-Small only

| model | images | GT | frozen τ | AP50 / AP50–95 | R tiny / small / med / large | CPU p50 / p95 ms |
|---|---|---|---|---|---|---|
| rfdetr-small | 3500 | 4683 | 0.60 | 0.735 / 0.453 | N/A / 0.889 / 0.798 / 0.621 | 194.0 / 199.7 |

The other 16 new detectors did **not** run the full SeaShips test: the slow CPU models would dominate the overnight budget. This selected-model check used the existing SeaShips dev threshold unchanged; no SMD or SeaShips test metric chose the model.

## 5. Existing B coverage — inherited, NOT rerun

These values are read from B’s archived `20261104-b-dev640` / `20261104-b-test640` metrics. Latency below is **NVIDIA L4 GPU p50**, not the current CPU. B used mixed fp16 Ultralytics / fp32 HF precision and its own package versions. Do not compare the numeric milliseconds across the GPU and CPU tables. AP protocol/sample counts agree, but this is not a newly controlled same-environment rerun. Argus maritime-trained variants are separated conceptually from generic zero-shot models; SeaShips is excluded for them.

| inherited model | params M | Sea dev AP50 / AP | SMD dev AP50 / AP | SMD test AP50 / AP | L4 GPU ms (inherited) |
|---|---|---|---|---|---|
| yolov8n | 3.2 | 0.377 / 0.210 | 0.592 / 0.332 | N/A | 10.8 |
| yolov8s | 11.2 | 0.564 / 0.316 | 0.571 / 0.393 | 0.842 / 0.477 | 10.7 |
| yolo11n | 2.6 | 0.451 / 0.251 | 0.639 / 0.384 | 0.808 / 0.456 | 12.9 |
| yolo11s | 9.5 | 0.506 / 0.276 | 0.699 / 0.393 | 0.819 / 0.479 | 13.1 |
| yolo26n | 2.6 | 0.506 / 0.271 | 0.641 / 0.359 | 0.814 / 0.451 | 14.9 |
| yolo26s | 10.0 | 0.569 / 0.331 | 0.644 / 0.370 | 0.866 / 0.485 | 14.5 |
| rtdetrv2-r18 | 20.2 | 0.654 / 0.367 | 0.794 / 0.412 | 0.896 / 0.490 | 55.4 |
| dfine-n | 3.8 | 0.613 / 0.334 | 0.723 / 0.415 | 0.877 / 0.499 | 61.9 |
| dfine-s | 10.3 | 0.656 / 0.373 | 0.691 / 0.424 | 0.885 / 0.498 | 65.2 |
| yolox-tiny | 5.1 | 0.400 / 0.198 | 0.621 / 0.306 | 0.745 / 0.389 | 5.5 |
| nanodet-plus | 2.4 | 0.291 / 0.114 | 0.322 / 0.169 | 0.650 / 0.300 | 9.8 |
| yolov8s-world | 12.8 | 0.478 / 0.287 | 0.609 / 0.345 | 0.735 / 0.431 | 14.5 |
| owlv2-b16 | 155.0 | 0.660 / 0.375 | 0.713 / 0.320 | 0.843 / 0.476 | 387.9 |
| gdino-t | 172.3 | 0.442 / 0.256 | 0.433 / 0.267 | N/A | 210.2 |
| argus-yolov8s | 11.1 | excluded (trained on set) | 0.825 / 0.358 | 0.796 / 0.436 | 10.9 |
| argus-yolo11l | 25.3 | excluded (trained on set) | 0.692 / 0.201 | N/A | 22.6 |
| rtdetr-l | 33.0 | 0.697 / 0.414 | 0.733 / 0.412 | 0.878 / 0.507 | 41.9 |

D also already measured the optimized edge path: YOLO11n-416 INT8 detector sustained **8.1 / 9.2 ms p50/p95**, and its full YOLO11n/MobileCLIP-S1 pipeline measured **15.8 / 25.5 ms**, **59 processed FPS**, **613 MB peak RSS** on D’s separate four-thread Xeon Platinum 8481C proxy. Those are **inherited D measurements**, not this VM, not the actual Dell/MacBook, and not new bake-off timings. We do not repeat D’s inferred laptop scaling as measured performance.

## 6. Hosted services — same 40-frame SMD dev/test protocol

All parameters are **N/A / undisclosed**. All latency below is **remote end-to-end ms**, not local CPU/GPU inference. Gemini receives a fixed boat/buoy/person localization prompt, normalized boxes, self-reported score, and a constrained JSON response schema. Scores are not calibrated detector probabilities. Cloud Vision uses Object Localization. Raw responses are archived without authorization headers.

| service | frozen τ | dev AP50 / AP | test AP50 / AP | dev FP/sea (17) | test R med / large | dev E2E p50 / p95 | test E2E p50 / p95 |
|---|---|---|---|---|---|---|---|
| gemini-2.5-pro | 0.90 | 0.567 / 0.250 | 0.674 / 0.388 | 0.294 | 0.100 / 0.865 | 9928.1 / 12639.9 | 9785.9 / 11424.0 |
| cloud-vision | 0.60 | 0.436 / 0.195 | 0.423 / 0.297 | 0.000 | 0.000 / 0.558 | 231.4 / 310.0 | 259.7 / 391.3 |
| gemini-2.5-flash | 0.95 | 0.105 / 0.039 | 0.069 / 0.026 | 0.000 | 0.000 / 0.231 | 4535.9 / 7855.6 | 4504.3 / 6309.9 |

Test FP/sea is N/A for all three because the test has no sea-only frames. Gemini Flash’s first dev attempt returned a different box schema and was rejected, not silently scored as empty. The schema-constrained Pro attempt hit HTTP429 quota and was partial; a separate completed retry-enabled dev run supplied the frozen threshold. No partial/error entry contributes to the table. API retries only handle transient statuses; they do not change the request. Only one corrected frozen test was run per service. Training overlap is unknown, service revisions can drift, quota/availability/cost are not benchmarked, and these localization results do not evaluate language reasoning or general maritime image understanding.

## 7. Ship-type classification / embedding

SeaShips native bulk/general/ore cargo map to cargo ship; container/fishing/passenger retain their coarse subtype. **13-way** means top-1 among 13 candidate prompts; the GT actually supports only **four coarse types**, not 13 independently validated ship classes. The restricted-four result masks the other prompts. Context is 1.5× GT box; seed0, balanced dev cap120 per each of six native labels (720 crops). Locked test uses all 4,683 labelled crops and is not class-balanced. All seven use exactly the same new crop sample and D’s unchanged prompts. Image/text parameters are measured separately; classification cost runs the image tower with cached text. Timing is torch batch16 mean per crop, not p50.

| model | total / visual params M | dev 13 / 4 accuracy | test 13 / 4 accuracy | CPU mean ms/crop dev / test |
|---|---|---|---|---|
| mobileclip-s2 | 99.2 / 35.8 | 0.604 / 0.697 | 0.698 / 0.780 | 81.2 / 82.6 |
| mobileclip-s1 (matched baseline) | 85.0 / 21.5 | 0.596 / 0.726 | 0.678 / 0.783 | 60.2 / 58.9 |
| siglip2-b16 | 375.2 / 92.9 | 0.596 / 0.717 | 0.683 / 0.769 | 84.3 / 168.1 |
| clip-b16-openai | 149.6 / 86.2 | 0.567 / 0.706 | 0.646 / 0.780 | 92.7 / 95.6 |
| openclip-b32-datacomp | 151.3 / 87.8 | 0.549 / 0.689 | 0.597 / 0.756 | 23.3 / 23.3 |
| openclip-b16-laion | 149.6 / 86.2 | 0.469 / 0.662 | 0.498 / 0.666 | 91.0 / 86.6 |
| clip-b32-openai | 151.3 / 87.8 | 0.401 / 0.671 | 0.417 / 0.683 | 23.6 / 23.7 |

The original D archived sample had **604** crops, despite the same cap120 command. Its MobileCLIP-S0/S1, OpenCLIP-B32 and SigLIP-B16 results are preserved in D, not mixed into this ranking. S1 was the sole repeated baseline specifically to obtain a consistent 720-crop comparison, as permitted by the task. The early freeze note guessed a different cap; `bakeoff-manifest.json` explicitly corrects that reason to a different actual sample/count. The matched S1 is not counted as a new checkpoint. S2’s dev 13-way advantage is six crops out of720 and does not establish a statistically robust replacement. SMD adds 30 dev and72 test diagnostic crops, but generic Boat/Vessel labels cannot establish fine ship-type truth; their per-label predictions are archived rather than advertised as subtype accuracy. Warship/submarine/coast-guard/etc. prompts lack corresponding SeaShips GT. No claim is made for those classes.

New weight mappings: OpenAI CLIP B/32 and B/16 use their **QuickGELU** architectures; OpenCLIP B/16 uses `laion2b_s34b_b88k`, B/32 uses `datacomp_xl_s13b_b90k`; SigLIP2-B/16 uses `webli`; MobileCLIP-S2 uses `datacompdr`. Exact cached HF revisions and local checkpoint SHA256 values are in `bakeoff-weights.json`.

### DINOv2 is image-only: feature smoke, not zero-shot subtype accuracy

| model | params M | dimension | dev crops | CPU ms p50 / p95 | subtype accuracy |
|---|---|---|---|---|---|
| facebook/dinov2-small | 22.06 | 384 | 60 | 58.5 / 73.2 | N/A — no text/classification head |
| facebook/dinov2-base | 86.58 | 768 | 60 | 139.9 / 155.3 | N/A — no text/classification head |

These two encoders ran only on dev crops. No fitted probe, nearest-neighbor classifier, or text tower was constructed, so reporting a zero-shot class accuracy would be invalid. DINOv2-with-a-probe and the separate text-aligned dino.txt variant are future candidates, not measured classifier results.

## 8. Current Go prioritization baseline — SYNTHETIC ORACLE ONLY

Actually run on this branch’s base application code (`3699324`), using its written mission-spec oracle, not human field truth:

```sh
/home/ubuntu/go/bin/go run ./cmd/bench -n 6000 -seeds 101,102,103
```

Seeds 101,102,103 · 6000 frames each

| Model | Edge drop | Edge miss P1 | Edge miss P1–2 | Tier exact | ±1 tier | P1 recall | P1 precision | Critical miss | False flash | Flash load | NDCG@20 | Review burden |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| maritime | 6.5% | 0.1% | 0.0% | 77.8% | 98.2% | 89.7% | 75.4% | 0.0% | 1.1% | 26.1% | 0.979 | 52.2% |
| air | 6.5% | 0.1% | 0.1% | 91.2% | 98.8% | 91.5% | 92.6% | 0.0% | 0.5% | 14.4% | 0.979 | 21.4% |
| sar | 6.5% | 0.0% | 0.0% | 79.8% | 96.2% | 99.3% | 73.3% | 0.0% | 0.2% | 6.2% | 0.977 | 5.7% |

These ranking/tier metrics are independent of detector AP and classifier accuracy. They do not validate a real sensor, deployment, military identification, or mission outcome. Main was moving in parallel; this is a pinned-code baseline, not an assertion about later integration changes.

## 9. Availability, provenance, and what was not tested

| candidate / family | coverage / reason |
|---|---|
| YOLOv9 t/s; YOLOv10 n/s; YOLO12 n/s | 6 new checkpoints: dev + frozen SMD test. Ultralytics public COCO weights; no maritime tuning. Larger variants (including registered-only v9c) not run after representative sizes. |
| DETR R50, Conditional DETR R50, Deformable DETR, RT-DETRv2-R34 | 4 new COCO detection checkpoints: dev + frozen SMD test. HF IDs in tools/bakeoff.py. Frozen-BatchNorm buffer warnings occurred; no failed model entry. |
| RF-DETR Nano / Small / Base | 3 public COCO checkpoints with DINOv2-derived backbone: dev + SMD test. Small additionally full SeaShips test. Native384/512/560; eager unoptimized CPU path, not compiled/export-optimized. |
| OWL-ViT v1 B/32 and B/16 | 2 new checkpoints: dev + SMD test. CLIP-style web image/text plus public detection fine-tuning; consult official model cards, not an independently audited training mixture. |
| Grounding DINO Base | 1 new public open-vocabulary checkpoint: dev + SMD test, same fixed maritime label prompts as existing HF adapter. |
| Florence-2 Base FT | 1 new public community-converted checkpoint: dev + SMD test. FLD-5B/downstream-task provenance per card, no independent overlap audit. OD output has no calibrated score: adapter assigns1.0 to every generated box. AP ties/τ sweep are therefore not directly comparable to calibrated detectors. |
| PaliGemma2 | Official Hugging Face model metadata reports manual-gated weights; no approved weights/access obtained. No valid comparable detection adapter or measurements. Not scored0. |
| Grounding DINO1.5 | Official repository requires a separate DeepDataSpace API token; no public-weight/local adapter or service credential obtained. GCP auth does not grant it. |
| SAM / SAM2 / grounded-SAM cascade | Mask/proposal models alone do not provide this vessel class/score protocol. No prompt/cascade was calibrated on dev, so not presented as a comparable standalone detector. |
| CLIP / OpenCLIP / SigLIP2 / MobileCLIP | 6 new text-aligned checkpoints + one matched existing S1 baseline, dev and locked test. Older D results inherited, not unnecessarily repeated. |
| DINOv2 small/base | 2 dev feature-only smoke measurements; zero-shot ship-type accuracy undefined without an added head. Probe / dino.txt not integrated. |
| Gemini2.5 Flash / Pro; Cloud Vision | 3 services, corrected dev + one frozen SMD test each. Prompt-specific localization only; quota and service revisions limit reproducibility. |
| New GPU runs / full equal-resolution sweeps | Not done: VM has no GPU; checked-in benchmark tools do not provision GPUs. CPU reduced dev samples used. No new GCP GPU VM was provisioned. |
| New INT8 / ONNX / OpenVINO / CoreML / sustained RSS tests | Not done; existing D results retained. New eager-PyTorch timing does not satisfy the complete edge acceptance budget. |
| Full SeaShips detector test for all17 / Singapore Maritime | Only dev-selected RF-Small full SeaShips test; other16 deferred for CPU budget. Singapore annotation joins invalid and excluded. |

Public checkpoints are obtainable, not necessarily training-disjoint from public maritime images. License and pretraining-overlap audits remain before deployment/distribution. “Zero-shot” here means no maritime training/fine-tuning in this study, not proof that upstream pretraining never saw similar imagery.

Official sources inspected: [OWL-ViT](https://huggingface.co/google/owlvit-base-patch32), [RF-DETR](https://github.com/roboflow/rf-detr), [Florence conversion](https://huggingface.co/florence-community/Florence-2-base-ft), [PaliGemma2](https://huggingface.co/google/paligemma2-3b-pt-224), [Grounding DINO1.5](https://github.com/IDEA-Research/Grounding-DINO-1.5-API), [DINOv2 model card](https://huggingface.co/facebook/dinov2-small).

## 10. Reproduction, artifacts, and checks

Small committed configs: `configs/bakeoff-requirements.txt`, `bakeoff-data.json` (input SHA256/bytes), `bakeoff-taus.json`, `bakeoff-weights.json`, `bakeoff-manifest.json` (sources, frozen choices, run URIs, validation commands), and `bakeoff-results.json` (finite JSON; unsupported metrics are null). Raw runs retain their original Python NaN convention; do not read NaN as zero.

Install the CPU wheels first, then the pinned extras:

```sh
python -m pip install torch==2.6.0 torchvision==0.21.0 --index-url https://download.pytorch.org/whl/cpu
python -m pip install -e 'edgeml[detect,test]' -r edgeml/configs/bakeoff-requirements.txt
```

Actual detector commands (working directory `/home/ubuntu/bakeoff`; data root `/home/ubuntu/data`; weight/data hashes in configs):

```sh
PY=/home/ubuntu/venv-bakeoff/bin/python
TOOL=/home/ubuntu/repos/ghost-seeker/edgeml/tools/bakeoff.py
export OMP_NUM_THREADS=4 MKL_NUM_THREADS=4 OPENBLAS_NUM_THREADS=4
MODELS=yolov9t,yolov9s,yolov10n,yolov10s,yolo12n,yolo12s,detr-r50,conditional-detr-r50,deformable-detr,rtdetrv2-r34,owlvit-b32,owlvit-b16,gdino-base,rfdetr-nano,rfdetr-small,rfdetr-base,florence2-base-ft
$PY $TOOL detectors --run-id 20261105-bakeoff-cpu-dev --split dev \
  --sets seaships7000:5,smd_vis_onboard_sample:1 --models "$MODELS" --device cpu --imgsz 640 --out /home/ubuntu/runs
$PY $TOOL detectors --run-id 20261105-bakeoff-cpu-test --split test \
  --sets smd_vis_onboard_sample:1 --models "$MODELS" --taus /home/ubuntu/bakeoff/taus-cpu-dev.json --device cpu --out /home/ubuntu/runs
$PY $TOOL detectors --run-id 20261105-bakeoff-selected-seaships-test --split test \
  --sets seaships7000:1 --models rfdetr-small --taus /home/ubuntu/bakeoff/taus-cpu-dev.json --device cpu --out /home/ubuntu/runs
```

Exact per-classifier invocations (annotation/image roots, split IDs, locked SMD videos, crop caps) are in `remaining-cpu-queue.log` and `baseline-s1-queue.log` in the support bundle. They invoke `bakeoff.py classifiers` → `d_eval_zeroshot.py`, with six new names plus matched S1, context1.5, dev cap120 / test cap10000. API invocations/configs and the exact frozen adapter are archived; only Cloud dev from `api-dev`, Flash dev from `api-dev-schema`, and Pro dev from `api-pro-dev` are authoritative. DINO commands are in `dino.log`. Re-analyze archived predictions rather than repeatedly invoking locked tests during development.

GCS runs (original config, metrics, detection JSONL; hosted runs also raw responses):

- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-cpu-dev/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-cpu-test/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-selected-seaships-test/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-classifiers-dev4/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-classifiers-test4/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-api-dev/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-api-dev-schema/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-api-pro-dev/`
- `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-bakeoff-api-test/`

Supplementary provenance/logs/source snapshots, ID lists, full pip freeze, weight hashes, DINO results, and Go output: **`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/20261105-0319-b-bakeoff-summary/`**. Run metrics and classifier objects were verified against local byte sizes after upload. Superseded CPU/threading/Florence pilots are excluded from conclusions; Gemini malformed/partial entries are retained for audit only. The initial classifier pilot directory was empty and had no object to upload.

The run configs’ git SHA alone does not identify the new adapters, which were still uncommitted during execution. The manifest pins base3699324 and actual source hashes; the PR supplies those source edits. Two later changes affect metadata only: raw API attempt-count logging and an OWL-ViT training-description correction. Exact frozen runner/adapter snapshots are in the support bundle; no prediction logic or test threshold changed.

**Storage caveat:** `ghost_fleet.edgeml_results` lookup returned `Not found`; no BigQuery row was inserted and no shared table/schema was created. GCS/configs are the delivered authoritative artifacts.

**Verification actually run and passed:** `go build ./...`, `go vet ./...`, `go test ./...`; Node24.22.2 `node --test web/*.test.cjs` (**20 passed**); installed edgeml extras + `python -m pytest edgeml/tests -q` (**32 passed**, including7 new adapter tests); focused Ruff E9/F63/F7/F82, Python compileall, and `git diff --check`. No configured static type-checker was present; compileall is a syntax check, not a claim of static typing. No UI changes or UI-driven test were needed.

## 11. Remaining risks / next evidence

- Obtain a genuinely tiny/far-contact labelled test and more independent cameras; current size buckets cannot validate far-contact safety.
- Measure the actual target laptop, per-model RSS and sustained throughput, then export/quantize any new edge candidate on dev only.
- Resolve gated/API access only if those candidates justify a separate dev-first study; SAM needs a specified grounding/classification cascade.
- Audit upstream training overlap, checkpoint/data licensing, class imbalance, and confidence calibration. Human review remains required for proposed labels.
- Publish the optional/shared BigQuery summary once the integration owner establishes its schema. No application weights, production defaults, or scoring behavior were changed.

All new table values above come from the named executed runs or direct arithmetic over their measured JSON; inherited B/D numbers are explicitly labelled. No new GPU or real-world operational performance is inferred.
