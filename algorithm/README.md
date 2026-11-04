# CPU prioritization baseline

This prototype ranks RGB observations with a default policy, then lets a UI change that policy without rerunning image analysis. It supports independent images, JSONL manifests, and sampled video. It runs locally with no GPU, NPU, cloud inference, or training requirement.

The default experiment combines spatial visual novelty, optional generic object detections, image quality, and similarity to already selected images. Novelty means visual distinctiveness, not reliable detection or identification of an unknown object. Scores are uncalibrated heuristics, not probabilities or threat assessments.

## Quick start

Use Python 3.11 or newer. From this `algorithm` directory:

```bash
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements.txt
.venv/bin/python -m unittest discover -s tests -v
.venv/bin/python -m ghost_priority analyze --images /path/to/images --out runs/evidence
.venv/bin/python -m ghost_priority rank --records runs/evidence/records.jsonl --out runs/default
```

Open `runs/default/report.html` in a browser. Output directories must be new to avoid overwriting an earlier experiment. The report references JPEG payloads in the evidence directory, so keep both directories when sharing it.

Ranking defaults to an absolute **2 Mbps application payload allowance over 60 seconds** (15,000,000 bytes). Use `--fractions 0.01 0.05 0.10` to reproduce the original relative-budget experiments.

The requirements file pins the versions tested in this prototype. Python wrappers call compiled OpenCV/NumPy routines; this is not an embedded-CPU certification.

The default `--threads 0` disables OpenCV parallel regions for a conservative CPU baseline. Positive thread counts depend on the build's threading backend; macOS GCD does not support positive limits. The summary records both requested and reported settings. See the [OpenCV thread-control documentation](https://docs.opencv.org/4.13.0/db/de0/group__core__utils.html). This does not cap every third-party runtime or emulate another CPU.

## Add the optional detector

The adapter uses the exact OpenCV Zoo 2022nov NanoDet model at 416×416. The inference wrapper and Apache license are in `ghost_priority/third_party/`. Model files are ignored by Git.

```bash
mkdir -p models
curl --fail --location https://media.githubusercontent.com/media/opencv/opencv_zoo/48b10fa505aeb51fbd127fc60a5c660040e823b7/models/object_detection_nanodet/object_detection_nanodet_2022nov.onnx -o models/nanodet.onnx
.venv/bin/python -m ghost_priority analyze --images /path/to/images --model models/nanodet.onnx --out runs/with-detector
.venv/bin/python -m ghost_priority rank --records runs/with-detector/records.jsonl --out runs/comparison
```

The classical path uses a longest-side cap of 320 pixels. The detector separately letterboxes the original input to 416×416. OpenCV CPU execution is requested and OpenCL is disabled. Tiny objects can disappear in either resize. The optional detector evaluates every supplied/sample frame in this first baseline; selective invocation is a later ablation.

## Add data without changing code

Each JSONL manifest row describes one observation. Paths are relative to the manifest unless absolute. Use unique IDs and a source group identifying the original recording/camera. Only provide timestamps when known.

```json
{"id":"clip-a:000100","path":"images/frame_000100.jpg","source":"clip-a","timestamp_s":10.0}
```

```bash
.venv/bin/python -m ghost_priority analyze --manifest /path/to/manifest.jsonl --out runs/manifest
.venv/bin/python -m ghost_priority analyze --video /path/to/clip.mp4 --fps 1 --limit 120 --out runs/video
```

Video sampling derives timestamps from container FPS, performs spatial scoring, and can suppress redundant views at selection time. It does not infer tracking identities, persistence, velocity, or event boundaries. Variable-frame-rate video should be extracted with authoritative timestamps before evaluation. Reading/skipping compressed frames still costs CPU; the report includes that work.

Exactly constant images (including all-black frames) are reported and excluded equally from every selection baseline. Merely low-contrast images remain eligible.

## Policy contract for the UI

`analysis.Analyzer.analyze(bgr)` produces policy-independent evidence. `policy.score_record(record, policy)` computes an image priority and reasons. `policy.select_records(records, budget_bytes, policy)` performs deterministic offline batch selection. The UI can send a validated JSON policy override and rerank existing evidence:

```bash
.venv/bin/python -m ghost_priority rank --records runs/with-detector/records.jsonl --policy policies/boats.json --out runs/boats
```

The example changes the weights of supported generic `boat` and `person` classes. It cannot turn `boat` into a specific vessel subtype. All category weights are soft preferences; the independent novelty path remains eligible. Policy updates do not introduce recognition capabilities or require additional inference.

Evidence fields:

- `signals`: local contrast, spectral saliency, quality, and spatial novelty in [0,1].
- `detections`: `null` when no detector ran; otherwise a list (possibly empty) of `{label, confidence, bbox_xywh}` in original-image pixel coordinates.
- `regions`: visual candidate boxes. These are not object detections or ground truth.
- `fingerprint`: compact absolute color layout for soft duplicate suppression; never use it as a hard object gate.
- `usable`, `unusable_reason`, `engine`, `timing_ms`, image dimensions, payload location and bytes.
- Selection adds rank, score, relevance, novelty, reasons, and selection lane.

The teammate's UI mockup remains unchanged. It currently uses synthetic subtype and behavior fields. Pass measured fields only; do not fabricate `approaching`, `loitering`, identity, or subtype values from a generic detector. See `DATA_NOTES.md`.

## Absolute bandwidth control

The UI/platform variable is **`available_bandwidth_bps`**, an integer number of **outbound payload bits per second allocated to this image service**. Use decimal units: 1 kbps = 1,000 bps; 1 Mbps = 1,000,000 bps. Treat this as the effective allocation after other ship traffic, transport overhead and retries have been accounted for. It is not the ship's total radio capacity or download speed.

The selector uses `budget_bytes = floor(available_bandwidth_bps * window_seconds / 8)`. The default window is 60 seconds. This budget stays fixed when more images arrive; it does not grow with the dataset. Settings and the resulting byte allowance appear in `comparison.json`, `selection_summary.json`, and the HTML report. Absolute-mode selections are saved as `priority-bandwidth.jsonl` and equivalent files for the other baselines.

Illustrative hackathon presets, all adjustable:

| Preset | Application payload allowance | JPEG budget over 60 s |
|---|---:|---:|
| `normal` (default) | 2 Mbps | 15,000,000 bytes |
| `degraded` | 256 kbps | 1,920,000 bytes |
| `ddil` | 64 kbps | 480,000 bytes |
| `severe-ddil` | 9.6 kbps | 72,000 bytes |
| `disconnected` | 0 bps | 0 bytes |

