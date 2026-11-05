# Tiling sanity check — DEV only

The zero small recall in Step 3 is at the **frozen precision-constrained
threshold**, not the candidate detection recall. Re-scoring the saved valid
predictions establishes the exact causal chain:

| Mode / threshold | Small TP / 1,989 | Small recall | Pooled precision |
|---|---:|---:|---:|
| Native, .001 candidate floor | 1,917 | 96.38% | 4.07% |
| Tiled, .001 candidate floor | 1,889 | 94.97% | 3.36% |
| Tiled, .1 | 1,776 | 89.29% | 17.30% |
| Tiled, native tau .431105614 | 1,423 | 71.54% | 56.57% |
| Tiled, .6 | 1,041 | 52.34% | 72.15% |
| Tiled, .8 | 39 | 1.96% | 79.46% |
| Tiled, frozen tau .861855209 | 0 | 0% | 90.02% |

**The maximum score of any matched tiled small-vessel candidate is .809252977.**
All 1,889 matched small candidates therefore fall below the selected .861855209
threshold. At lower thresholds they match GT in original-image coordinates;
small AP50 is nonzero (.3425). This disproves the hypothesis that small boxes
were universally lost through coordinate offsets, scaling, or NMS.

The reachable code path is `RFDetector._infer` (RF-DETR predict returns boxes in
input-image pixels), `BaseDetector.detect` (clips to input dimensions), then
`TiledDetector._run_crops` / `_shift` (adds crop origin), then `_merge` / NMS.
New `test_shore_tiling.py` exercises this same offset/merge path with a 20x20
contact at (1500,700) in a 1920x1080 frame: all nine full/tile calls preserve one
correctly located small box after NMS. It also tests empty/smaller-than-tile input.

No offset/scale/NMS implementation bug was found or patched. Before/after the
sanity check the frozen operating-point recall remains **0%**; the diagnostic
candidate recall is **94.97%**, not zero. No new GPU inference pass is needed to
re-score the identical saved zero-shot predictions. This check used no locked
annotations or predictions and completed within the 30-minute allowance.

This establishes why the reported zero occurs; it does not establish why this
tiling configuration produces so many high-confidence false positives. Label
incompleteness and crop/merge artifacts remain possible contributors, not proven
causes. We retain the honest poor operating point rather than relaxing precision.
For the budgeted training comparison, prefer native 512 vs a higher whole-frame
resolution, rather than adopting tiled crops on the strength of raw recall alone.

```
..                                                                       [100%]
2 passed in 0.07s
```
