# Unknown-object detection: surfacing unknown contacts that aren't noise

Synthetic / UNCLASSIFIED prototype. The Go benchmark oracle is a written mission spec, not field truth, so none of its numbers are real-world accuracy. The edgeml numbers come from small public/dev clips (sample sizes below).

Requirement: "If an object is unknown but not noise it should still be prioritized as it could be important."

## What already existed
- `edgeml/src/edgeml/embed/zeroshot.py`: open-set `unknownness` (MCM over known-class text prototypes, calibrated against dev cosines, plus optional kNN-OOD bank and sea/sky background prompts). `edgeml/reports/D.md` has earlier AUROC work.
- `edgeml/src/edgeml/tracking/aggregator.py`: gave an unknownness bonus once a track was ≥2 s old. It had no shape or motion test.
- `internal/shorebridge`: unknown contacts are never shown as suppressed by a label policy (unknownThreshold 0.5).
- The Go catalog, scoring, models, benchmark and UI were closed-set. They had no `unknown` class.

## Approach
### Go app (catalog, scoring, benchmark, UI)
- `internal/catalog`: added an `unknown` class (group `unknown`, label **"UNKNOWN — not classified"**), weight 0.7 in all three models (maritime, air, sar), edge weight 0.65, and a `transient` attribute ("single-frame / noise-like").
- `internal/command/score.go`: a persistent unknown is at least P2. It is P1 if it is approaching or dark (no AIS). A transient unknown gets weight 0.05, so it lands at P4. Classifier confidence does not lower the score of an unknown, because low class confidence is what "unknown" means. Persistence versus transience is what separates a real object from noise. Known classes behave as before.
- `internal/command/parse.go`: "unknown objects/contacts" and "unclassified" now target the unknown class. "unknown vessel" still maps to `dark`.
- `internal/sensor`: the simulator sometimes injects unknown contacts (persistent, approaching or transient) with plausible boxes. Library imagery is unchanged. Persistent unknowns are not dropped at the edge.
- `internal/bench`: the oracle maps persistent unknown → P2, approaching/dark unknown → P1 and transient → P4. Unknowns are injected with a separate seeded RNG, so library frames and false positives stay reproducible. New flags: `-unknowns=false` (main-equivalent) and `-before-unknown` (unknowns injected but scored with the old closed-set weights). The benchmark also reports per-contact unknown metrics.
- `web/static`: unknown contacts get a dashed purple box and the label "UNKNOWN — not classified". "Why this priority" gets a reason line: either *"Unclassified contact persisted across frames — not noise; prioritized as potentially important"* or the transient/noise explanation.

### Edge ML (edgeml)
- New `edgeml/src/edgeml/tracking/realness.py`: a "not noise" test on a sighting's trajectory. It checks persistence (≥N distinct frames and ≥T s), size stability (coefficient of variation of sqrt(area)) and motion consistency (constant-velocity residual, normalised by box size). Stationary contacts are allowed. The output is `realness` in [0,1].
- `aggregator.py` / `types.py` / `CONTRACTS.md`: sightings and events gain `realness` and `unknown_real` (unknownness ≥ τ and realness ≥ ρ). The unknown priority bonus now applies only when `unknown_real` is true. A real unknown gets a value floor equal to a known contact at 0.6 confidence. Library defaults are N=3, ρ=0.5.
- `edgeml/tools/eval_unknown.py`: replays cached detections through ByteTrack and the aggregator, with MobileCLIP-S1 zero-shot MCM unknownness calibrated on 136 dev GT crops. It matches sightings to GT and reports per-split metrics. It checks splits against `configs/splits.yaml`.

## Measured results

### Go benchmark (synthetic oracle): `go run ./cmd/bench`, seeds 101,102,103 × 6000 frames
Unknown contacts (identical for maritime, air and sar):

| Mode | Command | Persistent kept at edge **and** ≤P2 | Approaching scored P1 | Transient scored P4 |
|---|---|---:|---:|---:|
| before (closed-set) | `go run ./cmd/bench -before-unknown` | 0.0% of 1428 (edge kept 93.4%) | 0.0% of 681 | 100% of 728 |
| after | `go run ./cmd/bench` | **100%** of 1428 (edge kept 100%) | **100%** of 681 | 100% of 728 |

Frame-level metrics:

