# Color + pHash similarity

The default matcher uses **both the existing absolute-color fingerprint and a 64-bit DCT perceptual hash (pHash)**. Object relevance, visual interest and quality still determine the base score. Own-ship masking remains opt-in and is not enabled by this change.

## Evidence and comparison

Analysis resizes the original image to the configured longest-side cap (320 by default) once for these two descriptors:

- `fingerprint`: the existing 8×8×3 normalized BGR layout, rounded to five decimals. Its values and preprocessing are unchanged.
- `phash`: version, 16 hexadecimal digits, and analysis-size metadata. A 32×32 grayscale DCT supplies the upper 8×8 coefficients. DC is zeroed and coefficients are thresholded against their mean to form 64 bits. This uses core OpenCV and NumPy; no additional package or model download is required.

Comparison operates only on these cached values, using Python's standard library. Hashes are decoded once per image, then compared using XOR and bit count. `similarity.CachedSimilarity` separates `prepare(record)` from `compare(a, b)` so image preprocessing does not happen inside the pair loop.

```text
color_distance = mean(abs(color_a − color_b))
color_similarity = exp(−(color_distance / 0.08)²)

hash_distance = number of differing bits, from 0 to 64
phash_similarity = exp(−(hash_distance / 8.0)²)

combined_similarity = min(color_similarity, phash_similarity)
final_score = base_score × (1 − 0.85 × max_combined_similarity_to_earlier_ranked)
```

Taking the smaller component is a conservative agreement rule: either descriptor can limit suppression. Identical descriptors produce similarity 1 and retain 0.15× the base score. All eligible images stay in the complete ranking. There is no hard deletion threshold.

The scales are adjustable heuristics, **not calibrated probabilities or validated duplicate thresholds**. The pHash scale of eight bits is an initial engineering setting. At distances 0, 4, 8 and 16, its similarity is approximately 1.000, 0.779, 0.368 and 0.018. It must not reuse the color metric's 0.08 scale.

The earlier 0.90 penalty-strength experiment has not been adopted as the default. The default remains 0.85 while changing the representation. That experiment's artifacts are historical comparisons of the color-only matcher.

## Policy settings and audit fields

```json
{
  "similarity_method": "color-phash",
  "duplicate_strength": 0.85,
  "duplicate_distance": 0.08,
  "phash_distance_scale": 8.0
}
```

`similarity_method` accepts `color-phash` (default), `color` (legacy comparison) or `phash` (standalone experiment). Policy changes rerank cached records without rerunning any model. Object/visual/quality providers remain independently replaceable.

Each priority-ranked record includes `similarity_engine`, `max_similarity`, `nearest_prior_id`, and `similarity_match`. The match contains the combined score, component similarities, raw pHash distance and availability for the specific earlier image responsible for the largest penalty. With no positive match, the nearest ID and match are null. The HTML gallery displays the components. Comparison baselines do not apply similarity and do not retain stale audit fields.

The color-only mode preserves its prior handling of absent or dimensionally incompatible fingerprints: similarity is zero. Hybrid mode also requires compatible nonempty color vectors for an available match. Missing or malformed pHash descriptors raise an actionable error instead of silently changing matchers. Different pHash preprocessing sizes are rejected during comparison; prepare all records with the same analysis size. The hash version fixes DCT, resize, threshold and packing behavior.

## Migrate existing evidence

New `analyze` runs cache both descriptors automatically. Earlier evidence needs pHash added before default ranking:

```bash
python -m ghost_priority cache-similarity --records runs/old-evidence/records.jsonl --out runs/hybrid-evidence
python -m ghost_priority rank --records runs/hybrid-evidence/records.jsonl --ranking-only --out runs/hybrid-ranking
python -m ghost_priority select --ranked runs/hybrid-ranking/ranked-priority.jsonl --bandwidth-bps 64000 --window-seconds 60 --out runs/64kbps
```

Migration reads original images or original indexed video frames. It verifies original dimensions when present and rebuilds the existing color fingerprint exactly before adding pHash. Use `--analysis-size` if the old run used a cap other than 320. Missing inputs, changed inputs/preprocessing or an unsupported video seek fail explicitly. There is no silent fallback to resized/recompressed payload JPEGs. The fingerprint check is not a cryptographic proof of source identity.

The command writes a new evidence directory, preserves signals, detections, quality, payload sizes and original timings, and resolves file paths. It never invokes object/visual/quality providers. Its own elapsed time is recorded separately. Saved rankings are rejected as migration input; migrate evidence, then rebuild the ranking. To use old evidence without migration, explicitly select `{"similarity_method":"color"}` in a policy file.

## Evaluate repeated versus changed information

Keep pair labels outside the evidence and ranking pipeline:

```json
[
  {"a":"frame-a", "b":"frame-b", "label":"same_information", "group":"reviewed-clip-1"},
  {"a":"frame-a", "b":"frame-c", "label":"changed_information", "group":"reviewed-clip-1"},
  {"a":"frame-b", "b":"frame-c", "label":"uncertain", "group":"reviewed-clip-1"}
]
```

```bash
python -m ghost_priority evaluate-similarity --records runs/hybrid-evidence/records.jsonl --pairs reviewed-pairs.json --out runs/pair-review
```

The report compares color, pHash and their combination across explicit probe thresholds, overall and by review group. It separately counts recognized duplicate candidates and **changed pairs incorrectly treated as duplicate candidates**. Uncertain pairs are excluded from positive/negative rates. Labels are never inferred from temporal adjacency, source names or common object categories.

Thresholds here are diagnostics; they do not change the continuous ranking policy. These metrics are not actual image-selection recall. Split calibration and validation by independent recordings, and preserve a separate held-out reviewed set before claiming improved quality.

## Known limits and CPU cost

The conservative combination can reduce broad-color false matches, but it can also miss legitimate duplicates that either component dislikes. In particular, retaining the absolute-color check means pHash's brightness tolerance does not automatically make the combination brightness-invariant. Local changes can be diluted by both whole-image summaries; a matching hash does not prove unchanged content.

The implementation check includes controlled copies, brightness variants and 8×8 local markers, plus several reviewed scene pairs. Small markers remain near-perfect matches and are explicitly reported as false suppression candidates. These probes expose a limitation; they do not establish detector accuracy or generalization.

The CPU study measured pHash preparation at approximately 0.023 ms per existing 320px preview on an Apple M2, and pure hash comparisons at approximately 0.05 microseconds per pair. The hybrid still computes the original color metric and ranking bookkeeping, so it does not inherit the pHash-only all-pairs speedup. Timings on an eventual modest edge CPU must be measured directly.

Algorithm references: [OpenCV image hashing](https://docs.opencv.org/4.13.0/d4/d93/group__img__hash.html), [OpenCV DCT pHash formulation](https://github.com/opencv/opencv_contrib/blob/4.13.0/modules/img_hash/src/phash.cpp).
