# Night navigation lights: method and measured results

**Synthetic demonstration, not operational accuracy.** Automatic light detections remain unreliable in clutter. Partial patterns are displayed but cannot raise priority. All accuracy numbers below are measured on the named procedural datasets, not estimates of performance at sea.

## Method

| Component | Implementation / behavior |
|---|---|
| Day/night and mode gate | `edgeml/src/edgeml/nightlights/core.py`: mean luminance and dark fraction; confident image statistics win. Ambiguous images use NOAA approximate solar elevation below −6° when timezone-aware timestamp and lat/lon are available, otherwise report image ambiguity. SAR/radar night is not applicable; IR may estimate night but has no colour rules. |
| Detector | CPU OpenCV: padded contact boxes, peak/local-background contrast, connected components, area/circularity/elongation/texture limits, maximum eight candidates, chromatic halo colour around saturated cores, contextual sodium suppression, full-image regular pier-row rejection and conservative 85%-box-height waterline prior. No reliable horizon/hull segmentation. |
| Flashing | Presence/absence over four registered frames. This can confuse occlusion or detector misses with flashing; no automatic flare or Morse recognition. |
| Interpreter | Ordered vertical groups; set/isolated sidelight patterns; flashing constraints; first matching rule in catalog order. Red port side implies conditional right-to-left crossing; green starboard side implies left-to-right. Offset whites assume higher aft/lower forward mastheads, not tow lights; two whites remain ambiguous. |
| Whole-contact boost gate | After matching, the complete detected colour multiset must equal the pattern multiset. **Zero white-light allowance**: no rule currently declares an allowance. Extra lights leave the rule ID/line visible but set boost to zero and low confidence: “extra unexplained lights — verify, no priority boost”. External signal-assisted distress is exempt. This is a conservative consistency check, not calibrated probability. |
| Go integration | `internal/catalog/lights.go` embeds the data into typed rules and a separate `NightLibrary`. `InterpretLights` mirrors Python; coordinates convert from 960×600 observations to 480×300. Sensor selects night frames about 20% of eligible EO-P7/BY-12 captures. Ordinary 48-frame `Library` is unchanged. |
| Scoring | Only known EO source + night=true can retain/score lights. `ScoreDet` and `RescoreAt` recheck the boost gate, including stale carried signals. Eligible positive boost b updates directive-adjusted weight w to 1−(1−w)(1−b), followed by confidence scaling and the existing score cap/frame aggregation. Day/unknown/IR/SAR cannot boost; SAR is marked not applicable. `DetectionScore.light` identifies actual light boosts. |
| UI | Compact feed badge, per-contact coloured light stack with pulsing flashing dots and reduced-motion support, plain line/reference/aspect/caveats. Urgent supported signals appear in Review's why list. Partial interpretations are muted with a low-confidence/no-boost tag, not an alert claim. |

### Rule data

Source: [colregs_lights.json](../../internal/catalog/colregs_lights.json). Boosts are **heuristic priority weights**, not probabilities. “Set” matching can select white lights or isolated sidelights to retain an interpretation; the separate boost gate still requires complete contact coverage.

