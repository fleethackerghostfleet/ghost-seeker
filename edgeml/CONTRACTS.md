# edgeml contracts (v1)

Every workstream codes against this file. To change it, open a PR against `edgeml/main` that edits this
file and explains why.

## 0. Ground rules
- Python ≥3.10. Package `edgeml` under `edgeml/src/`. Install with `pip install -e edgeml[...]` and keep optional
  heavy deps in extras (`[gpu]`, `[openvino]`, `[eval]`, `[label]`).
- Reuse existing libraries before writing your own: TrackEval (HOTA/IDF1), pycocotools / faster-coco-eval (mAP),
  supervision / boxmot (trackers), SAHI (tiling), ultralytics / transformers / timm / open_clip (models), and
  OpenVINO NNCF (INT8).
- Stay **model-agnostic**. Every model sits behind an adapter that implements the protocols below. Licensing
  doesn't matter for now.
- Do not modify `algorithm/`, `edge/`, `shore/`, `proto/`, `web/`, `cmd/`. PRs target the `edgeml/main` branch.
- **No overfitting.** Tune thresholds and pick models on `dev` only. `test` sources in `configs/splits.yaml` are
  evaluated once per candidate at the end, and the test numbers are reported but never tuned on.

## 1. Types (`edgeml/src/edgeml/types.py`)
- Boxes are pixel `[x, y, w, h]` in original image coordinates. This matches the catalog (`ghost_fleet.annotations.box`).
- `Detection(box, score, label, label_id=None, embedding=None)`
- `Track(track_id, box, score, label, frame_idx, t_s, state)` where `state` is one of `tentative|confirmed|lost|removed`.
- `Sighting` holds one record per object per continuous pass. JSONL, one line per *event*:
  `{sighting_id, clip_id, event: "open"|"update"|"close", t_s, frame_idx, label, label_scores{}, subtype?,
    unknownness, box, best_frame_idx, best_box, best_score, first_t_s, last_t_s, n_frames, track_ids[]}`.
  `realness` (0..1 trajectory persistence/size/motion consistency) and `unknown_real` (unknownness and realness thresholds both passed) accompany every event; only real unknowns receive the unknown priority bonus.
  Optional lookout-report fields (see `reports/literature.md` §7): `bearing_deg` (box centre x + camera FOV, when
  known), `size_class` (by box-area bucket), `aspect` (bow/stern/beam, if the subtype model can say), `closing`
  (sign of box-area growth).
  `open` is the first report. `update` is sent only on a *material* change: a change in `size_class`, `aspect` or
  `closing`, a label flip, a confidence jump > 0.2, a new subtype, or a best key frame whose quality improves by more
  than `keyframe_improve_frac` (default 0.3). `close` is sent when the object is gone longer than `close_after_s`.
  A track re-acquired within `relink_gap_s` that matches by appearance **and** position continues the same sighting;
  prefer a missed re-link (one extra report) over a wrong one (merging two contacts).
  Key-frame quality is a cheap proxy, `q = score × sqrt(area) × sharpness × border_margin`; the first frame with
  `q ≥ q_min` is reported immediately so time-to-first-report is not delayed by waiting for the best shot.
- Every `open`/`update` event also carries the inputs for the **selection step** (bandwidth-aware, see §5b):
  `value` (0..1, model-agnostic priority estimate from confidence, unknownness, size/closing; operator interest is
  applied later by the scheduler) and
  `forms: [{kind: "fact"|"crop"|"thumb"|"full", bytes, quality, w, h, uri?}]`. These are pre-encoded payloads with
  **exact byte sizes**, e.g. a fact of ~300 B JSON, a crop of the best box with context at JPEG q≈50/80, a thumbnail
  of the key frame, and the full key frame.

## 2. Interfaces (`edgeml/src/edgeml/interfaces.py`)
```python
class Detector(Protocol):
    name: str
    def detect(self, image_bgr: np.ndarray) -> list[Detection]: ...
class Tracker(Protocol):
    def update(self, dets: list[Detection], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[Track]: ...
class Embedder(Protocol):
    dim: int
    def embed_crops(self, crops: list[np.ndarray]) -> np.ndarray: ...      # L2-normalised
    def embed_text(self, texts: list[str]) -> np.ndarray: ...
class Aggregator(Protocol):
    def update(self, tracks: list[Track], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[dict]: ...  # sighting events
    def flush(self, t_s: float) -> list[dict]: ...
```
`edgeml.pipeline.run_clip(frames, detector, tracker, aggregator, embedder=None, detect_every=1)` is the reference
runner. The same code runs on GPU in GCP and on CPU (ONNX Runtime / OpenVINO) for the laptop.

