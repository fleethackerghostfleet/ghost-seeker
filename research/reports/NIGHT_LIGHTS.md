# Night navigation lights: method and measured results

**Synthetic demonstration, not operational accuracy.** Automatic light detections remain unreliable in clutter. Partial patterns are displayed but cannot raise priority. All accuracy numbers below are measured on the named procedural datasets, not estimates of performance at sea.

## Strict-gate detector v5: independent unreflected lights

**Synthetic-procedural results, not at-sea accuracy.** v5 was tuned on hard/dev
and easy/dev only, frozen at **`3bfa51bde7b0eb8602cd31b29fe59ff1d42d9719`** with
threshold **0.85**, and recorded in PROTOCOL.md in commit **`24ae037`** before
hard/test4 was generated. Test4 uses base seed **4200000** (112 night contacts,
8 day and 16 twilight records). It was evaluated **exactly once**; both boost
and dark-contact summaries consumed that same saved observation dump. Easy/test
was subsequently checked as a regression set. No detector/threshold changes
followed either result.

The v4 association expression selected reflected lights *instead of* independently
qualified unreflected lights whenever any reflected candidate existed. v5 takes
their union: compact sharp points retain their own evidence, and colour-pure,
high-contrast same-colour vertical pairs can qualify without reflections. A
compact, transversely centered half-peak footprint rejects broad glare corners;
sodium ambiguity, pier rows and dense horizontal clutter remain rejection cues.
Confidence uses each light's own PSF/reflection evidence. Complete colour-multiset
equality, zero white allowance and assisted-distress exemption are unchanged.

Urgent columns show **true/false automatic boosts**. False boosts are all/automatic.

| Dataset | Light P | Light R | Rules /112 | Automatic /98 | RAM | NUC | Draught | False boosts | Night gate | Day false-night |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| easy/dev v5 | 100% | 100% | 112 | 98 | 7/0 | 7/0 | 7/0 | 0/0 | 120/120 (100%) | 0% |
| easy/test v5 | 100% | 100% | 112 | 98 | 7/0 | 7/0 | 7/0 | 0/0 | 120/120 (100%) | 0% |
| hard/dev v5 | 72.41% | 51.53% | 39 | 25 | 2/0 | 2/0 | 1/0 | 0/0 | 132/136 (97.06%) | 0% |
| hard/test4 v5 | 72.90% | 50.32% | 34 | 20 | 2/0 | 0/0 | 0/0 | 0/0 | 133/136 (97.79%) | 0% |

**Precision trade-off:** hard/dev changes from v4's 82 correct / 107 predicted
lights (P 76.64%, R 50.31%) to 84 / 116 (P 72.41%, R 51.53%). Rules improve
38→39, automatic rules 24→25 and correct urgent boosts 4→5, recovering NUC
300201 at confidence 0.9321875. Additional shore/pier candidates still survive.
The six would-be false boosts before confidence suppression retain maximum
confidence **0.791667**, leaving dev margin **0.058333** to 0.85. Easy/dev has no
would-be false boost, so its maximum/margin are not applicable.

Saved-observation dark-contact false-dark counts (only observable lit night
contacts, excluding fully occluded emitters and background records):

| Dataset | False dark / eligible lit contacts | Night-gate misses among eligible |
|---|---:|---:|
| easy/dev v5 | 0/112 | 0 |
| easy/test v5 | 0/112 | 0 |
| hard/dev v5 | 0/97 | 0 |
| hard/test4 v5 | 0/94 | 0 |

These sets have **no labeled unlit-vessel ground truth**, so dark-contact recall
cannot be measured here. The separate dev dark-contact scenario evaluation is
unchanged: easy/dev TP/FP/FN **28/0/0**, hard/dev **28/9/0**. That is a different
dataset/denominator, not a contradiction of the saved-observation table.

Only **2/21** automatic urgent test4 contacts receive correct boosts, both RAM;
NUC and draught receive none. Zero observed false boosts is not a general safety
guarantee. Occluded CD can still resemble NUC, heuristic confidence is not a
calibrated probability, static-frame persistence remains weak evidence, and the
generator's vessel-only reflection cue remains optimistically discriminative.
No test4 retuning or post-result detector changes were made.