| ID | Pattern / requirement | Reference | Meaning | Plain-language line | Severity | Base boost |
|---|---|---|---|---|---|---|
| ram | vertical red → white → red | Rule 27(b) | restricted in ability to manoeuvre — keep clear | Vessel showing red-over-white-over-red: restricted in ability to manoeuvre — keep clear | urgent | 0.8 |
| constrained_draught | vertical red → red → red | Rule 28 | constrained by draught — avoid impeding safe passage | Vessel showing red-over-red-over-red: constrained by draught — avoid impeding safe passage | urgent | 0.7 |
| nuc | vertical red → red | Rule 27(a) | not under command — keep clear | Vessel showing red-over-red: not under command — keep clear | urgent | 0.9 |
| trawling | vertical green → white | Rule 26(b) | engaged in trawling | Vessel showing green-over-white: engaged in trawling | advisory | 0.1 |
| fishing | vertical red → white | Rule 26(c) | fishing other than trawling | Vessel showing red-over-white: fishing other than trawling | advisory | 0.1 |
| pilot | vertical white → red | Rule 29 | pilot vessel on duty | Vessel showing white-over-red: pilot vessel on duty | advisory | 0 |
| tow_long | vertical white → white → white | Rule 24(a) | possible towing vessel, tow exceeds 200 m; verify towing lights and aspect | Vessel showing white-over-white-over-white: possible towing vessel, tow exceeds 200 m; verify towing lights and aspect | advisory | 0.1 |
| two_mastheads | set white + white | Rules 23(a), 24(a) | ambiguous: towing (vertical whites) or power-driven vessel 50 m or longer (forward/aft mastheads); geometry alone is insufficient | Vessel showing white + white: ambiguous: towing (vertical whites) or power-driven vessel 50 m or longer (forward/aft mastheads); geometry alone is insufficient | advisory | 0 |
| head_on | set red + green | Rules 21, 23 | both sidelights visible: approximately head-on; confirm risk and vessel type | Vessel showing red + green: both sidelights visible: approximately head-on; confirm risk and vessel type | advisory | 0 |
| port | set red | Rule 21(b) | her port side visible, likely crossing right to left; light alone does not establish right of way | Vessel showing red: her port side visible, likely crossing right to left; light alone does not establish right of way | advisory | 0 |
| starboard | set green | Rule 21(b) | her starboard side visible, likely crossing left to right; light alone does not establish right of way | Vessel showing green: her starboard side visible, likely crossing left to right; light alone does not establish right of way | advisory | 0 |
| white_ambiguous | set white | Rules 21(c), 23(d), 30 | ambiguous white light: stern, anchored vessel, or small power vessel; do not infer motion | Vessel showing white: ambiguous white light: stern, anchored vessel, or small power vessel; do not infer motion | advisory | 0 |
| air_cushion | set yellow; flashing required | Rule 23(b) | possible air-cushion vessel in non-displacement mode; verify other lights | Vessel showing yellow: possible air-cushion vessel in non-displacement mode; verify other lights | advisory | 0.05 |
| law_enforcement | set blue; flashing required | Local jurisdiction (non-COLREGS) | possible law enforcement where locally authorized; flashing blue is not a universal COLREGS signal | Vessel showing blue: possible law enforcement where locally authorized; flashing blue is not a universal COLREGS signal | advisory | 0.1 |
| distress_flare | External red_flare | Annex IV 1(c), 1(i) | red flare reported: distress — render assistance as safe | Vessel showing red flares: red flare reported: distress — render assistance as safe | urgent | 1 |
| distress_sos | External sos | Annex IV 1(d) | SOS reported by flashing light: distress — render assistance as safe | Vessel showing SOS: SOS reported by flashing light: distress — render assistance as safe | urgent | 1 |

## Data and protocol

| Dataset | Seeds / samples | Rendering and use |
|---|---|---|
| easy/dev | Base 1000; 112 night contacts, four frames each + 8 day images | Separated bright coloured lights; deliberately easy regression baseline. |
| easy/test | Base 100000; same counts | Locked easy test. |
| hard/dev | Base 300000; 112 night contacts + 8 day + 16 twilight images (136 gate cases) | Only split used for detector improvements and boost-gate design. |
| hard/test v1 | Base 900000; same counts as hard/dev | Evaluated before v2 development; preserved v1 reports, not reused to tune v2. |
| hard/test2 | Fresh base 2700000; same counts | Generated after detector v2 freeze and evaluated once for v2. New boost gate developed on hard-dev and evaluated on the existing frozen test2 observations **once**, after locking the gate; no detector rerun/tuning. |
| App demos | Eight 960×600 JPEGs, each <150 KB; 15 contacts | Moderate synthetic harbor/offshore scenes; observations separate from truth. Frames 05–08 are an externally identified SOS on/off proxy, not decoded Morse. |

