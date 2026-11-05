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

## Comparison

(filled in below from `make demo-compare`)