### v5 commands and demo refresh

```sh
PY=~/nightlights-venv/bin/python
# After committing the freeze record; detector evaluation below was run once.
$PY edgeml/scripts/nightlights_synth.py --tier hard --split test4
$PY edgeml/scripts/nightlights_eval.py --tier hard --split test4 --version v5
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split test4 --version v5
$PY edgeml/scripts/nightlights_dark_eval.py --observations data/nightlights/reports/hard_test4_v5.json --output edgeml/reports/nightlights/dark_contacts_hard_test4_v5.json
$PY edgeml/scripts/nightlights_eval.py --tier easy --split test --version v5
$PY edgeml/scripts/nightlights_boost_eval.py --tier easy --split test --version v5
$PY edgeml/scripts/nightlights_dark_eval.py --observations data/nightlights/reports/easy_test_v5.json --output edgeml/reports/nightlights/dark_contacts_easy_test_v5.json
$PY edgeml/scripts/nightlights_demo.py
$PY -m pytest edgeml/tests -q
PATH="$HOME/sdk/go/bin:$PATH" go test ./...
```

Dev dark summaries likewise replay the saved `easy_dev_v5.json` and
`hard_dev_v5.json` observations without redetection. Large per-case dumps remain
gitignored; committed evaluation artifacts are summaries only.

The demo catalog was refreshed because v5 changes detections: night-01 port
gains an extra light, night-01 starboard changes to two_mastheads, and night-03
pilot changes to two_mastheads. These errors are retained, not relabeled.
Night-02 NUC **+0.90** and draught **+0.70** remain unchanged, as do assisted
distress boosts. All eight JPEGs are byte-identical. The v4 screenshots below
are retained as historical evidence, not newly captured v5 screenshots.

## Strict-gate detector v4 (before/after, historical)

**Synthetic-procedural measurements only, not at-sea accuracy.** Detector and
threshold were frozen in `a3e3e9005fa3241368e9cddf38db0ce3cdb80a9d`.
Only hard/dev and easy/dev informed v3/v4 tuning; easy/test was a regression
confirmation, not a diagnosis set. Hard/test and hard/test2 were not accessed
during tuning. Hard/test3 (base **4100000**, 112 night + 8 day + 16 twilight)
was generated after the freeze and evaluated **exactly once**, followed by boost
evaluation. Hard/test and hard/test2 were then evaluated for comparison only.
No tuning of normal navigation-light detection or threshold followed these
evaluations; the API safety and dark-contact follow-up is described below. Older sections
below retain explicitly historical v1/v2 results rather than describe v4.

The frozen CPU detector combines fine/broad Gaussian local peaks and compact
PSF centroids, saturated-core/chromatic-halo colour classification (including
nonmatching `ambiguous_orange`), vertical grouping, local contrast, and
colour-compatible water-reflection support below the hull. A resolved aligned
pair can receive high confidence from pure colour and contrast without requiring
three emitters or a bright reflection. No truth/distractor labels are used at
inference. **Caveat:** the generator draws vessel-coloured reflection streaks
but no equivalent shore-emitter streaks; this physically motivated cue is
optimistically discriminative on this dataset and needs real-sensor validation.

Strict complete-colour-multiset equality and **zero white allowance** remain
mandatory. Pattern confidence is the minimum per-light `conf`; the shared
`lights.pattern_conf_threshold` is **0.85**. The maximum hard/dev would-be false
boost confidence is 0.791667, giving **0.058333 dev safety margin**. Confidence is
heuristic, not a calibrated probability. Signal-assisted distress remains exempt.

Go reads the same catalog threshold, carries `conf` through observations and
rechecks both conditions in scoring/rescoring without changing rule-ID logic.
Missing/invalid confidence now conservatively maps to zero in **both Python and
Go**, with signal-assisted distress still exempt. The post-freeze API safety
follow-up changed Python's former missing-field default of 1.0, not the frozen
detector-produced observations. Shared missing/null/out-of-range/threshold and
assisted-distress cases live in `internal/catalog/light_confidence_cases.json`
and are read by both test suites; the demo catalog also checks agreement.