Night-contact seeds add rule_index×100 + repetition (seven repetitions/rule); day/twilight offsets are 9000/9500. Generated datasets/crops/caches live under gitignored `data/nightlights/`. Hard scenes include tiny one-pixel to 1–3 px lights, defocus/motion blur, bloom/merged points, warm whites, orange reds, cyan greens, white saturated cores, reflections, sodium/white shore and pier rows, moon glints, partial occlusion, noise, JPEG compression and twilight.

Hard summary day-image counts were corrected from 16 to 8 using cached labels only: the 16 twilight images (eight labelled daytime) are counted separately, not again as dedicated day images. Totals remain 112 night contacts + 8 day + 16 twilight = 136 gate cases. Detection, interpretation, confusion and accuracy measurements are unchanged; no detector or Gemini rerun.

Unresolvable direct-light patterns are labeled, never removed: permanent occlusion or one-pixel saturation that destroys direct colour. Radius zero renders one pixel, not a subpixel source. Reflections/context/external distress can still give cues. Occluded emitters are excluded from observable-light recall, but full latent rule accuracy retains all contacts. Low SNR alone is not used to discard cases.

Full-frame light matching requires position within 6 px and correct colour. Wrong colour penalizes precision/recall; unmatched predictions populate the background row. Flashing accuracy counts missed observable lights as errors. Aspect includes unknown labels and is not heading accuracy. Automatic rule accuracy excludes 14 externally signaled distress cases.

## Measured full-frame / four-frame results

Historic detector v1/v2 metrics are preserved, **not rerun for the new boost gate**. The gate changes boost eligibility only, not light detections, rule IDs, aspect or flashing. v1 test versus v2 test2 are different datasets; only hard-dev gives a paired v1/v2 comparison.

| Report / dataset / candidate | Light P | Light R | Rules /112 | Automatic /98 | Aspect | Flashing | Night gate | Day false-night |
|---|---|---|---|---|---|---|---|---|
| easy_dev | 100.00% | 100.00% | 112 | 98 | 100.00% | 100.00% | 100.00% | 0.00% |
| easy_test | 100.00% | 100.00% | 112 | 98 | 100.00% | 100.00% | 100.00% | 0.00% |
| easy_test_v2 | 100.00% | 100.00% | 112 | 98 | 100.00% | 100.00% | 100.00% | 0.00% |
| hard_dev_v1 | 9.20% | 23.93% | 18 | 4 | 61.61% | 30.06% | 97.06% | 0.00% |
| hard_dev_v2 | 19.76% | 40.49% | 25 | 11 | 54.46% | 46.01% | 97.06% | 0.00% |
| hard_test_v1 | 8.72% | 24.07% | 21 | 7 | 67.86% | 34.57% | 96.32% | 0.00% |
| hard_test2_v2 | 13.72% | 31.33% | 24 | 10 | 53.57% | 42.17% | 97.06% | 0.00% |

### Per-rule correct /7 (each easy rule is 7/7)

| Rule | hard_dev_v1 | hard_dev_v2 | hard_test_v1 | hard_test2_v2 |
|---|---|---|---|---|
| ram | 0 | 1 | 0 | 0 |
| constrained_draught | 0 | 1 | 0 | 0 |
| nuc | 1 | 1 | 0 | 0 |
| trawling | 1 | 2 | 1 | 2 |
| fishing | 1 | 1 | 0 | 2 |
| pilot | 0 | 1 | 2 | 0 |
| tow_long | 1 | 1 | 2 | 1 |
| two_mastheads | 0 | 0 | 0 | 1 |
| head_on | 0 | 1 | 0 | 0 |
| port | 0 | 2 | 1 | 0 |
| starboard | 0 | 0 | 1 | 4 |
| white_ambiguous | 0 | 0 | 0 | 0 |
| air_cushion | 0 | 0 | 0 | 0 |
| law_enforcement | 0 | 0 | 0 | 0 |
| distress_flare | 7 | 7 | 7 | 7 |
| distress_sos | 7 | 7 | 7 | 7 |

### Position-matched colour confusion