These rates are **test scenarios, not measured or specified destroyer bandwidth**. They let a small image application range from a useful connected allocation to severe restriction without assuming it owns the whole ship's link. For public context, the [2017 Navy Program Guide](https://media.defense.gov/2020/May/18/2002302043/-1/-1/1/NPG17.PDF), page 101, described ADNS Increment III aggregate capacity of 25 Mbps for unit-level ships; that historical aggregate figure is not an available application uplink guarantee. [DON CIO's discussion of DDIL](https://www.doncio.navy.mil/mobile/ContentView.aspx?ID=15161&TypeID=21) describes low bandwidth, high latency and disconnects. It does not prescribe these presets.

```bash
.venv/bin/python -m ghost_priority rank --records runs/evidence/records.jsonl --bandwidth-bps 64000 --window-seconds 60 --out runs/64kbps
.venv/bin/python -m ghost_priority rank --records runs/evidence/records.jsonl --bandwidth-preset disconnected --out runs/offline
```

For direct UI/platform integration:

```python
from ghost_priority.bandwidth import bandwidth_budget_bytes
from ghost_priority.policy import select_records

available_bandwidth_bps = 64_000  # UI setting or measured payload allocation
budget_bytes = bandwidth_budget_bytes(available_bandwidth_bps, window_seconds=60)
selected = select_records(records, budget_bytes, policy)
```

Changing the rate only reranks cached evidence. A zero rate selects no payloads and leaves evidence on disk. If one whole JPEG exceeds the window's budget, it cannot be selected; fragmentation or carrying credit across windows belongs in the platform scheduler. This does not implement network pacing, a persistent queue, or a time-varying DDIL simulation. For changing connectivity, the platform should supply updated rates per scheduling window and remove acknowledged payloads from the pending set; a constant low rate does not model outages or latency.

## Compare methods and incorporate labels

The `rank` command compares uniform representative sampling, novelty-only selection, the default policy, and detector-only selection when all records include detector results. Every method sees the same JPEG representations and byte limit. The default is the absolute `normal` bandwidth preset over 60 seconds; `--fractions 0.01 0.05 0.10` explicitly selects the original percentage limits instead. Fraction mode and bandwidth mode are mutually exclusive.

This is an **offline batch benchmark**, not a causal live queue or a simulation of changing connectivity. Payload bytes exclude metadata, network overhead and retries; they do not establish savings against a compressed video stream. No images are transmitted.

Optional evaluation labels are stored separately as a JSON object keyed by observation ID:

```json
{
  "clip-a:000100": {"interesting": true, "classes": ["boat"], "event_id": "reviewed-event-a"},
  "clip-a:000101": {"interesting": false, "classes": []},
  "clip-a:000102": {"interesting": null}
}
```

```bash
.venv/bin/python -m ghost_priority rank --records runs/with-detector/records.jsonl --labels /path/to/reviewed-labels.json --out runs/evaluated
```

Labels enter only evaluation after selection. Unreviewed observations are not counted as negatives. An absent detection annotation does not establish an empty image. Class/event coverage measures whether an annotated observation ID was selected; it is not bounding-box AP, detector recall, or pixel localization accuracy. It also does not prove that a tiny annotated object remains visible in the resized JPEG. Review transmitted detail separately. Do not invent event IDs from archive filename order.

When detection annotations arrive, inspect their format and completeness, then map reviewed image relevance/classes into this schema. Add localization evaluation against the original boxes as a separate experiment. Keep source recordings grouped when splitting development and test data.

### Import the new SMD labelled subset

The repository's `frames/SMD_VIS_Onboard_frames.zip` is a Git LFS archive. Retrieve the actual archive, extract it, and use its directory containing `index.csv`:

```bash
.venv/bin/python -m ghost_priority.smd --root /path/to/SMD_VIS_Onboard_frames --out runs/smd-input
.venv/bin/python -m ghost_priority analyze --manifest runs/smd-input/manifest.jsonl --model models/nanodet.onnx --out runs/smd-evidence
.venv/bin/python -m ghost_priority rank --records runs/smd-evidence/records.jsonl --labels runs/smd-input/presence_labels.json --out runs/smd-evaluation
```

The importer separates raw object/horizon annotations from the analyzer manifest. Its explicit evaluation proxy is **at least one source-annotated object**. This is not an operator-relevance label. It preserves the original box coordinates and reports boundary issues. It does not create event IDs from the category-based `object_id` values. See the dated update in `DATA_NOTES.md` before interpreting metrics.

## Measurements and limits

`analysis_summary.json` records environment, initialization, CPU/wall time, process peak RSS, and per-image p50/p95 feature, detector, read, JPEG encoding and total times. Per-image total excludes output-file writing; whole-run wall/CPU times include it. Model initialization and any video decoding after the final sampled frame are reported separately. Batch selection time is recorded per comparison in `comparison.json`. These are measurements on the machine used, not estimates for a Raspberry Pi or another edge CPU. OpenCV's thread setting does not control all third-party threads.

`comparison.json` records budget use and source counts. `report.html` shows selected images and predictions. Without reviewed labels it does not claim accuracy, event recall, or superiority over uniform sampling.

Key limitations: camera-ship structures can dominate detections; waves/glare/overlays can dominate novelty; the model is trained on generic objects, not maritime subtype identification; fingerprints can miss small meaningful changes; full-image downsampling loses tiny features; the greedy selection method is a heuristic, and its representative reserve may be too small to fit a whole image. Batch ranking keeps records in memory and is not a bounded streaming deployment.

## Next experiment

1. Review a source-grouped subset and define image relevance independently of predictions.
2. Inspect selected and missed examples, especially own-ship structure, tiny objects, and lighting changes.
3. Compare recall-versus-bytes on held-out data; tune the default policy only on development groups.
4. Benchmark on the eventual edge CPU before changing precision or claiming a compute budget.
5. Integrate the ranker's evidence/reasons with the UI and the platform's live scheduler.