| Model | Mode | P1 recall | P1 precision | Critical miss | NDCG@20 | Review burden |
|---|---|---:|---:|---:|---:|---:|
| maritime | main (`-unknowns=false`) | 89.7% | 75.4% | 0.0% | 0.979 | 52.2% |
| maritime | before | 80.2% | 76.3% | 3.8% | 0.980 | 87.4% |
| maritime | after | 91.5% | 78.7% | 0.0% | 0.990 | 54.6% |
| air | main | 91.5% | 92.6% | 0.0% | 0.979 | 21.4% |
| air | before | 75.2% | 93.0% | 15.3% | 0.981 | 85.4% |
| air | after | 93.5% | 94.4% | 0.0% | 0.988 | 25.4% |
| sar | main | 99.3% | 73.3% | 0.0% | 0.977 | 5.7% |
| sar | before | 56.4% | 74.7% | 30.1% | 0.832 | 85.7% |
| sar | after | 99.6% | 84.3% | 0.0% | 0.989 | 9.4% |

`TestModelsMeetFloor` passes. Caveat: the oracle encodes the same spec the scorer implements. These numbers show the implementation matches the spec. They do not show the spec is right.

### edgeml (real clips: ghost_fleet_5fps + fvessel_clip10, yolo11n conf 0.05, ByteTrack, MobileCLIP-S1 MCM)
Commands are run from the repo root with `~/edgeml-venv`. Data comes from `gs://…-ghost-seeker` eval sets.
```
python edgeml/tools/run_c.py cache --data ~/data/edgeml_eval --sets ghost_fleet_5fps fvessel_clip10 --split {dev,test} --detector yolo11n --conf 0.05 --name yolo11n-c05
python edgeml/tools/eval_unknown.py --data ~/data/edgeml_eval --split dev --out ~/unknown-eval
python edgeml/tools/eval_unknown.py --data ~/data/edgeml_eval --split test --out ~/unknown-eval --frozen ~/unknown-eval/frozen.json
```
Dev sweep: N ∈ {3,5,8} × ρ ∈ {0.3,0.5,0.7}. All nine settings tied on dev: 23/34 GT objects surfaced, 8/9 matched unknown sightings flagged, 0/391 false unknowns. The declared tie-break (stricter persistence) froze **N=8, T=2 s, ρ=0.7, τ=0.5**. Test was evaluated **once** with these frozen parameters and not re-tuned afterwards.

"Before" means unknownness ≥ τ alone (the current behaviour). "After" also requires `unknown_real`.

| Split | Mode | GT objects surfaced | Matched unknown sightings flagged | False unknowns / complete sea-only frame |
|---|---|---:|---:|---:|
| dev | before | 23/34 (67.6%) | 9/9 (100%) | 0/391 |
| dev | after | 23/34 (67.6%) | 8/9 (88.9%) | 0/391 |
| test | before | 27/52 (51.9%) | 9/9 (100%) | 0/214 |
| test | after | 26/52 (50.0%) | **4/9 (44.4%)** | 0/214 |

**Key finding:** at the frozen N=8 operating point, the realness gate cut held-out recall of real unknown objects from 9/9 to 4/9. It showed no reduction in false unknowns, because there were zero false unknowns before the gate on these complete sea-only frames. On these clips the gate costs recall and has not been shown to remove noise. Sample sizes are tiny: 9 unknown sightings, 391 dev and 214 test sea-only frames. Only 3 dev and 6 test sequences have complete GT. Unmatched flags on sequences with partial or unspecified GT are not counted as noise (dev 3 → 3, test 1 → 0).

## Limits
- There is no restricted-area or geofence data model, so "P1 in a restricted area" is **not implemented**. Today P1 comes from approaching or dark.
- In the Go app, persistence and transience are simulated attributes. The Go app does not yet consume edgeml `realness`/`unknown_real` through the shore bridge.
- Realness is geometric evidence. Smooth, persistent clutter can pass it. Camera motion, occlusion and changes in scale can reject real objects.
- The current eval data cannot support any claim about clutter or glint rejection: zero false unknowns before and after.

## What's left
1. Gather sea-clutter, glint and wake negatives with complete GT, then re-tune on dev. Pick N by recall cost: the library default N=3 was never evaluated on test, and N=8 failed there.
2. Wire edgeml `unknown_real` → Go `unknown` class (and `Transient = !unknown_real`) in `internal/shorebridge`.
3. Add a restricted-area polygon model so unknowns inside it get P1.
4. Add the kNN-OOD dev bank (`fit_bank`), which D.md found important for detector false positives.