Rows are truth, columns are predicted colour/miss. Background = unmatched predictions. Every easy split is diagonal: red 84, green 21, white 77, yellow 7, blue 7; zero background/misses.

**hard_dev_v1**

| Truth → prediction | Red | Green | White | Yellow | Blue | Miss |
|---|---|---|---|---|---|---|
| red | 9 | 0 | 2 | 6 | 0 | 52 |
| green | 0 | 1 | 0 | 0 | 0 | 15 |
| white | 0 | 0 | 24 | 2 | 0 | 42 |
| yellow | 0 | 0 | 1 | 2 | 0 | 1 |
| blue | 0 | 0 | 0 | 0 | 3 | 3 |
| background | 1 | 0 | 265 | 108 | 0 | 0 |

**hard_dev_v2**

| Truth → prediction | Red | Green | White | Yellow | Blue | Miss |
|---|---|---|---|---|---|---|
| red | 28 | 0 | 1 | 5 | 0 | 35 |
| green | 0 | 5 | 1 | 0 | 0 | 10 |
| white | 1 | 0 | 28 | 2 | 0 | 37 |
| yellow | 0 | 0 | 0 | 2 | 0 | 2 |
| blue | 0 | 0 | 1 | 0 | 3 | 2 |
| background | 55 | 0 | 196 | 6 | 0 | 0 |

**hard_test_v1**

| Truth → prediction | Red | Green | White | Yellow | Blue | Miss |
|---|---|---|---|---|---|---|
| red | 7 | 0 | 6 | 9 | 0 | 46 |
| green | 0 | 4 | 1 | 0 | 0 | 10 |
| white | 0 | 0 | 27 | 1 | 0 | 40 |
| yellow | 0 | 0 | 0 | 0 | 0 | 5 |
| blue | 0 | 0 | 0 | 0 | 1 | 5 |
| background | 0 | 0 | 277 | 114 | 0 | 0 |

**hard_test2_v2**

| Truth → prediction | Red | Green | White | Yellow | Blue | Miss |
|---|---|---|---|---|---|---|
| red | 12 | 0 | 0 | 9 | 0 | 52 |
| green | 0 | 13 | 0 | 0 | 0 | 7 |
| white | 4 | 0 | 21 | 6 | 0 | 33 |
| yellow | 0 | 0 | 0 | 2 | 0 | 2 |
| blue | 0 | 0 | 0 | 0 | 4 | 1 |
| background | 59 | 0 | 232 | 15 | 2 | 0 |

### Nuisance false positives and observability

| Split / detector | Shore FP | Pier FP | All background FP |
|---|---|---|---|
| hard_dev_v1 | 157 | 217 | 374 |
| hard_dev_v2 | 168 | 89 | 257 |
| hard_test_v1 | 143 | 248 | 391 |
| hard_test2_v2 | 187 | 121 | 308 |

Hard-dev v2 correctly coloured reds: 28/69 versus v1 9/69; red misses 35 versus 52. Red background FP rose 1→55; sodium confusion remains. Hard-test2 v2: 12/73 correctly coloured reds, 52 red spatial misses.

| Dataset / detector | Resolvable rules correct | Unresolvable rules correct |
|---|---:|---:|
| hard/dev v2 | 16/76 | 9/36 |
| hard/test2 v2 | 22/78 | 2/34 |

### Exact historical commands

From repository root; the Python executable was `/home/ubuntu/nightlights-venv/bin/python`. These are the commands that produced the archived measurements, not new runs against current code. v1 archives are copies of their original unversioned report outputs. Re-running historical commands on current code does not reconstruct the old detector; the split and tuning protocol is recorded above and in [PROTOCOL](../../edgeml/reports/nightlights/PROTOCOL.md).

**easy_dev** → `edgeml/reports/nightlights/easy_dev.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier easy --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier easy --split dev
```

**easy_test** → `edgeml/reports/nightlights/easy_test.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier easy --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier easy --split test
```

**easy_test_v2** → `edgeml/reports/nightlights/easy_test_v2.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier easy --split test --version v2
```