The follow-up replayed boost evaluation on **saved** hard/dev and hard/test3 v4
observations: every boost metric remained identical. There was **no test3 image
redetection or detector evaluation**. A fresh hard/dev detector evaluation
produced a byte-identical summary JSON (SHA256
`0613cba8f9d4a83fbf87524e30a2762719657be1baa1896974ae2d2348f1714d`);
all 136 contacts' light arrays and interpretations were also byte-identical
under canonical JSON serialization. Only dark-contact results changed.

Urgent columns are **true/false automatic boosts** (seven truth contacts per
urgent rule); false-any is **all/automatic**. Rules have denominators 112/98.
Historical dev/test2 boost columns refer to the archived strict-gate replay,
not the original permissive interpreter. `n/r` means not recorded in the
hard/test v1 archive; it was not reconstructed with a different detector.

| Dataset / version | Light P | Light R | Rules /112 | Automatic /98 | RAM T/F | NUC T/F | CD T/F | False-any all/auto | Night gate | Day false-night |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| easy_dev v4 (unchanged detection accuracy) | 100% | 100% | 112 | 98 | 7/0 | 7/0 | 7/0 | 0/0 | 100% | 0% |
| easy_test v4 (unchanged detection accuracy) | 100% | 100% | 112 | 98 | 7/0 | 7/0 | 7/0 | 0/0 | 100% | 0% |
| hard_dev v2 | 19.76% | 40.49% | 25 | 11 | 0/0 | 0/0 | 0/0 | 0/0 | 97.06% | 0% |
| hard_dev v4 | 76.64% | 50.31% | 38 | 24 | 2/0 | 1/0 | 1/0 | 0/0 | 97.06% | 0% |
| hard_test v1 | 8.72% | 24.07% | 21 | 7 | n/r | n/r | n/r | n/r | 96.32% | 0% |
| hard_test v4 | 81.03% | 58.02% | 40 | 26 | 0/0 | 0/0 | 1/0 | 0/0 | 96.32% | 0% |
| hard_test2 v2 | 13.72% | 31.33% | 24 | 10 | 0/0 | 0/0 | 0/0 | 0/0 | 97.06% | 0% |
| hard_test2 v4 | 80.43% | 44.58% | 34 | 20 | 0/0 | 2/0 | 1/0 | 0/0 | 97.06% | 0% |
| hard_test3 v4 | 73.28% | 52.80% | 35 | 21 | 0/0 | 0/0 | 2/0 | 0/0 | 97.06% | 0% |

The new boost evaluator also includes negatives: 136 all / 122 automatic hard
records and 120 / 106 easy records, versus the older night-only 112 / 98.
Each split has 14 externally assisted distress boosts; these are not automatic
urgent recognition. **No test3 false-boost contacts occurred**, but only 2/21
automatic urgent contacts boosted. Remaining automatic urgent misses are 17/21
on dev, 20/21 on test, 18/21 on test2 and 19/21 on test3. Misses include permanent
occlusion, missing/merged points, colour confusion, unexplained extras and
confidence abstention. Hard/dev RAM 300000 and CD 300103 are correctly detected
but suppressed at 0.7958 and 0.8230 respectively.

Two dev terminal-occluded CD contacts (300101, 300106) look like NUC; confidence
0.7889/0.7917 suppresses them, not any justified missing-middle spacing test.
A terminal-occluded CD and genuine NUC can be indistinguishable. Zero observed
false boosts does **not** establish general protection against this ambiguity.
Four-frame persistence requires >=2/4 detections, retaining 2/4 flashing lights.
Its measured v4 hard/dev effect is **107 -> 107 lights, 18 -> 18 boosts, zero
false boosts before/after**: a no-op on static four-frame scenes, not a demonstrated
clutter remedy. Phase-2 v3 likewise measured 101 -> 101 and 17 -> 17 boosts.

### Exact v4 evaluation commands

These document the completed experiment; **do not repeat the locked test3
evaluation as another independent trial**. From the repository root with the
frozen source/config:

```sh
export PATH="$HOME/sdk/go/bin:$PATH"
PY="$HOME/nightlights-venv/bin/python"
# Pre-freeze dev tuning and easy regression evaluation:
$PY edgeml/scripts/nightlights_eval.py --tier easy --split dev --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier easy --split dev --version v4
$PY edgeml/scripts/nightlights_eval.py --tier easy --split test --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier easy --split test --version v4
$PY edgeml/scripts/nightlights_eval.py --tier hard --split dev --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split dev --version v4
# After freeze commit a3e3e900, in this order; each eval ran once:
$PY edgeml/scripts/nightlights_synth.py --tier hard --split test3
$PY edgeml/scripts/nightlights_eval.py --tier hard --split test3 --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split test3 --version v4
$PY edgeml/scripts/nightlights_synth.py --tier hard --split test2
$PY edgeml/scripts/nightlights_eval.py --tier hard --split test2 --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split test2 --version v4
$PY edgeml/scripts/nightlights_synth.py --tier hard --split test
$PY edgeml/scripts/nightlights_eval.py --tier hard --split test --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split test --version v4
# Phase 4: refresh observations from existing JPEGs (does not overwrite JPEGs):
$PY edgeml/scripts/nightlights_demo.py
# Follow-up API parity: saved observations only (no image reads):
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split dev --version v4
$PY edgeml/scripts/nightlights_boost_eval.py --tier hard --split test3 --version v4
# Follow-up default detector invariance check, dev only:
$PY edgeml/scripts/nightlights_eval.py --tier hard --split dev --version v4
```

Summary artifacts are `edgeml/reports/nightlights/{easy,hard}_*_v4.{json,md}`
and `boost_gate_{easy,hard}_*_v4.json`; full per-case dumps remain gitignored.

## Method

| Component | Implementation / behavior |
|---|---|
| Day/night and mode gate | `edgeml/src/edgeml/nightlights/core.py`: mean luminance and dark fraction; confident image statistics win. Ambiguous images use NOAA approximate solar elevation below −6° when timezone-aware timestamp and lat/lon are available, otherwise report image ambiguity. SAR/radar night is not applicable; IR may estimate night but has no colour rules. |
| Detector | Frozen v4 CPU OpenCV: fine/broad Gaussian peaks plus compact PSF centroids, halo colour, contrast, aligned columns and reflection support; confidence abstention. No reliable horizon/hull segmentation; see v4 section above. |
| Flashing | Presence/absence over four registered frames. This can confuse occlusion or detector misses with flashing; no automatic flare or Morse recognition. |
| Interpreter | Ordered vertical groups; set/isolated sidelight patterns; flashing constraints; first matching rule in catalog order. Red port side implies conditional right-to-left crossing; green starboard side implies left-to-right. Offset whites assume higher aft/lower forward mastheads, not tow lights; two whites remain ambiguous. |
| Whole-contact boost gate | Complete colour-multiset equality, **zero white allowance**, and min light confidence >=0.85. Extra/uncertain lights retain the rule ID/line but cannot boost. External signal-assisted distress is exempt. This is not calibrated probability. |
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
| hard/test3 | Base 4100000; same counts | Generated after v4 freeze a3e3e900; evaluated once. Historical test/test2 also evaluated with v4 after freeze, never for v4 tuning. |
| App demos | Eight 960×600 JPEGs, each <150 KB; 15 contacts | Moderate synthetic harbor/offshore scenes; observations separate from truth. Frames 05–08 are an externally identified SOS on/off proxy, not decoded Morse. |

Night-contact seeds add rule_index×100 + repetition (seven repetitions/rule); day/twilight offsets are 9000/9500. Generated datasets/crops/caches live under gitignored `data/nightlights/`. Hard scenes include tiny one-pixel to 1–3 px lights, defocus/motion blur, bloom/merged points, warm whites, orange reds, cyan greens, white saturated cores, reflections, sodium/white shore and pier rows, moon glints, partial occlusion, noise, JPEG compression and twilight.

Hard summary day-image counts were corrected from 16 to 8 using cached labels only: the 16 twilight images (eight labelled daytime) are counted separately, not again as dedicated day images. Totals remain 112 night contacts + 8 day + 16 twilight = 136 gate cases. Detection, interpretation, confusion and accuracy measurements are unchanged; no detector or Gemini rerun.

Unresolvable direct-light patterns are labeled, never removed: permanent occlusion or one-pixel saturation that destroys direct colour. Radius zero renders one pixel, not a subpixel source. Reflections/context/external distress can still give cues. Occluded emitters are excluded from observable-light recall, but full latent rule accuracy retains all contacts. Low SNR alone is not used to discard cases.

