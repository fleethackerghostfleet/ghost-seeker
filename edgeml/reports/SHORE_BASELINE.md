# Shore zero-shot DEV baseline — 2026-11-05

**No training and no locked-test inference.** Both modes scored all 2,423 frozen
valid frames (five Singapore dev videos plus MVI_0790/MVI_0799), containing
17,633 vessel observations. Every frame is 1920x1080. Native is the stronger
zero-shot baseline; this simple tiling configuration underperformed substantially.
Neither mode approaches the draft day-stratum recall gate.

## Operating points and latency

Each mode independently maximizes valid recall subject to pooled precision >=.90.
This does **not** enforce .90 precision in every stratum; failures remain visible
below. Thresholds and detector configuration are frozen in
`configs/shore-baseline-v1/{native,tiled}/threshold.json`.

| Mode | Threshold | Recall | CP95 lower | Precision | AP50 | p50 / p95 ms |
|---|---:|---:|---:|---:|---:|---:|
| Native 512 | .431105614 | 65.89% | 65.30% | 90.01% | .7668 | 72.45 / 73.60 |
| 640px tiles, 20% overlap + full frame | .861855209 | 15.75% | 15.30% | 90.02% | .6089 | 367.02 / 377.28 |

Hardware: `shore-det-a100`, `us-central1-c`, NVIDIA A100-SXM4-40GB. Torch
2.9.1+cu129, RF-DETR 1.3.0 Small COCO checkpoint, transformers 4.57.1,
peft 0.17.1, four Torch CPU threads. No inference optimization/compilation was
enabled. Tiling uses class-agnostic NMS .5, max 300 merged detections.

Latency includes decoded-frame preprocessing, synchronized GPU execution and
merge, excludes disk IO/hash verification, and excludes five warmup passes.
All 2,423 frames contribute to each latency distribution. These A100 timings
cannot certify the requirement's L4 latency target.

## Per-stratum results

Recall and precision percentages; CP is the one-sided 95% exact lower bound.
AP50 always uses IoU .5, whereas requirement recall allows .3 for boxes <32².
Size-only rows include both lighting groups; day gate rows explicitly exclude
the visually reviewed MVI_1578_VIS twilight source.

| Stratum | Boxes | Native recall / CP | Native precision | Native AP50 | Tiled recall / CP | Tiled precision | Tiled AP50 |
|---|---:|---:|---:|---:|---:|---:|---:|
| Tiny | 17 | 0 / 0% | 0% | .00004 | 0 / 0% | N/A | .00037 |
| Small | 1,989 | 50.63 / 48.76% | 64.59% | .3793 | 0 / 0% | N/A | .3425 |
| Medium | 7,943 | 47.60 / 46.67% | 88.69% | .6539 | 8.55 / 8.04% | 84.98% | .4877 |
| Large | 7,684 | 88.89 / 88.28% | 98.01% | .9733 | 27.30 / 26.47% | 91.78% | .8232 |
| **Day small** | **1,626** | **61.93 / 59.90%** | **64.93%** | .4505 | **0 / 0%** | **N/A** | .3999 |
| **Day medium+large** | **12,455** | **64.63 / 63.92%** | **93.90%** | .7976 | **22.30 / 21.68%** | **90.25%** | .6524 |
| Low-light | 3,535 | 72.45 / 71.18% | 96.03% | .8385 | 0 / 0% | 0% | .6359 |

Both modes have **0 FP per vessel-empty frame on 17 empty frames**. This is a
small negative sample, not evidence of a robust false-alarm guarantee. There are
no low-light empty frames, so that rate is unavailable for low-light. Size-bin
empty-frame FP rates are also zero. Full day/size metrics and FP counts are in
the committed metric JSON files.

R4 is **N/A** for this valid set: the catalog images have no GT track IDs.
The harness's MOT two-second-window math is unit-tested; no locked MOT set was
run merely to obtain R4.

## Provenance and checks

Frozen annotations SHA-256:
`695c77a453d9ced442dab8401d42b958f2a87cff2f9e5ccb38b6a160f7c51b2f`.
The baseline checked annotation/shard/image hashes before scoring. Source script
and checkpoint hashes are recorded in `configs/shore-baseline-v1/summary.json`.

Predictions, thresholds, metrics, latency, environment snapshot and logs:

`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/shore/runs/zeroshot-20261105-v1/`

Metric/routing tests after the final exporter edit:

```
...........                                                              [100%]
11 passed in 0.38s
```

The existing zero-shot tests also passed in the earlier combined run:
`14 passed in 0.46s` (11 shore + 3 existing tests). The tests cover the CP
300-observation examples (eight misses passes .95 lower bound; nine fails),
relaxed-vs-strict IoU, AP50, ignore/crowd handling, empty/error cases, tied-score
threshold selection, valid-only fitting, track-window gaps/source scoping,
source routing, bucket boundaries and source frame indices.

Freeze verification checked all seven annotation hashes, image-shard hashes and
counts, source separation and zero exact-image overlap across partitions.
Human annotation completeness, independent-camera provenance and licenses remain
unresolved as documented in `SHORE_DATASET.md`. This is a baseline, not an
accuracy certification. No root cause for tiling's quality loss was established
in this step; do not assume tiling improves small-object recall.
