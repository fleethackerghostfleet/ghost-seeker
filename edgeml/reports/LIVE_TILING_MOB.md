# Budgeted live tiling and possible person-in-water detection

## Verdict

The production sidecar now runs a native pass first, then admits at most two
640px tiles within a **3,500ms soft per-frame detector budget**. On the frozen,
official-video-disjoint eval-only holdout below, COCO-small contact recall rises
from **1.05% to 5.23%**, at a cost of small precision falling from **100.00% to
52.08%**. This is a bounded recall improvement, not a high-recall solution.

Possible-MOB precision/recall is **21.74% / 0.58%** (5 TP, 18 FP, 855 swimmer GT).
The context heuristic is **not rescue-grade**: a horizontal edge is not water
segmentation, an aerial swimmer need not have a horizon, and swimming does not
prove distress. Do not treat this report as evidence of reliable drowning/MOB
recognition or as an operational alert acceptance test. No threshold, tile
policy, model, or heuristic was tuned on these results.

## Live behavior and configuration

`edgeml.detectors.live_tiled.LiveTiledDetector` wraps the existing runtime
detector in `EdgemlEngine`, before optional anomaly fusion and tracking. It is
not an offline-only path. There are no new dependencies, weights or API fields.

Defaults (`--live-tiling` accepts a JSON object overriding these fields):

```json
{"enabled": true, "min_pixels": 921600, "tile": 640, "overlap": 0.2,
 "max_tiles": 2, "budget_ms": 3500, "cost_margin": 1.2, "merge_iou": 0.5}
```

- The minimum-resolution check is strictly `width * height > min_pixels`.
- Always run native inference, even if it alone exceeds the budget.
- With a sufficiently contrasting horizontal edge, prioritize a band straddling
  that edge. Otherwise rotate through a whole-frame grid; uncertain context
  does **not** prevent small-target tiling.
- Rotate tile origins across frames rather than repeatedly observing only the
  left edge. At most two extra calls per frame; this does not cover a 4K frame
  exhaustively in one frame. Horizon-only coverage intentionally favors distant
  contacts and may miss nearer swimmers.
- Before each extra call, require elapsed time plus measured inference cost
  times 1.2 to fit the budget. Cost uses the native measurement and a decaying
  high-water estimate of prior calls. No warm-up calibration is required.
- ONNX calls cannot be preempted. A sudden cost increase can overrun the soft
  budget; then no more tiles start. It is not a hard real-time or CPU-utilization
  limit. The budget covers detection, not tracking, CLIP, encoding, or transport.
- Offset crop boxes without mutating base detections; class-aware NMS avoids
  suppressing a person overlapping a vessel. Keep low-confidence tracking
  evidence at the existing 0.05 floor; new reports still require 0.35.
- Refine a COCO `person` below the estimated edge to `person in water`, unless
  its foot point lies in/just above a confident vessel box. Without reliable
  edge context, retain `person`. Shore/pier people can be false positives.
- Do not let the vessel-oriented zero-shot classifier overwrite either person
  label. Advertise the new class in sidecar capabilities.
- Add MOB aliases, catalog weights, directive matching, and an additive critical
  rescue scorer to both edge prioritizers. A generic `person` directive also
  matches the refinement. Human correction and suppression retain authority.
- Preserve the pre-existing broad-boost guard's denominator; add no simulated
  MOB imagery or simulation-pool entries.

`--live-tiling '{"enabled":false}'` disables extra tile passes, **not** the MOB
context refinement. `max_tiles:0` or `budget_ms:0` also disables extra passes.
Config is validated at construction. Per-frame `last_timing_ms` includes native
and total detector wall time plus actual extra-call count.

## Provenance and verified split

**Data:** the catalog and boxes are downloaded exclusively from
`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/clean/v3/coverage-20261105/catalog/`.
The clean/v3 catalog intentionally references immutable source JPEG objects at
`datasets/seadronessee/images/{train,val}/`; those referenced bytes were fetched
and their SHA-256 verified against the clean/v3 row. No clean/v1 data, Singapore
Maritime data, or `ir_maritime_vessels` were used. No training occurred.