Full-frame light matching requires position within 6 px and correct colour. Wrong colour penalizes precision/recall; unmatched predictions populate the background row. Flashing accuracy counts missed observable lights as errors. Aspect includes unknown labels and is not heading accuracy. Automatic rule accuracy excludes 14 externally signaled distress cases.

## Historical v1/v2 full-frame / four-frame results

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

From repository root; the Python executable was `/home/ubuntu/nightlights-venv/bin/python`. These are the commands that produced the archived measurements, not new runs against current code. v1 archives are copies of their original unversioned report outputs. Re-running historical commands on current code does not reconstruct the old detector; the split and tuning protocol is recorded above and in [PROTOCOL](../edgeml/reports/nightlights/PROTOCOL.md).

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

## Historical Gemini comparison: identical contact crops (pre-v4)

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

336 calls total, no retries. Ten truncated MAX_TOKENS JSON responses count as empty detections/incorrect rules, not excluded cases. Cached under `data/nightlights/gemini/`; no new Gemini calls in this boost-gate round. Crop rule strata (ours/Gemini): dev resolvable 6/76 vs 18/76, unresolvable 3/36 vs 2/36; test2 resolvable 8/78 vs 18/78, unresolvable 0/34 vs 3/34. Compact per-colour/per-rule summary: [gemini_comparison.json](../edgeml/reports/nightlights/gemini_comparison.json).

Exact command (datasets hard/dev, hard/test2, easy/test):
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_gemini.py --workers 4 --max-calls 400
```

## Measured boost-gate results (paired frozen v2 observations)

Historical gate design: hard-dev only; exact multiset coverage, no optional white allowance, no numeric threshold at that stage. Detector and track analysis then remained v2; only `interpret` changed in that stage. Test2 observations were replayed once after gate design, without test2-driven adjustments. Current v4 adds detector changes and the confidence gate described above.

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

**Historical v2 trade-off:** this replay disabled every automatic boost; all 14 remaining boosts were signal-assisted distress. The v4 table above supersedes that sensitivity result. Fully covered wrong patterns can still boost in principle; the historical demo pilot→tow-long error is no longer present in refreshed v4 observations.

Exact commands, in order:
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --split test2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --refresh-demos
```

Artifacts: [boost_gate_dev.json](../edgeml/reports/nightlights/boost_gate_dev.json), [boost_gate_test2.json](../edgeml/reports/nightlights/boost_gate_test2.json). These contain summary metrics and explicit denominators; contact-level before/after dumps are under gitignored `data/nightlights/reports/`. Demo refresh changes interpretation metadata only, not images/detections/truth.

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

## Demo truth → detected (frozen v4 redetection of unchanged JPEGs)

| Frame | Truth → detected, by contact | Current boosts | Low-confidence contacts |
|---|---|---|---|
| night-01.jpg | head_on → head_on; port → port; starboard → starboard | 0, 0, 0 | 1, 2, 3 |
| night-02.jpg | ram → none; nuc → nuc; constrained_draught → constrained_draught | 0, 0.9, 0.7 | none |
| night-03.jpg | trawling → starboard; fishing → fishing; pilot → pilot | 0, 0.1, 0 | 1 |
| night-04.jpg | two_mastheads → two_mastheads; tow_long → tow_long | 0, 0.1 | none |
| night-05.jpg | distress_sos → distress_sos | 1 | none |
| night-06.jpg | distress_sos → distress_sos | 1 | none |
| night-07.jpg | distress_sos → distress_sos | 1 | none |
| night-08.jpg | distress_sos → distress_sos | 1 | none |

Frames 05–08 are signal-assisted, exempt even though their light confidence is below 0.85. Night-02 NUC/CD pattern confidence is 0.9500/0.89375 and both automatically boost correctly; RAM remains unrecognized. Truth is never relabeled to match detections. JPEG bytes are unchanged. The demo script now preserves existing images and refreshes observations, including confidence, from decoded JPEGs.

Exact observation refresh command (renders only missing JPEGs):
```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_demo.py
```
The older interpretation-only refresh command above describes the historical gate replay, not this detector refresh.

