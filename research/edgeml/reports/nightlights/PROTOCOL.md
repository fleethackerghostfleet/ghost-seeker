# Night-light candidate: protocol and limitations

Current consolidated protocol and measurements: [NIGHT_LIGHTS.md](../../../reports/NIGHT_LIGHTS.md).
Committed JSON contains summary metrics only. Full contact/light observations are
written to gitignored `data/nightlights/reports/` by the evaluator, Gemini comparison
and boost evaluator. Regenerate observations with `nightlights_eval.py` before
running `nightlights_boost_eval.py`; historical v2 reproduction requires its frozen
detector revision, not the current interpreter. Legacy duplicate reports and
stage scratch notes were removed; the consolidated report retains their results.

The sections below record the historical stage-1 baseline, not the current
candidate. See NIGHT_LIGHTS.md above and easy_*/hard_* summaries for current
tiered paths, mode-aware gating, integration and demo metadata.

CPU only, Python 3.10, NumPy 2.2.6, OpenCV headless 5.0.0.93.
The system pip/setuptools could not install editable PEP 660 packages. Installation
was completed in `/home/ubuntu/nightlights-venv` after upgrading pip/setuptools;
no repository dependency/build files were changed.

## Strict-gate detector v4 (before/after)

**Synthetic-procedural only; not at-sea accuracy.** The frozen detector and
`lights.pattern_conf_threshold=0.85` are at commit
`a3e3e9005fa3241368e9cddf38db0ce3cdb80a9d`. Tuning used hard/dev and easy/dev
only; easy/test was a regression confirmation. Hard test/test2/test3 were not
generated or evaluated during v3/v4 tuning. After freeze, hard/test3 (seed base
4100000, 112 night + 8 day + 16 twilight, four registered frames per contact)
was generated and evaluated exactly once, then boost-evaluated. Historical
hard/test2 (2700000) and hard/test (900000) were evaluated for comparison only.
Summary commit: `68dc0b94`. Subsequent API/dark-contact fixes below do not change
normal detector-produced light observations or navigation boost metrics.

The method combines local Gaussian peaks/compact PSFs, chromatic halos around
saturated cores, contrast, aligned columns, resolved-pair confidence and
colour-compatible reflections below the hull. No truth is used at inference.
Vessel-only generated reflection streaks likely overstate the cue's real-world
discrimination. Strict full multiset equality, zero white allowance and min
light confidence >=0.85 are required for automatic boosts. The maximum dev
would-be false boost confidence is 0.791667 (safety margin 0.058333).
Signal-assisted distress is exempt. The two dev terminal-occluded CD->NUC
ambiguities remain suppressed by confidence, not a demonstrated spacing cue;
this does not guarantee safety for indistinguishable real patterns.
Persistence requires >=2/4, including flashing 2/4; measured dev effect is a
no-op. Many urgent contacts still miss (dev 17/21, test 20/21, test2 18/21,
test3 19/21); zero observed false boosts is not accurate automatic recognition.

The consolidated report's **Strict-gate detector v4 (before/after)** section
contains the full P/R, rules, automatic rules, urgent true/false boosts,
all/automatic false boosts, night/day-gate table, exact generation/evaluation
commands and limitations. All v4 summaries are committed; detailed observations
remain gitignored. Do not repeat test3 and count it as an independent trial.

Go reads the same threshold and carries `conf` through demo observations,
scoring and rescoring. The API follow-up makes missing/invalid confidence zero
in Python as well as Go, while preserving assisted distress. Both suites read
`internal/catalog/light_confidence_cases.json`, including missing confidence,
and the refreshed demo catalog. Saved hard/dev and test3 v4 boost metrics were
replayed and are identical; **no test3 image redetection/evaluation occurred**.
The hard/dev summary JSON is byte-identical after redetection; all 136 contacts'
lights and interpretations also match byte-for-byte under canonical JSON.

Dark-contact presence now uses permissive compact bright-point candidates
(peak >=100, local Gaussian-background contrast >=65), before colour,
waterline and vessel-association rejection; these are not confirmed navigation
lights. Default interpretation detections and threshold remain unchanged.
Main / Phase-4 v4 / now false dark: easy/dev 0/1/0, easy/test2 0/0/0,
hard/dev 12/21/9, hard/test2 12/17/9. All four retain TP=28, FN=0 (100% recall).
The four dark/burst evaluations were rerun; burst metrics remain unchanged from
Phase 4, including their regressions. Background candidates can hide genuinely
dark hulls; this is synthetic evidence, not real-world recall validation.

## Historical stage-1 frozen candidate before first test evaluation

Dev was generated and evaluated during implementation. The final candidate passed
36 unit tests and dev evaluation before generating or evaluating test. Test seeds
are disjoint from dev; the test split is evaluated once, with no subsequent tuning.
There are 112 night contacts (7 per rule) and 8 day images per split, 224 night
contacts total. Every night contact has four frames. Images and detailed source
labels live under gitignored `data/nightlights/{dev,test}`; seeds and generator
reproduce them. Dev seeds are 1000 + rule_index*100 + repetition; test seeds are
100000 + rule_index*100 + repetition. Day seeds add 9000 to the split base.

SHA256 of the frozen evaluation inputs:

