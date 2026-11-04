# Workstream B — zero-shot detector benchmark (RGB, GPU accuracy + GPU latency)

**Question.** Which existing pretrained detectors are usable on maritime imagery *without fine-tuning*, and what do
input size and horizon-band tiling buy for small/far contacts? Everything is behind the `Detector` adapter
(CONTRACTS §2) in `edgeml/src/edgeml/detectors/`; native labels are kept and mapped to a class-agnostic
`vessel` / `object` label (`detectors/labels.py`).

Phase-1 scope: RGB only (IR skipped), no fine-tuning, no tracking/sightings (Workstream C) — but the metrics below are
chosen for the sightings problem: recall by box area (far contacts) and **false positives per sea-only frame**
(every FP on an empty-sea frame becomes a false sighting downstream).

All numbers in this report come from `metrics.json` files under
`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/runs/<run-id>/` (run ids listed in §8); tables were rendered
with `edgeml/tools/report_tables.py`. "Verified" = measured in those runs; "inferred" is marked as such.

## 1. TL;DR / shortlist

__TLDR__

## 2. Setup

| item | value |
|---|---|
| GPU VM | `edgeml-b-l4`, g2-standard-4 (1× NVIDIA L4, 4 vCPU), us-central1-a, Deep Learning VM image (`g2-standard-8` was stocked out) |
| software | torch 2.x CUDA, ultralytics 8.3.x, transformers 4.5x, onnxruntime 1.2x, faster-coco-eval (COCO AP), see `edgeml/pyproject.toml` |
| precision | fp16 for Ultralytics models on GPU, fp32 for HF (RT-DETRv2, D-FINE, OWLv2, Grounding DINO) and ONNX-Runtime models (YOLOX-tiny, NanoDet-Plus, CUDA EP) |
| latency | wall-clock of `Detector.detect()` per frame incl. preprocessing/postprocessing, batch 1, median over the set (`ms_p50`); **GPU numbers, not the laptop** — Workstream D measures CPU ms from the ONNX exports |
| catalog | `~/data/catalog/*.jsonl` mirror of the BigQuery `ghost_fleet` tables (pulled 2026-11-04), images mirrored to `~/data/datasets/...` |

### Candidates (all zero-shot)

| adapter name | family | weights | train data | params (M) | tier | notes |
|---|---|---|---|---|---|---|
| yolov8n / yolov8s | Ultralytics YOLOv8 | `yolov8{n,s}.pt` | COCO | 3.2 / 11.2 | nano / small | COCO `boat` |
| yolo11n / yolo11s / yolo11m | Ultralytics YOLO11 | `yolo11{n,s,m}.pt` | COCO | 2.6 / 9.4 / 20.1 | nano / small / med | literature pick |
| yolo26n / yolo26s | Ultralytics YOLO26 | `yolo26{n,s}.pt` | COCO | 2.4 / 9.5 | nano / small | NMS-free head; literature pick |
| yolox-tiny | YOLOX (Megvii ONNX) | `yolox_tiny.onnx` 416² | COCO | 5.1 | tiny | raw head output, decode in adapter |
| nanodet-plus | NanoDet-Plus-m 416 (ONNX) | `nanodet-plus-m_416.onnx` | COCO | 1.2 | tiny | |
| dfine-n / dfine-s | D-FINE (HF `ustc-community/dfine-{nano,small}-coco`) | HF | COCO | 3.8 / 10.2 | nano / small | DETR-style, no NMS; literature pick |
| rtdetrv2-r18 | RT-DETRv2 (HF `PekingU/rtdetr_v2_r18vd`) | HF | COCO | 20 | medium | |
| rtdetr-l | RT-DETR-L (Ultralytics) | `rtdetr-l.pt` | COCO | 32 | large | |
| yolov8s-world | YOLO-World-S (Ultralytics) | `yolov8s-worldv2.pt` | O365+GoldG+CC3M | 12.7 | small | prompts: ship, boat, sailboat, ferry, cargo ship, fishing boat, speedboat, kayak, warship, submarine, vessel, buoy, person |
| owlv2-b16 | OWLv2 B/16 (HF `google/owlv2-base-patch16-ensemble`) | HF | WebLI + O365/VG | 154 | **heavy, teacher/upper-bound only** | same prompts; ~380 ms/frame on the L4 → will not run on the laptop |
| gdino-t | Grounding DINO-T (HF `IDEA-Research/grounding-dino-tiny`) | HF | O365+GoldG+Cap4M | 172 | **heavy, teacher only** | same prompts |
| argus-yolov8s / argus-yolo11l | maritime-pretrained YOLO (public "Argus" checkpoints) | HF | Roboflow marine surveillance **+ SeaShips** | 11.1 / 25.3 | small / large | **trained on SeaShips → excluded from SeaShips tables**; shown on SMD only |

Params are Ultralytics/HF reported values (verified for the Ultralytics models via `model.info()`, inferred from model
cards for the others); GFLOPs are not reported because the adapters' `info()` only has them for Ultralytics models.

### Eval sets (catalog state 2026-11-04, see `edgeml/configs/splits.yaml`)

| set | split used | images (stride) | GT boxes | notes |
|---|---|---|---|---|
| SeaShips 7000 | dev = upstream train+valid (3500); **test = upstream test (3500)** | dev stride 5 → 700; test stride 2 → 1750 | ~1.3 boxes/img, 6 ship classes | fixed shore CCTV, 1920×1080, mostly medium/large ships |
| SMD VIS onboard sample | dev = MVI_0790, MVI_0799; **test = MVI_0797, MVI_0801** | 40 / 40 frames | 27 dev GT (6 medium, 21 large, 0 < 32² px), 75 test GT | the only on-board labelled set; horizon GT available |
| Singapore Maritime (HF COCO) | — | 1981 imgs / 112k boxes in catalog | **GT unusable, see §7** | excluded from selection; dev numbers shown for completeness |
| unlabeled 1-fps clips | qualitative | 7 clips, every 15th frame, ≤60 frames | none | glare, whitecaps, own-ship structure, tiny horizon targets |

Recall buckets: the code keeps COCO-style tiny (<16²) / small (16²–32²) / medium (32²–96²) / large (>96²) buckets; the
tables merge tiny+small into **"<32²"** as asked in `literature.md` and print the GT count per bucket because the
labelled sets have very few sub-32² boxes (SMD dev: 0, SeaShips dev: 1).

Threshold protocol: every model is run at score ≥ 0.05; AP is threshold-free; the operating threshold τ is chosen per
model and per dataset on **dev** (max F1 of class-agnostic vessel matching at IoU 0.5) and then frozen
(`~/taus640.json`, `~/taus960.json`, copied to the run dirs) for the single test pass.

## 3. Dev leaderboard (640 px, full frame)

__DEV640__

## 4. Input-size sweep (dev)

__SWEEP__

## 5. Horizon-band tiling (dev)

__TILING__

## 6. Locked test sets (one pass, τ frozen from dev)

__TEST__

## 7. Singapore Maritime catalog: image↔annotation mis-join (for the catalog owner)

__SG__

## 8. Failure modes (contact sheets)

__SHEETS__

## 9. ONNX exports for Workstream D (CPU)

__EXPORTS__

## 10. Reproduce

__REPRO__