## Integration checks and measured simulator benchmark

Commands:
```sh
go build ./... && go vet ./... && go test ./...
go run ./cmd/bench -n 6000 -seeds 101,102,103
/home/ubuntu/.nvm/versions/node/v24.22.2/bin/node --test web/*.test.cjs
/home/ubuntu/nightlights-venv/bin/python -m pytest edgeml/tests -q
go run ./cmd/ghost-seeker -addr :8090 -auto-cdr 0
```

Phase-4 build/vet/all Go tests passed; parity covers all 15 refreshed demo contacts including rule ID, gated boost and low-confidence state. Python also reads the same catalog as an agreement fixture. Node: 49 tests passed. The follow-up passed `go test ./internal/...` and Python: 195 tests passed, 3 skipped. Ordinary-library regression floors pass.

Bench seeds 101,102,103 × 6000 frames; current Phase-4 run below. **Bench oracle ≠ field truth**. This benchmark does not measure real-night detector performance.

| Model | Edge drop | Edge miss P1 | Edge miss P1–2 | Tier exact | ±1 tier | P1 recall | P1 precision | Critical miss | False flash | Flash load | NDCG@20 | Review burden |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| maritime | 6.0% | 0.0% | 0.0% | 79.4% | 98.3% | 91.5% | 78.7% | 0.0% | 1.1% | 28.9% | 0.990 | 54.6% |
| air | 6.0% | 0.1% | 0.1% | 91.9% | 98.9% | 93.5% | 94.4% | 0.0% | 0.5% | 17.8% | 0.988 | 25.4% |
| sar | 6.0% | 0.0% | 0.0% | 81.5% | 96.5% | 99.6% | 84.3% | 0.0% | 0.3% | 10.0% | 0.989 | 9.4% |

Phase-4 screenshots (Playwright/Chrome, headless, 1440×900) are [night-v4-detail.png](img/night-v4-detail.png) and [night-v4-review.png](img/night-v4-review.png). Start with `go run ./cmd/ghost-seeker -addr :8090 -auto-cdr 0 -backfill 600`, pause the feed, select an actual `night-02.jpg` item and open its detail/Operator Review. Both show the correct urgent NUC/CD signals and priority boosts, with synthetic-data banners. Historical screenshots remain local under `~/shots/`.

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
- Hard-dev informed design; held-out splits reduce tuning leakage but cannot resolve the synthetic-to-real gap. The v4 gate's few correct automatic urgent boosts still trade false alerts for many missed urgent vessels.
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
are present as permissive bright-point candidates in any frame. This presence
check uses the candidate stage, not the precision-oriented colour/association
output used for navigation-light interpretation. It retains compact local
maxima with peak >=100 and local Gaussian-background contrast >=65
(v2-equivalent bright-point criteria), before chromatic classification,
waterline and vessel-association rejection. Counts are **candidates**, not
confirmed navigation lights. Normal `detect_lights` output and boosts remain
unchanged.

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
`Ingester.OnBurst`; replay passes the source to `Replay.Burst`, which keeps a
separate period/deadline per source and shortens the wait only when the next
queued capture belongs to a source with an active burst.
In sightings mode, a `DUPLICATE` with a key-detection box can attach an
overlapping (IoU ≥ 0.5) dark-contact detection to the existing item, tag it,
and rescore it without replacing its forms or key frame. There is no new
capture subsystem. The `dark` scorer uses the highest
above-threshold dark-contact confidence and has a policy weight override
(`ScorerWeights["dark"]`, including zero). Interest boost/suppress matching
normalizes “dark vessel(s),” “dark ship,” “running dark,” “vessel(s) running
dark,” and “dark contacts” to `dark contact`.

The deterministic, in-memory evaluation uses 28 examples per dark-contact and
burst scenario, with separate split/tier seeds and varied per-sample seeds,
positions, and steady-pattern rules from `load_rules()`. Easy samples use the
existing `render`; hard night samples use `hard_scene`/`hard_contact` without
harbor lights, retaining hard optical noise and rendering variability (day
samples use `render` in daylight). The hard positive is an unlit hull;
lit-vessel negatives use random rules from `load_rules()`. These are synthetic
counts, not field-performance estimates.