**hard_dev_v1** → `edgeml/reports/nightlights/hard_dev_v1.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier hard --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier hard --split dev
```

**hard_dev_v2** → `edgeml/reports/nightlights/hard_dev_v2.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split dev --version v2
```

**hard_test_v1** → `edgeml/reports/nightlights/hard_test_v1.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier hard --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier hard --split test
```

**hard_test2_v2** → `edgeml/reports/nightlights/hard_test2_v2.{json,md}`

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --tier hard --split test2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test2 --version v2
```

## Measured Gemini comparison: identical contact crops

Gemini 2.5 Flash on Vertex, temperature 0, thinking budget 0, max output 1024 tokens, JSON MIME, four workers, 400-call cap. Each request contains the same four native-resolution contact crops padded 20%, JPEG quality 95; our detector sees those identical decoded crops. No truth or external distress signal supplied. Both methods use **per-contact colour-count overlap**, not spatial matching (Gemini returned colour/y order, not precise locations). A wrong-location same-colour shore light can receive count credit. Do not compare this P/R directly with full-frame spatial P/R.

| Dataset | Our count P / R | Our rule accuracy | Gemini count P / R | Gemini rule accuracy |
|---|---|---|---|---|
| hard/dev | 26.74% / 63.80% | 9/112 (8.04%) | 46.63% / 46.63% | 20/112 (17.86%) |
| hard/test2 | 25.99% / 63.25% | 8/112 (7.14%) | 50.81% / 37.95% | 21/112 (18.75%) |
| easy/test | 100% / 100% | 98/112 (87.50%) | 100% / 100% | 91/112 (81.25%) |

| Dataset | Calls | HTTP failures | Invalid JSON | Mean latency | Median | p95 |
|---|---:|---:|---:|---:|---:|---:|
| hard/dev | 112 | 0 | 7 | 1.443 s | 1.237 s | 3.515 s |
| hard/test2 | 112 | 0 | 3 | 1.787 s | 1.277 s | 1.794 s |
| easy/test | 112 | 0 | 0 | 1.393 s | 1.328 s | 1.909 s |

336 calls total, no retries. Ten truncated MAX_TOKENS JSON responses count as empty detections/incorrect rules, not excluded cases. Cached under `data/nightlights/gemini/`; no new Gemini calls in this boost-gate round. Crop rule strata (ours/Gemini): dev resolvable 6/76 vs 18/76, unresolvable 3/36 vs 2/36; test2 resolvable 8/78 vs 18/78, unresolvable 0/34 vs 3/34. Compact per-colour/per-rule summary: [gemini_comparison.json](../../edgeml/reports/nightlights/gemini_comparison.json).

Exact command (datasets hard/dev, hard/test2, easy/test):
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_gemini.py --workers 4 --max-calls 400
```

## Measured boost-gate results (paired frozen v2 observations)

Gate design: hard-dev only; exact multiset coverage, no optional white allowance, no tuned numeric threshold. Only `interpret` changed in Python core; every other function (including detector, image gate, colour and track analysis) is byte-identical to the committed v2 implementation. Test2 was evaluated **once for this new gate**, after the dev result, using saved observations from `hard_test2_v2.json`. No test2-driven adjustments.

Definitions:
- **False boost:** positive boost and truth's table boost is zero **or** predicted rule differs from truth. Rate = false / boosted; also report false / all contacts.
- **Missed boost:** urgent truth (RAM, NUC, constrained draught, distress) with boost zero. Rate = missed / urgent truths. A wrong positive boost is not a “miss” under this literal definition, so correct-rule misses are reported separately.
- Advisory nonzero boosts count as boosts but are not urgent truths. Each split has 35 urgent truths: 21 automatic + 14 signal-assisted distress. “All” = 112 contacts; “automatic” = 98 without external signals. 0/0 is undefined, not perfect precision.

