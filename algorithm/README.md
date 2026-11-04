# CPU prioritization baseline

The implemented pipeline is **analyze once → rank the full pool → select within an absolute bandwidth allowance**. It accepts independent RGB images, JSONL manifests and sampled video. Built-in providers run on CPU; custom providers control their own runtime and dependencies. Labels enter evaluation only.

- [Provider handoff](PROVIDERS.md): replace object, visual-interest and quality models independently.
- [Ranking and selection](RANKING_AND_SELECTION.md): scoring, duplicate suppression, stable ranks and byte accounting.
- [Data notes](DATA_NOTES.md): sample provenance, label caveats and historical measurements.

## Quick start

Use Python 3.11 or newer. From `algorithm/`:

```bash
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements.txt
.venv/bin/python -m unittest discover -s tests -v
.venv/bin/python -m ghost_priority analyze --images /path/to/images --out runs/evidence
.venv/bin/python -m ghost_priority rank --records runs/evidence/records.jsonl --ranking-only --out runs/ranking
.venv/bin/python -m ghost_priority select --ranked runs/ranking/ranked-priority.jsonl --bandwidth-bps 64000 --window-seconds 60 --out runs/64kbps
.venv/bin/python -m ghost_priority select --ranked runs/ranking/ranked-priority.jsonl --bandwidth-bps 256000 --window-seconds 60 --out runs/256kbps
```

Open each run's `report.html`. The ranking report shows the complete priority list. Both selection reports reuse that exact list; changing bandwidth does not change its order or scores. Output directories must be new. Saved rankings use absolute JPEG payload paths, so keep those files accessible when sharing or moving reports.

For a combined comparison, `rank` saves each available baseline's full ranking once and then selects:

```bash
.venv/bin/python -m ghost_priority rank --records runs/evidence/records.jsonl --bandwidth-preset ddil --window-seconds 60 --out runs/comparison
```

Omitted bandwidth options default to `normal`: 2 Mbps over 60 seconds. `--ranking-only` accepts neither bandwidth settings nor evaluation labels.

## Interchangeable providers

Defaults are `NoObjects`, `ClassicalVisualInterest` and `SharpnessExposureQuality`. The optional `--model` argument enables `NanoDetObjects`. Teammate implementations can replace any role:

```bash
.venv/bin/python -m ghost_priority analyze --images /path/to/images --object-provider team_models:make_objects --visual-provider team_models:make_visual --quality-provider team_models:make_quality --out runs/team-evidence
```

`team_models` must be an importable module supplied by the team. Each no-argument factory returns a named object with `analyze(original_bgr)`. `--model` and `--object-provider` are mutually exclusive. See [PROVIDERS.md](PROVIDERS.md) for return types, examples and validation.

An object provider may return a direct relevance score, detections to be weighted by policy, or both. A direct score takes precedence, including zero; accompanying detections remain display evidence. The analyzer caches all signals so policy changes do not repeat inference. Scores are heuristics, not calibrated probabilities, identities or threat assessments.

## Optional built-in detector

The adapter uses the OpenCV Zoo 2022nov NanoDet model at 416 × 416. Its wrapper and Apache license are in `ghost_priority/third_party/`. Model files are ignored by Git.

```bash
mkdir -p models
curl --fail --location https://media.githubusercontent.com/media/opencv/opencv_zoo/48b10fa505aeb51fbd127fc60a5c660040e823b7/models/object_detection_nanodet/object_detection_nanodet_2022nov.onnx -o models/nanodet.onnx
.venv/bin/python -m ghost_priority analyze --images /path/to/images --model models/nanodet.onnx --out runs/with-detector
```

Built-in visual/quality analysis caps the longest side at 320 pixels. NanoDet separately letterboxes original pixels to 416 × 416 and runs on every supplied/sample frame. Tiny details may disappear. Generic `boat` output does not establish a vessel subtype.

Built-ins request CPU execution and disable OpenCL. Default `--threads 0` disables OpenCV parallel regions; positive limits depend on the build's threading backend. Requested and reported settings are recorded. This does not constrain custom-provider runtimes or emulate another CPU. [OpenCV thread-control documentation](https://docs.opencv.org/4.13.0/db/de0/group__core__utils.html)

## Inputs and policy changes

Manifest paths are relative to the manifest unless absolute. IDs must be unique; source groups should identify the original recording/camera. Supply timestamps only when known.

```json
{"id":"clip-a:000100","path":"images/frame_000100.jpg","source":"clip-a","timestamp_s":10.0}
```

```bash
.venv/bin/python -m ghost_priority analyze --manifest /path/to/manifest.jsonl --out runs/manifest
.venv/bin/python -m ghost_priority analyze --video /path/to/clip.mp4 --fps 1 --limit 120 --out runs/video
.venv/bin/python -m ghost_priority rank --records runs/with-detector/records.jsonl --policy policies/boats.json --ranking-only --out runs/boats-ranking
```