**Dark-contact follow-up fix:** Phase-4 v4 exposed a mismatch between precision-
tuned classification and the recall-oriented question “are any lights present?”
The candidate-only path restores false-dark counts below the main baseline on
both hard sets, with 28/28 unlit-hull recall on all four sets. An initial trial
admitted faint low-contrast glow on one unlit hard/test2 hull; retaining the
v2-equivalent peak/background contrast criteria rejects that glow without
vessel/colour-confidence filtering. No normal navigation-light detections or
boost metrics changed. Background points can still hide genuinely dark hulls;
these procedural counts are not real-world recall validation. **Burst behavior
is not fixed**: more requests on complete steady patterns and fewer on yellow
lights remain, with unchanged Phase-4 counts below.

| Tier / split | Main false dark | Phase-4 v4 false dark | Now false dark | Unlit TP main / v4 / now | FN main / v4 / now |
|---|---:|---:|---:|---:|---:|
| easy / dev | 0 | 1 | 0 | 28 / 28 / 28 | 0 / 0 / 0 |
| easy / test2 | 0 | 0 | 0 | 28 / 28 / 28 | 0 / 0 / 0 |
| hard / dev | 12 | 21 | 9 | 28 / 28 / 28 | 0 / 0 / 0 |
| hard / test2 | 12 | 17 | 9 | 28 / 28 / 28 | 0 / 0 / 0 |

| Tier / split | Dark hull + vessel | Lit vessel + box | Day + box | Dark hull + buoy | TP / FP / FN | Precision / recall |
|---|---:|---:|---:|---:|---:|---:|
| easy / dev | 28/28 | 0/28 | 0/28 | 0/28 | 28 / 0 / 0 | 100% / 100% |
| easy / test2 | 28/28 | 0/28 | 0/28 | 0/28 | 28 / 0 / 0 | 100% / 100% |
| hard / dev | 28/28 | 9/28 | 0/28 | 0/28 | 28 / 9 / 0 | 75.68% / 100% |
| hard / test2 | 28/28 | 9/28 | 0/28 | 0/28 | 28 / 9 / 0 | 75.68% / 100% |

| Tier / split | Flashing yellow, 4 frames | Single-frame yellow | Complete steady pattern | Low-confidence pattern | Day | Continuous 120 s: accepted (new intervals + extensions) / denied / duty |
|---|---:|---:|---:|---:|---:|---:|
| easy / dev | 20/28 | 25/28 | 18/28 | 25/28 | 0/28 | 20 (2 new + 18 extensions) / 100 / 20% |
| easy / test2 | 19/28 | 25/28 | 16/28 | 27/28 | 0/28 | 20 (2 new + 18 extensions) / 100 / 20% |
| hard / dev | 0/28 | 9/28 | 13/28 | 17/28 | 0/28 | 20 (2 new + 18 extensions) / 100 / 20% |
| hard / test2 | 3/28 | 8/28 | 15/28 | 20/28 | 0/28 | 20 (2 new + 18 extensions) / 100 / 20% |

Continuous-sequence `accepted` counts calls returning true; `new_intervals`
counts newly granted intervals, and `extensions` counts successful extensions.
For this sequence, accepted equals new intervals plus extensions.

The JSON reports retain per-scenario flagged/request counts and seeds:
`edgeml/reports/nightlights/dark_burst_{easy,hard}_{dev,test2}.json`.
Reproduce with:

```sh
~/nightlights-venv/bin/python edgeml/scripts/nightlights_dark_eval.py --split dev --tier easy
~/nightlights-venv/bin/python edgeml/scripts/nightlights_dark_eval.py --split test2 --tier easy
~/nightlights-venv/bin/python edgeml/scripts/nightlights_dark_eval.py --split dev --tier hard
~/nightlights-venv/bin/python edgeml/scripts/nightlights_dark_eval.py --split test2 --tier hard
```

Limitations: evaluation is synthetic only. IR hot-blob dark contacts from
`dynamic-interest` are not ported because ghost-seeker skips IR light rules.
Replay burst only shortens the replay period; it does not add camera controls.
The sliding duty cap is enforced by the per-source Python controller at the
edge, not by Go replay.