## 3. Eval data (GCS: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/`)
One directory per sequence (clip), in **MOTChallenge layout** so TrackEval works unchanged:
```
edgeml/eval/<set_name>/<seq_id>/
  seqinfo.ini            name, frameRate, seqLength, imWidth, imHeight, imExt=.jpg, source_dataset, license
  img1/000001.jpg ...    frames (contiguous, 1-based)
  gt/gt.txt              frame,id,x,y,w,h,conf(1=evaluate,0=ignore),class_id,visibility
  gt/labels.json         {class_id: native_label}  + {"label_source": "human"|"public_gt"|"teacher+human"|"teacher"}
edgeml/eval/<set_name>/index.jsonl   one line per seq: seq_id, set, source_dataset, split (dev|test), fps, n_frames, n_tracks, viewpoint
```
- `id` is the object identity within a continuous pass. A human would call it "the same contact."
- The `label_source` must be honest. Teacher-only labels can be used for dev analysis but are never headline numbers.
- Detection-only sets (no identities) reuse the existing catalog (`ghost_fleet.images` / `annotations`) and are not copied.

## 4. Results (GCS: `edgeml/runs/<run_id>/`, plus a summary row in BigQuery `ghost_fleet.edgeml_results`)
```
edgeml/runs/<run_id>/config.json     detector, tracker, aggregator params, hardware, git sha
edgeml/runs/<run_id>/<seq_id>/dets.jsonl, tracks.txt (MOT format), sightings.jsonl
edgeml/runs/<run_id>/metrics.json
```
`run_id = <yyyymmdd-hhmm>-<workstream>-<short-desc>`.

## 5. Metrics
| Layer | Metrics |
|---|---|
| Detection | COCO mAP50, mAP50-95, recall@score≥τ by box-area bucket (tiny <16², small <32², medium, large), class-agnostic "vessel" AP |
| Tracking | HOTA, IDF1, MOTA, ID switches, Frag (TrackEval), plus HOTA at α=0.3 for tiny far boxes |
| **Sightings (headline)** | **object recall** (GT tracks with ≥1 matched sighting), **duplicates per object** (matched sightings − 1, averaged over GT tracks), **false sightings per minute**, **time-to-first-report** (s after the GT track becomes visible), **reports per object** (open+update events) |
| Edge cost | ms/frame p50/p95, sustained FPS at a 4-thread cap, peak RSS, model size. Measured on the laptop-proxy VM |

Sighting↔GT matching: a sighting matches the GT track that has the most IoU≥0.3 overlap over the sighting's frames, if that covers ≥30% of the sighting's frames.

## 5b. Two steps: detection → selection (bandwidth)
Assume a link API reports the bytes available right now (budget per window). **Detection** (this package) emits
deduplicated sightings with `value` and `forms`. **Selection** picks which forms to send under the budget. This is a
knapsack over value per byte, with an item's cheaper forms sent before its expensive ones (the Go scheduler already
ranks `priority × ladder_weight / bytes`). edgeml provides
`edgeml.select.greedy(events, budget_bytes, ladder_decay=(1, 0.5, 0.25, 0.1), max_bytes_per_object=None)` as the
reference selector for evaluation only. It must not grow into a second scheduler. `ladder_decay` multiplies the value
of each successive form of the same sighting (fact→crop→thumb→full) so bytes spread across objects before upgrading
one; `max_bytes_per_object` caps what one sighting may consume in a window. Both exist because plain value/byte
breaks at 72 KB/min, where a single `full` frame is the whole budget (`reports/literature.md` §2.4); report results
with and without them.

Headline system metric: **unique GT objects delivered to shore vs budget.** An object counts as delivered when a
crop or full frame containing it is sent, and separately when a fact is sent. Measure it at windows of 60 s × {9.6 kbps,
64 kbps, 256 kbps, 2 Mbps} (72 KB, 480 KB, 1.92 MB, 15 MB), plus bytes per delivered object. Compare the same greedy
selector fed by (a) naive per-frame detections (full frames), (b) image-level near-duplicate suppression, and
(c) sightings.

## 6. Edge hardware assumption
Dell Latitude 7230 Rugged Extreme-class laptop: Intel 12th-gen U-series (e.g. i7-1260U, 2P+8E cores), 16 GB RAM, no
dGPU, shared with the Go edge and other ship workloads. **ML budget: ≤4 threads, ≤2 GB RSS, ≥2 processed FPS
per camera.** Laptop proxy in GCP: `n2-standard-8` or `c3-standard-8` (Intel), benchmarked with 4 threads pinned.

## 7. GCP usage
Project `qwiklabs-gcp-02-34a870014566`, zone `us-central1-a`. Name every resource `edgeml-<workstream>-*` and label it
`team=edgeml,workstream=<a|b|c|d>`. Each workstream gets at most 1 L4 GPU VM plus a small CPU VM unless the coordinator
approves more. Set an idle auto-shutdown and **delete VMs when done**. Never touch `ag-vm-*` or `lfs-setup-vm`. Do not
write to `catalog/`, `raw/`, `datasets/` or `frames/` except through the workstream-A ingest path described in its brief.