Video timestamps derive from container FPS. Extract variable-frame-rate video with authoritative timestamps before evaluation. Reading/skipping compressed frames still costs CPU. The analyzer does not infer tracks, velocity, persistence or event boundaries. Exactly constant images are marked unusable and excluded from all ranking baselines; low-contrast images remain eligible.

The UI can override policy weights and rerank cached evidence. A direct object relevance score cannot be reinterpreted through category weights; providers should return detections without a direct score when operator category weighting is desired. The synthetic UI mockup remains separate. Its simulated subtype, approaching, loitering and AIS fields are not established by this baseline.

## Absolute bandwidth

`available_bandwidth_bps` is the effective outbound **payload allocation to this image service**, after other traffic, protocol overhead and retries. It is not total ship radio capacity.

```text
budget_bytes = floor(available_bandwidth_bps × window_seconds / 8)
```

| Preset | Payload rate | Allowance over 60 seconds |
|---|---:|---:|
| `normal` (default) | 2 Mbps | 15,000,000 bytes |
| `degraded` | 256 kbps | 1,920,000 bytes |
| `ddil` | 64 kbps | 480,000 bytes |
| `severe-ddil` | 9.6 kbps | 72,000 bytes |
| `disconnected` | 0 bps | 0 bytes |

These are adjustable hackathon scenarios, not measured or specified Navy application rates. Decimal units apply. Selection walks the fixed ranking, skips whole JPEGs that cannot fit, and preserves relative order. Zero bandwidth selects nothing. The caller retains unselected evidence; no live queue, sender, pacing, acknowledgements or accumulated transmission credit is implemented.

## Evaluation

`rank` compares priority order, uniform representative order, visual-interest-only order (method `novelty`), and object-only order (`detector`) when every eligible record has object evidence. Every method uses identical JPEG representations and byte limits. This is an offline batch benchmark; no files are transmitted, and JPEG bytes do not establish savings against compressed video.

Labels are a separate JSON object keyed by observation ID:

```json
{
  "clip-a:000100": {"interesting": true, "classes": ["boat"], "event_id": "reviewed-event-a"},
  "clip-a:000101": {"interesting": false, "classes": []},
  "clip-a:000102": {"interesting": null}
}
```

```bash
.venv/bin/python -m ghost_priority select --ranked runs/ranking/ranked-priority.jsonl --labels /path/to/reviewed-labels.json --bandwidth-preset ddil --window-seconds 60 --out runs/evaluated
```

Labels enter evaluation after selection. Unreviewed observations are not negatives. Coverage means an annotated observation was selected; it is not bounding-box AP, detector recall or proof that a small object remains visible in the encoded JPEG. Event coverage requires independently reviewed event IDs.

To import the SMD directory containing `index.csv`:

```bash
.venv/bin/python -m ghost_priority.smd --root /path/to/SMD_VIS_Onboard_frames --out runs/smd-input
.venv/bin/python -m ghost_priority analyze --manifest runs/smd-input/manifest.jsonl --model models/nanodet.onnx --out runs/smd-evidence
.venv/bin/python -m ghost_priority rank --records runs/smd-evidence/records.jsonl --labels runs/smd-input/presence_labels.json --bandwidth-preset degraded --window-seconds 60 --out runs/smd-evaluation
```

Its proxy is **at least one source-annotated object**, not operator relevance. Raw annotations remain outside the analyzer manifest. Category IDs do not become event IDs. Consult [DATA_NOTES.md](DATA_NOTES.md) before interpreting results.

## Outputs and limits

| Artifact | Meaning |
|---|---|
| `records.jsonl`, `analysis_summary.json` | Evidence, provider identities, environment and analysis timings |
| `ranked-{method}.jsonl`, `ranking_summary.json` | Complete orders and ranking timings before selection |
| `priority-bandwidth.jsonl` and other baseline subsets | Selections from the combined `rank` command |
| `selected.jsonl` | Selection from a saved ranking via `select` |
| `selection_summary.json`, `comparison.json` | Budget use, source counts, separate selection timing and optional label metrics |
| `report.html` | Full-ranking gallery or selection comparison |

Analysis, ranking and selection are measured separately. These timings describe the machine used, not certification for an embedded target. Without reviewed labels reports make no accuracy claim. Historical measurements in `DATA_NOTES.md` used the older combined selector and are not results for the separated implementation.

Known limits: own-ship structures, waves, glare and overlays may dominate signals; fingerprints can miss small changes; resizing loses detail; full priority ranking has quadratic time cost and keeps the pool in memory. Use source-grouped held-out data and benchmark on the eventual edge CPU.

## Compatibility

`policy.select_records()` remains a wrapper around full ranking followed by selection. `novelty_weight` aliases `visual_interest_weight`; conflicting values are rejected. Removed `coverage_fraction` and `byte_cost_exponent` keys raise migration errors: the full ranking has neither budget reserves nor byte-size preferences.

Historical dataset-fraction budget definitions remain available through `rank --fractions 0.01 0.05 0.10`. This preserves those allowances, not the earlier selector's results. Fraction mode is mutually exclusive with bandwidth/window options, and its allowance grows with the candidate pool. Absolute bandwidth is the primary deployment-facing interface.