| Dataset / scope | Gate | Boosted | False / boosted | False / contacts | Missed / urgent | No correct boost / urgent |
|---|---|---|---|---|---|---|
| hard/dev · all | off | 37 | 16/37 (43.24%) | 16/112 (14.29%) | 14/35 (40.00%) | 18/35 (51.43%) |
| hard/dev · all | on | 14 | 0/14 (0.00%) | 0/112 (0.00%) | 21/35 (60.00%) | 21/35 (60.00%) |
| hard/dev · automatic | off | 23 | 16/23 (69.57%) | 16/98 (16.33%) | 14/21 (66.67%) | 18/21 (85.71%) |
| hard/dev · automatic | on | 0 | 0/0 (undefined) | 0/98 (0.00%) | 21/21 (100.00%) | 21/21 (100.00%) |
| hard/test2 · all | off | 34 | 15/34 (44.12%) | 15/112 (13.39%) | 18/35 (51.43%) | 21/35 (60.00%) |
| hard/test2 · all | on | 14 | 0/14 (0.00%) | 0/112 (0.00%) | 21/35 (60.00%) | 21/35 (60.00%) |
| hard/test2 · automatic | off | 20 | 15/20 (75.00%) | 15/98 (15.31%) | 18/21 (85.71%) | 21/21 (100.00%) |
| hard/test2 · automatic | on | 0 | 0/0 (undefined) | 0/98 (0.00%) | 21/21 (100.00%) | 21/21 (100.00%) |

**Trade-off:** on these hard splits, the strict gate disables every automatic boost, including true urgent interpretations; all 14 remaining boosts are signal-assisted distress. Zero observed false boosts is therefore a conservative fallback, **not evidence of accurate automatic alerting**. The 21/21 automatic urgent missed-boost rate is 100% on both splits. Fully covered wrong patterns can still boost: the demo pilot→tow-long error retains its 0.1 advisory boost.

Exact commands, in order:
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --split test2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --refresh-demos
```

Artifacts: [boost_gate_dev.json](../../edgeml/reports/nightlights/boost_gate_dev.json), [boost_gate_test2.json](../../edgeml/reports/nightlights/boost_gate_test2.json). These contain summary metrics and explicit denominators; contact-level before/after dumps are under gitignored `data/nightlights/reports/`. Demo refresh changes interpretation metadata only, not images/detections/truth.

## Existing 48-frame library (measured / visually reviewed)

| Result | Count |
|---|---:|
| Frames estimated night after mode-aware fix | 0/48 |
| SAR night not applicable | 8 |
| Navigation-light candidates | 0 |
| Resolution | 960×600; catalog boxes scaled 2× from 480×300 |

Earlier UAV flags `uav-3-{01,02,06,08}.jpg` were visually inspected: daylight aerial imagery with blue water, wakes and deck/container detail, **not night**. Earlier SAR brightness-based night labels were invalid; SAR is not applicable. No real-night detection accuracy can be inferred from this library.

Exact command (all 48 ordinary library frames; latest stage-1 v2 export):
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --library
```

## Demo truth → detected (measured detector outputs, unchanged by gate)

| Frame | Truth → detected, by contact | Current boosts | Low-confidence contacts |
|---|---|---|---|
| night-01.jpg | head_on → nuc; port → fishing; starboard → two_mastheads | 0, 0, 0 | 1, 2, 3 |
| night-02.jpg | ram → none; nuc → nuc; constrained_draught → constrained_draught | 0, 0.9, 0.7 | none |
| night-03.jpg | trawling → trawling; fishing → fishing; pilot → tow_long | 0, 0, 0.1 | 1, 2 |
| night-04.jpg | two_mastheads → two_mastheads; tow_long → tow_long | 0, 0.1 | none |
| night-05.jpg | distress_sos → distress_sos | 1 | none |
| night-06.jpg | distress_sos → distress_sos | 1 | none |
| night-07.jpg | distress_sos → distress_sos | 1 | none |
| night-08.jpg | distress_sos → distress_sos | 1 | none |

Frames 05–08 are signal-assisted. The five-light false NUC in night-01 contact 1 remains visible but now has **boost 0**, low confidence, and no light-driven priority marker. Truth is never relabeled to match detections.