```text
a7c3b44e1a03fdf29bfb73c92b30ba706b740c3707ca96e1e7a8cb23aa2b14cb  edgeml/src/edgeml/nightlights/__init__.py
096abb0f07d7b4a564a663b4c5eb83edaf542a148d9d3ad71357afdf22d02953  edgeml/src/edgeml/nightlights/core.py
4334cff8ab04cd40fce495b1d540ef42f40f2a83839594588068f603bcfd87bc  edgeml/scripts/nightlights_eval.py
09f1fb7975e7cb829fe3a9d2599e0a0ed53de299f9e4af38c430f881e0ca2806  edgeml/scripts/nightlights_synth.py
b0d4c376d0fa888ca615c2d2af7f4d3fddffc0b6033cdc397471391ef171e1d7  internal/catalog/colregs_lights.json
```

## Commands (from repository root)

```sh
/home/ubuntu/nightlights-venv/bin/pip install -e 'edgeml[test]'
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --library
/home/ubuntu/nightlights-venv/bin/python -m pytest edgeml/tests/test_nightlights.py -q
```

## v5 freeze and locked test4

The v5 detector is frozen at commit
`3bfa51bde7b0eb8602cd31b29fe59ff1d42d9719`, with pattern-confidence threshold
**0.85**. Dev-only results and the precision/recall trade-off are recorded in
`V5_DEV.md`. Strict complete-multiset equality and zero white allowance remain
unchanged. No detector or threshold tuning is permitted after opening test4.

Hard/test4 uses fresh seed base **4200000**, with 112 night contacts, 8 day
records and 16 twilight records. It is to be generated only after this freeze
record is committed, then detected exactly once as v5. Boost and dark-contact
summaries must consume that saved observation dump without image redetection.
Easy/test may subsequently be evaluated as a regression set, without retuning.

Freeze record commit: `24ae0376c0df400b19b857457279c6b157d9cd73`. Test4 was
subsequently generated and evaluated once, without crashes or detector changes.
Boost and dark summaries used the saved `hard_test4_v5.json` observations.
Test4 light P/R are 72.90%/50.32%, rules 34/112, automatic 20/98, automatic
urgent boosts RAM/NUC/draught 2/0/0 with zero false boosts of any kind. Easy/test
retains 100% light P/R, 112/112 rules, 98/98 automatic and 7/7 correct boosts
for each urgent rule. Saved-observation false-dark counts are 0/94 observable
lit night test4 contacts and 0/112 easy/test contacts; no unlit-vessel recall can
be estimated from these sets. Full v5 results, precision trade-offs, demo changes
and commands are in NIGHT_LIGHTS.md. Neither v4 artifacts nor the frozen v5
detector/threshold were changed.

## API and meaning

`analyze(image_or_frame_list, [x,y,w,h], modality="EO", timestamp=None,
lat=None, lon=None, rules=None, signal=None)` returns night/luminance/confidence/
basis, candidate light coordinates in original pixels and relative to the box,
flashing observations, and a rule/aspect/plain-language/boost interpretation.
`load_rules(path)` accepts an explicit JSON file path; the default is the repository
catalog. Rule order is specificity precedence. `interpret` also enforces night
and modality guards when used directly. IR/SAR suppresses colour extraction and
interpretation. SAR intensity alone does not establish astronomical night.

Confidence is heuristic, not calibrated probability. Confident image decisions
override solar elevation; ambiguous images use the NOAA approximation at -6 degrees
when complete timezone-aware metadata is supplied, otherwise report
`image_ambiguous`. Images must be nonempty uint8 BGR arrays.

## Limits, especially for stage 2

- This is a simple procedural benchmark with bright, separated lights, static
  tracks and limited noise/blur/scale variation. Perfect synthetic scores do not
  validate real-world accuracy. There are no human-labeled real navigation lights.
- Library bright-point candidates (especially the many UAV-frame detections) are
  unvalidated texture/glare candidates, **not confirmed ship navigation lights**.
  No thresholds were fitted to the library. Do not promote all these to alerts.
- Flashing means intermittent presence in spatially registered contact frames;
  occlusion, motion, or detector misses can look like flashing. The caller should
  register moving tracks; there is no frequency estimation or Morse decoder.
- Distress rules interpret externally identified `signal="red_flare"` or
  `signal="sos"`. The benchmark supplies these labels explicitly and reports
  automatic rules separately. It does **not** demonstrate automatic flare/SOS
  detection; the rendered distress points are only proxies, not realistic flares.
- Red alone means her port side, typically right-to-left crossing; green alone
  means her starboard side, typically left-to-right. Vertical all-round stacks
  are not sidelights. Horizontal whites infer direction only conditionally on
  higher-aft/lower-forward masthead identification; equal-height/vertical whites
  leave aspect unknown. Two whites remain towing/large power-vessel ambiguous.
  These observations cannot determine collision risk or right of way.
- The rule table contains the requested signatures and references, not every
  possible configuration under Rules 20–31. Routine anchor/stern white ambiguity
  is explicit. Blue law-enforcement lights depend on local law, not COLREGS.
- Stage 1 included no Go integration or UI. Current v4 integration and synthetic
  UI evidence are documented in NIGHT_LIGHTS.md; no operational validation or
  production deployment is claimed.
