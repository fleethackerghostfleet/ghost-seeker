# Thermal hotspot fallback: development record

## Data boundary

No `ir_maritime_vessels` images, annotations, or splits were used for threshold
selection, synthetic generation, or tuning. Real IR was used **only** to evaluate
the frozen grayscale modality gate. Its unknown license still prohibits shipping
models trained on it. This proposer has no learned weights.

Development inputs:

* `tools/synth_thermal.py`, seed **1729**, 120 images at 480x320: 84 contacts,
  36 empty frames. Sky/sea gradients, random horizon height and slight tilt,
  Gaussian clutter, sinusoidal wave texture, horizontal glint streaks; alternating
  white/black-hot ellipses and ship silhouettes with 3–120 px nominal width.
* `clean/v3/coverage-20261105/catalog` SMD onboard **dev only**: 40 images,
  30 object boxes, including all 14 labeled negative images. Images are converted
  to grayscale before proposing. Neither SMD test nor any IR split was tuned on.

Local generated data: `~/data/synth_thermal_shapes_dev`; original rectangle-only
prototype: `~/data/synth_thermal_dev`. Both use the same seed. The rectangle
prototype informed replacing a wide horizontal blur (opposite-polarity side
lobes) with robust row medians, rejecting <3 px component dimensions, and
narrowing aspect ratio to 0.2–8. The final sweep below uses the shipped generator.

Reproduce generation from the repository root:

```sh
.venv/bin/python edgeml/tools/synth_thermal.py --seed 1729 --count 120 \
  --output ~/data/synth_thermal_shapes_dev
```

Output directories must not already exist. No real images are read by this script.

## Threshold selection

Candidates: `k={4,6,8}`, `min_delta={24,32,40}`; fixed min area 12 work pixels,
max component area 4% of work frame, minimum dimension 3, aspect 0.2–8.
Match criterion: proposal/GT IoU >0.3. Unmatched proposals count as false
proposals; this is a proposal-level measure, not tracking precision.

| k | delta | Synthetic matched /84 | Synthetic false proposals | Empty synth frames with proposals /36 | SMD negative proposals /14 frames |
|---|---|---|---|---|---|
| 4 | 24 | 79 | 16 | 4 | 15 |
| 4 | 32 | 78 | 35 | 4 | 11 |
| 4 | 40 | 78 | 18 | 2 | 5 |
| 6 | 24 | 79 | 8 | 1 | 4 |
| 6 | 32 | 78 | 13 | 2 | 2 |
| 6 | 40 | 78 | 13 | 1 | 4 |
| 8 | 24 | 80 | 6 | 0 | 5 |
| 8 | 32 | 79 | 14 | 2 | 2 |
| **8** | **40** | **79** | **8** | **1** | **1** |

Chose 8/40 to prioritize real grayscale-negative FP control while retaining
79/84 synthetic matches. This is deliberately conservative: very small or
low-contrast contacts can be missed. On all 40 grayscale SMD dev frames:
14 proposals, 4/30 GT boxes matched, 10 unmatched proposals; one proposal on
one of the 14 empty frames. This is not a general-purpose RGB detector.

## Frozen defaults and behavior

* Work width 480 (never upsample); robust row background and row MAD sigma;
  3x3 residual smoothing, threshold `max(8 * 1.4826 * MAD, 40)` on 8-bit intensity.
* Gray-friendly horizon uses the strongest median vertical gradient across
  columns, requiring strength >=2 and >=6x median row strength. Unlike the
  unconditional band estimator in `tiled.py`, it can decline a horizon and
  fall back to the whole frame. Search starts 4% frame height above a reliable
  horizon to retain straddling hulls. No shared temporal horizon state.
* Both residual signs in auto mode; optional `white`/`black`. 2x2 opening,
  connected components and the above size/aspect filters; at most eight
  proposals. Contrast-based scores in [0.3,0.9], label `thermal contact`.
* Default fusion `when_empty`, cover threshold .25. `uncovered` uses anomaly
  fusion's padded center/IoU coverage suppression. Non-grayscale frames pay
  only the cheap gate in addition to the original detector.
* Engine wraps after optional anomaly fusion, enabled by default. Explicit
  `thermal_fallback=None` or `False` disables; dictionary config accepts
  proposer options, fusion options, and `unknownness_floor` (default .9).
* CLI: `--thermal-fallback '{"mode":"uncovered","unknownness_floor":0.95}'`;
  `--thermal-fallback false` or `null` disables; bare flag uses defaults.
* Detection label passes into track label, then aggregator label votes. The
  zero-shot adapter restores the thermal label when thermal votes dominate,
  and floors unknownness **after** classification (also without an embedder).
  Open/update/close and duplicate reports retain that label/floor. Frame
  unknownness includes thermal duplicate reports. Nonthermal duplicate wire
  fields remain unchanged. Subtype can still hold the zero-shot hypothesis;
  it cannot replace the thermal keyDetection label.

## Modality evaluation (not tuning)

Gate frozen before real IR inspection: downsample to 64 px width; sum of mean
absolute B-G and G-R differences <=6 and mean HSV saturation <=18. Single
channel inputs pass directly. This identifies grayscale appearance, not sensor
physics: desaturated RGB can pass; false-color thermal can fail.

| Evaluation input | Gate positives | Rate |
|---|---|---|
| All clean v3 IR images (all splits pooled) | 8,402 / 8,402 | 100% |
| All clean v3 SMD RGB sample images | 0 / 80 | 0% |
| Ghost Fleet 5fps RGB, sorted local paths stride 20 | 4 / 315 | 1.27% |
| FVessel RGB, sorted local paths stride 20 | 0 / 130 | 0% |

RGB MOT inputs are under `~/data/clean_v3/edgeml/eval/`; IR and SMD paths follow
catalog GCS URIs relative to `~/data/clean_v3`. RGB rates were reported as-is,
not used to adjust the gate after observing IR. No real IR proposer recall or
tracking benchmark is claimed here. Unit tests use model-free detector/tracker
fakes with the real sighting aggregator; real model/boxmot replay is not covered.

## Existing suite failures

Clean detached `origin/dev` worktree at `42834c5` reproduces:

* `test_rpc_http_override_and_unexpected_engine_exception`: expected
  `RuntimeError: backend offline`, actual `inference request failed`.
* `test_body_limits_reject_without_waiting_for_the_declared_body[-1]`:
  expected HTTP 413, actual 400.

Neither protocol behavior is changed in this work.

Final full-suite command: `.venv/bin/python -m pytest edgeml/tests -q`

```text
FAILED edgeml/tests/test_sidecar_protocol.py::test_rpc_http_override_and_unexpected_engine_exception
FAILED edgeml/tests/test_sidecar_protocol.py::test_body_limits_reject_without_waiting_for_the_declared_body[-1]
2 failed, 606 passed, 3 skipped in 7.14s
```

All new thermal tests pass within this run, including no-embedder, low zero-shot
unknownness and higher-than-floor zero-shot unknownness on duplicate reports.