Exact renderer/redetection command used for v2 demo observations:
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_demo.py
```
Current interpretation-only refresh command is listed with the gate evaluation above.

## Integration checks and measured simulator benchmark

Commands:
```sh
go build ./... && go vet ./... && go test ./...
go run ./cmd/bench
/home/ubuntu/.nvm/versions/node/v24.22.2/bin/node --test web/*.test.cjs
/home/ubuntu/nightlights-venv/bin/python -m pytest edgeml/tests -q
go run ./cmd/ghost-seeker -addr :8090 -auto-cdr 0
```

Build/vet/all Go tests pass; parity covers all 15 demo contacts including rule ID, gated boost and low-confidence state. Node: 27 tests pass; Python: 90 tests pass. Ordinary-library regression floors pass.

Bench seeds 101,102,103 × 6000 frames; results unchanged from origin/main (3699324), using ordinary Library only. **Bench oracle ≠ field truth**.

| Model | Edge drop | Edge miss P1 | Edge miss P1–2 | Tier exact | ±1 tier | P1 recall | P1 precision | Critical miss | False flash | Flash load | NDCG@20 | Review burden |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| maritime | 6.5% | 0.1% | 0.0% | 77.8% | 98.2% | 89.7% | 75.4% | 0.0% | 1.1% | 26.1% | 0.979 | 52.2% |
| air | 6.5% | 0.1% | 0.1% | 91.2% | 98.8% | 91.5% | 92.6% | 0.0% | 0.5% | 14.4% | 0.979 | 21.4% |
| sar | 6.5% | 0.0% | 0.0% | 79.8% | 96.2% | 99.3% | 73.3% | 0.0% | 0.2% | 6.2% | 0.977 | 5.7% |

Screenshots are local evidence under `~/shots/night-{feed,detail}-{1280,360}.png` and `~/shots/night-review-1280.png`, not repository assets.

## Measured versus estimated / limitations

| Measured | Not measured / heuristic |
|---|---|
| Synthetic spatial/count metrics, gate consistency/boost rates, API parity, simulator bench, ordinary library classifications and UI checks | Real-night accuracy, calibrated confidence, range limits, collision risk, legal right of way, false-alert rate at sea, real sensor/night prevalence |
| Generated scales/blur/occlusion and conditional aspects | No physical range/resolution or photometric calibration; priority boosts and simulator's ~20% night selection are design choices, not field estimates |

- Synthetic-only night labels and procedural imagery are **not operational accuracy**; the easy tier is intentionally uninformative about field difficulty.
- Real glare, shore lights, reflections, sea state, motion and changing waterline/horizon can defeat current rejection heuristics.
- Range/resolution may merge lights or make them subpixel. Superstructure can occlude required lights; an incomplete true pattern will miss a boost.
- Camera white balance, exposure, saturation and colour shift can change the observed signature. The whole-contact gate does not prove a fully covered pattern is the correct vessel state.
- No automatic flare detection, SOS/Morse recognition, frequency estimation or reliable moving-track registration. Signal-assisted distress depends on an external report that must itself be verified.
- Rule signatures cover selected COLREGS configurations, not the entire legal standard. White stern/anchor/tow/power-driven ambiguities and local-law blue lights remain explicit.
- Hard-dev informed design; held-out splits reduce tuning leakage but cannot resolve the synthetic-to-real gap. The gate's zero automatic boosts on hard splits trades false alerts for missed urgent vessels.
- All interpretations require visual/operator verification. No automatic manoeuvring or operational safety claim is supported.

## Burst frame rate and dark contacts (ported from dynamic-interest)

Night-lights analysis can request a temporary higher capture rate for a possible
flashing yellow light, an unresolved flashing rate, an unresolved light stack,
or a low-confidence pattern. A per-source Python `BurstController` grants or
denies requests against a sliding-window duty cap, extends a current interval
when allowed, and reports source-rate degradation. Dark-contact analysis is
limited to night-gated optical frames and vessel-like labels. It searches for
navigation lights in a clipped box expanded above and around the hull box, so
masthead lights above the hull count; a contact is flagged only when no lights
are detected in any frame.

Shared settings live in `internal/catalog/colregs_night_config.json` and are
loaded by Python and embedded by Go:

| Setting | Default |
|---|---:|
| Base / burst rate | 1 / 8 fps |
| Burst duration | 3 s |
| Maximum burst duty / window | 0.2 / 60 s |
| Dark-contact search: side / above / below | 0.15× width / 1.5× max(height, 4 px) / 0.1× height |
| Dark-contact label / priority weight | `dark contact` / 1.0 |

The sidecar keeps one controller per source and appends a separate
`dark contact` detection using the hull's normalized box. `AnalyzeResponse`
field 7 (`NightBurst`) carries granted requests. The edge invokes
`Ingester.OnBurst`; replay connects it to `Replay.Burst`, which temporarily
shortens the replay period and returns to the base period at the deadline.
There is no new capture subsystem. The `dark` scorer uses the highest
above-threshold dark-contact confidence and has a policy weight override
(`ScorerWeights["dark"]`, including zero). Interest boost/suppress matching
normalizes “dark vessel(s),” “dark ship,” “running dark,” “vessel(s) running
dark,” and “dark contacts” to `dark contact`.

The deterministic, in-memory evaluation uses 28 examples per dark-contact
scenario, with separate seeds for each split/tier. Easy samples use the
existing `render`; hard samples use `hard_scene`/`hard_contact` without harbor
lights, retaining hard optical noise and rendering variability. The hard
positive is an unlit hull; lit-vessel negatives use random rules from
`load_rules()`. These are synthetic counts, not field-performance estimates.

| Tier / split | Dark hull + vessel | Lit vessel + box | Day + box | Dark hull + buoy | TP / FP / FN | Precision / recall |
|---|---:|---:|---:|---:|---:|---:|
| easy / dev | 28/28 | 0/28 | 0/28 | 0/28 | 28 / 0 / 0 | 100% / 100% |
| easy / test2 | 28/28 | 0/28 | 0/28 | 0/28 | 28 / 0 / 0 | 100% / 100% |
| hard / dev | 28/28 | 12/28 | 0/28 | 0/28 | 28 / 12 / 0 | 70% / 100% |
| hard / test2 | 28/28 | 12/28 | 0/28 | 0/28 | 28 / 12 / 0 | 70% / 100% |

| Tier / split | Flashing yellow, 4 frames | Single-frame yellow | Complete steady pattern | Low-confidence pattern | Day | Continuous 120 s: granted / denied / duty |
|---|---:|---:|---:|---:|---:|---:|
| easy / dev | 1/1 | 1/1 | 0/1 | 1/1 | 0/1 | 2 / 100 / 20% |
| easy / test2 | 1/1 | 1/1 | 0/1 | 1/1 | 0/1 | 2 / 100 / 20% |
| hard / dev | 1/1 | 1/1 | 0/1 | 1/1 | 0/1 | 2 / 100 / 20% |
| hard / test2 | 1/1 | 1/1 | 0/1 | 1/1 | 0/1 | 2 / 100 / 20% |

The JSON reports retain per-scenario flagged/request counts and seeds:
`edgeml/reports/nightlights/dark_burst_{easy,hard}_{dev,test2}.json`.
Reproduce with:

```sh
cd edgeml
~/gs-venv/bin/python scripts/nightlights_dark_eval.py --split dev --tier easy
~/gs-venv/bin/python scripts/nightlights_dark_eval.py --split test2 --tier easy
~/gs-venv/bin/python scripts/nightlights_dark_eval.py --split dev --tier hard
~/gs-venv/bin/python scripts/nightlights_dark_eval.py --split test2 --tier hard
```

Limitations: evaluation is synthetic only. IR hot-blob dark contacts from
`dynamic-interest` are not ported because ghost-seeker skips IR light rules.
Replay burst only shortens the replay period; it does not add camera controls.
The sliding duty cap is enforced by the per-source Python controller at the
edge, not by Go replay.
