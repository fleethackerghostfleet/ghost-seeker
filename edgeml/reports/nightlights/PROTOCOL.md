# Night-light candidate: protocol and limitations

Historical stage-1 baseline. See STAGE1B.md and easy_*/hard_* reports for the
current candidate, tiered paths, mode-aware gate and demo metadata.

CPU only, Python 3.10, NumPy 2.2.6, OpenCV headless 5.0.0.93.
The system pip/setuptools could not install editable PEP 660 packages. Installation
was completed in `/home/ubuntu/nightlights-venv` after upgrading pip/setuptools;
no repository dependency/build files were changed.

## Frozen candidate before first test evaluation

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
- No Go code, integration, production deployment, or UI rendering is included.