**License:** clean/v3 catalog and the
[SeaDronesSee HF source card](https://huggingface.co/datasets/dronefreak/SeaDronesSee/blob/main/README.md)
identify this image release as **CC0-1.0**. The mirror revision recorded by the
catalog is `deb1f872ffe7efa4b7ee3a672562d16b6dc0ca5c`. This is eval-only use;
no model-license decision or change is made (YOLO11n AGPL-3.0 / MobileCLIP terms
remain as-is).

The existing clean/v3 `source_video=sds_seq...` and `eval_split` values are
inferred. **They are not used to claim video disjointness here.** Instead, join
clean/v3 numeric image IDs to the official uncompressed per-image `source`
metadata from the
[SeaDronesSee release](https://cloud.cs.uni-tuebingen.de/index.php/s/ZZxX65FGnQ8zjBP).
The uncompressed JSONs retain `drone`, `folder_name`, `video`, and `frame_no`.
The official train/val files share 32 video filenames, so those upstream image
splits are not used as video holdouts either.

Verified recording key: `drone/folder_name/video`. Of 10,477 clean/v3 images,
8,664 have an official video key across 37 videos; exclude the other 1,813 still
images. Assign each entire official video to holdout iff
`int(sha256('live-tiles-v1:' + key)[:8],16) / 2**32 < .25`.
This gives **10 held-out videos**, disjoint from the **27 unused reference
videos**. Select at most 24 frames/video, evenly spaced by official frame
number, without inspecting boxes or predictions: **217 frames**. No fitted
training or validation stage uses either partition. This is a newly constructed
eval-only holdout, not a repair or republication of clean/v3's existing split,
and not an evaluation of a maritime-fine-tuned model trained on the reference
partition. The fixed COCO model is used unchanged.

Held-out recordings (one frame from `DJI_0003_d3`, 24 from each other recording):

```text
m210/DJI_0003_d3/DJI_0003_d3.mov
m210/DJI_0011_d3/DJI_0011_d3.mov
m210/NorthSea_Sat_Z30_DJI003/NorthSea_Sat_Z30_DJI003.MP4
mavic/DJI_0032/DJI_0032.MP4
mavic/DJI_0041/DJI_0041.MP4
mavic/DJI_0062/DJI_0062.MP4
mavic/NorthSea_Fri_DJI_0114/NorthSea_Fri_DJI_0114.MP4
mavic/NorthSea_Fri_DJI_0115/NorthSea_Fri_DJI_0115.MP4
mavic/NorthSea_Sat_DJI_0122/NorthSea_Sat_DJI_0122.MP4
mavic/NorthSea_Thu_DJI_0107/NorthSea_Thu_DJI_0107.MP4
```

Official annotations SHA-256:

- `instances_train.json`: `d7309081bdd84975f1783279b4f02bfff829d8effa6e2a4857630f82c982a75c`
- `instances_val.json`: `9d64e077cc8c5d8c53e8f106ec292123300a6d93fed7f1c5c1464b600f9ce6a9`
- Generated full manifest: `0f72f9a3fb8cb63cde00286f6ffff6979a71e8cb8f12466fa7c7061495927486`

## Frozen metric protocol and results

Use native-pixel GT boxes from clean/v3, score-greedy one-to-one matching at
**IoU >= 0.5**, no AP averaging. Native and tiled use identical YOLO11n-640
FP32 COCO ONNX weights, input/keep-label settings and 0.05 candidate floor.
The shipped sidecar report threshold is **0.35**, not the RF-DETR offline
precision-constrained threshold in `SHORE_TILING_SANITY.md`.

Map `boat` + `jetski` GT to COCO `boat`; map `swimmer` GT to person for contact
metrics. Generic person and MOB predictions both match swimmers for contact
recall, but **only `person in water` predictions count as MOB predictions**.
Buoys and life-saving appliances are not COCO person/boat classes and are
excluded from these scoped metrics. False detections of the scoped classes on
those objects still count as FP. No ignored/crowd GT exists in this catalog.

COCO-small means **original-image box area < 32²**, not size after resizing to
640. The stricter `<32px` slice requires **both width and height <32px**. Slice
precision ignores matches to out-of-slice GT and unmatched out-of-slice
predictions (COCO-style size ignoring). It is not pooled all-size precision.
`N/A` means no predictions, not perfect precision.

| Slice | GT | Native TP / FP | Native P / R | Tiled TP / FP | Tiled P / R |
|---|---:|---:|---:|---:|---:|
| All supported contacts | 1,279 | 173 / 95 | 64.55% / 13.53% | 222 / 146 | 60.33% / 17.36% |
| COCO-small contacts | 478 | 5 / 0 | 100.00% / 1.05% | 25 / 23 | 52.08% / 5.23% |
| Both dimensions <32px | 380 | 3 / 0 | 100.00% / 0.79% | 18 / 14 | 56.25% / 4.74% |
| All vessels | 424 | 142 / 40 | 78.02% / 33.49% | 162 / 70 | 69.83% / 38.21% |
| COCO-small vessels | 23 | 0 / 0 | N/A / 0.00% | 3 / 12 | 20.00% / 13.04% |
| Vessels, both dimensions <32px | 6 | 0 / 0 | N/A / 0.00% | 0 / 5 | 0.00% / 0.00% |
| Possible MOB vs swimmer labels | 855 | 0 / 0 | N/A / 0.00% | 5 / 18 | 21.74% / 0.58% |

MOB labels exist, but they are **swimmer boxes**, not confirmed overboard or
distress events. The native unwrapped detector cannot emit the new class, hence
its zero MOB recall. The combined contact metric above provides a fair generic
person/boat baseline; the MOB row evaluates the new emitted label itself.
There is no labeled operational man-overboard incident set here, no shore/pier
negative challenge set, and no claim about false alarms per hour. The small
vessel slice has only 23 boxes (strict slice only six); do not generalize those
percentages to the fleet. Views are aerial, not bridge-mounted, so operational
distribution shift remains unmeasured.

For completeness, applying the unrelated offline frozen threshold
**0.8618552088737488** to the same YOLO predictions gives **0% small and strict
<32px recall in both modes** (precision N/A); pooled P/R is native
100% / 0.31%, tiled 94.12% / 1.25%. MOB recall is zero in both. No threshold
was silently lowered to claim a result, and this diagnostic does not reproduce
the older RF-DETR experiment.

## CPU latency and budget

Measured detector wall-clock time, excluding image download/decode and model
load, including preprocessing, ONNX, postprocessing and (tiled) context/NMS.
Shared Linux x86-64 VM, 8 vCPUs, Intel Xeon, ONNX Runtime **1.23.2 CPU provider**,
FP32, 4 intra-op threads / 1 inter-op thread; OpenCV **5.0.0**, NumPy **2.2.6**,
OpenCV threads=1. Five synthetic warm-up calls excluded. Alternate native/tiled
execution order each frame; reset tiling history at video boundaries. Other
verification jobs shared the VM, so this is not an isolated hardware benchmark.
193 frames are 3840x2160; 24 are 1920x1080.

| Mode | p50 ms/frame | p95 ms/frame | Extra calls/frame |
|---|---:|---:|---:|
| Native | 81.19 | 166.04 | 0 |
| Budgeted tiled | 227.64 | 464.53 | 2 |

All 217 frames admitted two tiles; **0/217 exceeded 3,500ms**. This CPU is much
faster than the user's ~1.5s/frame deployment baseline. With a deterministic
1.5s/call fake clock, unit tests admit native + one tile (3.0s) and reject the
third call; with a 4s native call they admit no tile. Sudden overruns are tested
to prevent subsequent admission. Neither these measurements nor the tests
promise hard deadlines on a different CPU.

## Reproduce

Source catalog is the clean/v3 prefix above (the same publication backs
`ghost_fleet.images_clean_v3` / `annotations_clean_v3`); no SQL selection or
server-side dataset mutation was used for this experiment.

Download `catalog/images/seadronessee.jsonl`,
`catalog/annotations/seadronessee.jsonl`, and `catalog/datasets.jsonl`. Download
the official `Uncompressed Version/annotations/instances_{train,val}.json`
from the release above (read-only public WebDAV with the share token as user).

```sh
python edgeml/tools/eval_live_tiling.py prepare \
  --catalog "$EVAL/catalog" \
  --official "$EVAL/official-train.json" "$EVAL/official-val.json" \
  --out "$EVAL/manifest.json"
# Fetch the manifest's gcs_uri objects to $EVAL/images/<numeric-image-id>.jpg.
# The evaluator verifies each image hash and dimensions against the clean row.
python edgeml/tools/eval_live_tiling.py run \
  --manifest "$EVAL/manifest.json" --images "$EVAL/images" \
  --model "$EVAL/model/model.onnx" --threads 4 --out "$EVAL/results.json"
```

Model source:
`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/yolo11n-640/{model.onnx,meta.json}`.
Model SHA-256: `cf094973e1f946b2e86883120ddc455a0e33bb07f89096ac1d8bac14ac9833b6`.
The evaluator saves summary JSON and all per-frame predictions; selection,
one-to-one counting, size ignores, MOB label handling and deadline admission
have model-free tests. Source pixels, credentials and model binaries are not
committed.
