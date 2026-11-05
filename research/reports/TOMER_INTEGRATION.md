# Tomer's CPU image prioritization in the live demo (`tomer` mode)

`algorithm/ghost_priority` (Tomer Atzili, PR #29) is a standalone, offline pipeline:
providers `analyze(original BGR)` -> `ObjectResult` / `VisualResult` / quality ->
`policy.rank_records` (deterministic full ranking, soft near-duplicate suppression via
color + pHash) -> `selection.select_ranked(ranked, budget_bytes)` (whole-JPEG pick under an
absolute byte allowance). This note describes how it is wired into the running edge as a
**selectable prioritization mode**, so operators can flip between `edgeml` and `tomer` and
compare. Nothing under `algorithm/` is modified; it is imported as-is from the repo root.

## Where it sits

```
cmd/edge ──► edge/ingest ──► sidecar InferenceService.Analyze ──► edge/scheduler ──► shore
                 │                 │  engine: edgeml | mock
                 │                 └─ prioritizer: edgeml | tomer | both  (new, additive)
                 └── -prioritizer edgeml|tomer : which score drives Score.Priority
```

* **Sidecar provider** (`edgeml/src/edgeml/sidecar/tomer.py`): `TomerPrioritizer` lazily
  imports `ghost_priority` from `<repo>/algorithm` (or `$GHOST_PRIORITY_PATH`); no new
  dependency group — it only needs the NumPy/OpenCV already in the sidecar venv. Per frame it
  calls `Analyzer.analyze(bgr)` (the stock providers: `NoObjects`, `ClassicalVisualInterest`,
  `SharpnessExposureQuality`; `--tomer-model` swaps in `NanoDetObjects`), then
  `policy.score_record` for the base score and `similarity.engine_from_policy(...).compare`
  against a bounded per-source window of previously **kept** frames. The final score is
  exactly his ranking term, `base * (1 - duplicate_strength * max_similarity)`; the only
  difference from `rank_records` is that "earlier ranked" becomes "earlier in time" because
  the edge sees a stream, not a pool. The policy JSON is his `DEFAULT_POLICY` unless
  `--tomer-policy` points at a file. Sidecar flag `--prioritizer edgeml|tomer|both`
  (default `edgeml`). `tomer` runs without any model (`--detector` not needed), `both` runs
  the edgeml engine and the Tomer provider on the same frame.
* **Wire format**: additive only. `AnalyzeResponse.image_priority` (new message
  `ImagePriority`: `engine`, `score`, `base_score`, `max_similarity`, `nearest_prior_id`,
  `near_duplicate`, `visual_interest`, `quality`, `object_relevance`, `latency_ms`) and
  `GetStatusResponse.prioritizer` (`"edgeml"` / `"tomer"`). `gen/` regenerated with the
  pinned `protoc-gen-go v1.36.12` / `protoc-gen-connect-go v1.18.1`; `make gen` is diff-clean.
* **Edge** (`cmd/edge -prioritizer edgeml|tomer`): in `tomer` mode the ingester copies the
  image-level numbers into `Features.Tags` (`tomer_score`, `tomer_sim`, `tomer_dup`, ...;
  they ride in the FACT so shore/UI can show *why*) and the scoring engine swaps
  `BaseValue+Unknown+Confidence` for a single `tomer` scorer whose value is his score.
  Operator interest (boost/suppress), freshness decay and shore pull boosts still apply —
  those are platform features, not prioritization. The form ladder (FACT/THUMB/CROP/FULL,
  priority-per-byte) is unchanged: Tomer's `select_ranked` picks whole JPEGs under an
  absolute allowance, the scheduler picks *forms* under a live link estimate — different
  contracts, so his score only sets the ordering. In `edgeml` mode `image_priority` is
  ignored except for the tags. The active prioritizer is reported in `GetStatus` and in the
  operator UI status strip (one-line hook; the console rebuild session owns the rest).
* **Dedup**: his suppression is *soft* (never deletes). On a 9.6 kbps link, though, the
  manifest + FACT of every 5 fps frame alone exceeds the link, so folding is needed in
  practice. Rule: `-prioritizer tomer` with `-dedup hash` keeps hash dedup (exact SHA-256
  fold) **and** additionally folds a frame whose `near_duplicate` flag is set (combined
  color+pHash similarity ≥ `--tomer-dup-threshold`, default 0.5, i.e. both descriptors agree)
  into its nearest prior item, counted in `items_deduped`. Non-folded near-matches keep the
  penalized score (soft suppression as in his ranking). `-dedup naive` disables folding
  (pure soft suppression); `-dedup sightings` is object-level and unchanged.
* **Headless comparison**: `cmd/sim -dedup tomer` (part of `all`) uses the real sidecar in
  `--prioritizer tomer` mode over Connect (`-infer`) instead of the mock, so the numbers
  come from his code, not a Go port. `scenarios/ghostfleet_demo/compare.sh` starts that
  sidecar automatically when `algorithm/` is present and adds the `tomer` row next to
  `naive / hash / sightings` at 9.6 kbps / 64 kbps / 256 kbps / 2 Mbps.

## Comparison (real numbers, `make demo-compare`)

`scenarios/ghostfleet_demo` (3 clips, 1270 frames at 5 fps = 254 s of footage, 6 ground-truth
objects), flat link at each preset, 6 simulated minutes, `prioritized` strategy, mock detector
for all rows (identical object bookkeeping); the `tomer` row's scores and near-duplicate
flags come from the real `ghost_priority` code through the sidecar (`--prioritizer tomer`,
no detector model, 10–25 ms/frame on this VM's CPU). "Frames dropped" = captures folded on
the edge before they ever reached the queue.

| link | mode | items sent | frames dropped | objects w/ image | all 6 at | bytes on air | bytes / delivered object |
|---|---|---:|---:|---:|---:|---:|---:|
| 9.6 kbps | naive | 1249 | 21 | 0/6 | never | 386 KB | – |
| 9.6 kbps | hash | 459 | 811 | 1/6 | never | 378 KB | 378 KB |
| 9.6 kbps | **tomer** | 313 | 957 | **3/6** | never | 378 KB | **126 KB** |
| 9.6 kbps | sightings | 19 | 1251 | 5/6 | never | 367 KB | 73 KB |
| 64 kbps | naive | 1249 | 21 | 3/6 | never | 2508 KB | 836 KB |
| 64 kbps | hash | 459 | 811 | 5/6 | never | 2466 KB | 493 KB |
| 64 kbps | **tomer** | 313 | 957 | 5/6 | never | 2461 KB | 492 KB |
| 64 kbps | sightings | 19 | 1251 | 6/6 | 303 s | 2431 KB | 405 KB |
| 256 kbps | naive | 1249 | 21 | 5/6 | never | 10831 KB | 2166 KB |
| 256 kbps | hash | 459 | 811 | 5/6 | never | 10786 KB | 2157 KB |
| 256 kbps | **tomer** | 313 | 957 | 5/6 | never | 10746 KB | 2149 KB |
| 256 kbps | sightings | 20 | 1250 | 6/6 | 242 s | 3049 KB | 508 KB |
| 2 Mbps | naive | 1249 | 21 | 5/6 | never | 83810 KB | 16762 KB |
| 2 Mbps | hash | 459 | 811 | 6/6 | 245 s | 64927 KB | 10821 KB |
| 2 Mbps | **tomer** | 313 | 957 | 6/6 | 242 s | 57168 KB | 9528 KB |
| 2 Mbps | sightings | 20 | 1250 | 6/6 | 240 s | 3038 KB | 506 KB |

Objects with an image on shore over time (t = 0 … 366 s of link time):

| link | naive | hash | tomer | sightings |
|---|---|---|---|---|
| 9.6 kbps | 0 0 0 0 0 0 0 | 0 0 0 0 1 1 1 | 0 1 1 1 1 1 3 | 0 2 3 4 5 5 5 |
| 64 kbps | 0 1 1 1 2 3 3 | 0 2 3 4 5 5 5 | 0 2 4 4 5 5 5 | 0 2 3 4 5 6 6 |

### Where each wins

* **Tomer vs. image-level hash dedup (the fair comparison — both are image-level, neither
  needs a detector):** his color+pHash near-duplicate suppression folds 957 of 1270 frames vs.
  811 for dHash, i.e. 313 items instead of 459, and his score ranks the remaining frames by
  visual interest × quality instead of treating them alike. At 9.6 kbps that is the difference
  between 1 and 3 objects imaged in six minutes (3× better bytes/object: 126 KB vs 378 KB);
  at 64 kbps he reaches 4 objects a minute earlier; at 2 Mbps the same 6/6 on 12 % fewer
  bytes. That is the strongest result in the table for a pipeline that costs ~15 ms of CPU
  per frame and zero model weights.
* **Where ours wins:** `sightings` is object-level — one item per tracked object plus a
  refresh ≤ every 30 s — so it sends 19–20 items where Tomer sends 313, and gets 5/6 at
  9.6 kbps and 6/6 at 64 kbps on 73–405 KB/object. An image-level score cannot know that the
  frame it is ranking shows the *same submarine* as 40 frames ago unless the pixels still look
  alike (they do not once the vessel turns or the camera pans), so Tomer's mode keeps
  re-sending the same objects at 256 kbps / 2 Mbps (57 MB vs 3 MB on air for the same 6/6).
  The form ladder (FACT → THUMB → CROP → FULL under the live link estimate) is orthogonal and
  applies to both; it is what lets *any* of the rows land something at 9.6 kbps.
* **Cost:** `tomer` runs on the CPU with no model; `sightings` needs YOLO + tracker + CLIP
  (~T4 or 4 CPU threads at 5 fps). On a platform without the model stack, `-prioritizer tomer
  -dedup hash` is the best available mode; with the model stack, his score is still useful as
  a tie-breaker / explanation (it rides in the tags in `both` mode) but should not replace the
  object-level identity.
* **What is not compared:** his `selection.select_ranked` (whole-JPEG byte allowance) is not
  on the edge path at all — the scheduler's per-form priority-per-byte selection stands in for
  it. His NanoDet object provider was not used (`--tomer-model` would enable it); the row is
  the model-free configuration he documents as the baseline.

## For the UI rebuild session

`GetStatus.prioritizer` (string, `"edgeml"` / `"tomer"`) is new; `internal/shorebridge`
forwards it as `edge.prioritizer` in the status JSON and `web/static/shore.js` prefixes the
edge-queue line with `prioritization: <mode>` — one line, replace freely. Per-item explanation
is in `Features.Tags`: `tomer_score`, `tomer_base`, `tomer_sim`, `tomer_dup`, `tomer_near`.

## Running it

```
make demo-sidecar                      # --prioritizer both (default now): edgeml engine + Tomer score on every frame
make demo-edge PRIORITIZER=tomer DEDUP=hash   # queue ranked by his score, near-duplicates folded
python -m edgeml.sidecar --prioritizer tomer  # model-free sidecar (what compare.sh starts on :9093)
make demo-compare                      # adds the tomer row when algorithm/ is checked out
```
