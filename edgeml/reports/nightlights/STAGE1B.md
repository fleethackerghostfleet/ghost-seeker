# Stage 1b: hard tier and optical-only light analysis

## Evaluation protocol

Current results are `easy_dev`, `easy_test`, `hard_dev`, `hard_test` JSON/Markdown.
The earlier `dev`/`test` reports and PROTOCOL.md are historical stage-1 baselines.
All generated evaluation images and labels remain under gitignored
`data/nightlights/{easy,hard}/{dev,test}`. Demo JPEGs are the only images intended
for git. Each tier/split has 112 contacts with four-frame tracks, plus eight day
images. Hard adds 16 twilight images, eight either side of civil twilight; their
labels use the supplied equatorial UTC solar geometry rather than rendered brightness.

Easy seeds retain the baseline. Hard uses new seed bases 300000 (dev) and 900000
(test), plus rule_index*100+repeat. Day/twilight use offsets 9000/9500.
Only hard-dev was used to adjust the new filtering. It exposed severe pier/shore
confusion, and row rejection was added; no effort was made to force perfect scores.
Unit tests caught an overly restrictive area floor for normal 3 px-radius blobs;
that regression was corrected. No hard-test data was generated or read before
freezing the candidate below; hard-test is evaluated once, with no later tuning.

Frozen SHA256 before the first hard-test evaluation:

```text
f101401db039f2cd58086b400c08c0e76fc32c852dfccb1adbb7d6ef3115a1c0  edgeml/src/edgeml/nightlights/core.py
ee6df4ba21c518c06efcc00d8ea7e0e61693b44b36a20a966a20e4a330681eeb  edgeml/scripts/nightlights_synth.py
69fa621d3c2fc74f456192229ca6f42396dd75c5a9df0d2ba0d7b0c516ec0962  edgeml/scripts/nightlights_eval.py
```

## Rendering and metrics

Hard contacts include one-pixel and 3-pixel-diameter lights as well as nearer
larger points, defocus, horizontal motion blur, blooming close stacks, warm white,
orange-shifted red, cyan-shifted green, saturated white cores, permanent partial
occlusion, reflections, sodium/white shore lights and lit pier rows, moon glints,
low-light noise, and lossy JPEG before saving benchmark PNGs. The scene nuisance
layout stays fixed across a track. All nuisance points are labeled independently
of the navigation-light labels. JSON reports retain full source labels and
detector observations for auditing.

Colour matching is spatial (6 px), then colour-correct; wrong colours penalize
both precision and recall. The confusion matrix includes a background row for
unmatched predictions, including pier/shore false positives. Permanently occluded
lights are excluded from light recall, but rule/aspect scoring retains the latent
full vessel configuration. Thus some rule failures are intentionally unresolvable.
Flashing accuracy includes missed observable lights as errors. Aspect scores
include the many genuinely unknown-aspect labels; they are not heading accuracy.
Distress still receives external signal labels and is separately scored. It is
not automatic flare or Morse recognition.

## Gate and filtering

SAR/radar returns `night: null`, `basis: not_applicable_radar`; it cannot establish
astronomical night from radar backscatter. IR can receive an image-statistics night
estimate, but colour extraction/rules remain disabled. Only optical images enter
light detection. The gate additionally considers HSV-value shadow coverage to
avoid confusing diffuse blue daylight water with underexposed night. Confidence
remains heuristic. Complete UTC/position metadata resolves ambiguous images.

Blob filters now enforce bounded size, local peak/background contrast, circularity,
aspect ratio, background texture, horizontal pier-row rejection, and a maximum of
eight candidates per contact (including the track union). Isolated shore lights
can still be observationally indistinguishable from navigation lights. Hard-tier
precision exposes that limitation; these results are not safe automatic alerts.

## Real library inspection

Viewed all four formerly flagged UAV images at native 960x600:
`web/static/imagery/uav-3-{01,02,06,08}.jpg`.
They are daylight aerial photographs, not night: clearly illuminated blue water,
sun glint, wakes, visible deck detail and container colours. No library thresholds
were adjusted from those images. The updated gate clears their false-night labels.
All 48 real images are regenerated with 2x coordinate scaling; eight SAR entries
are not applicable, and no real frame now produces night-light candidates.

## App demo

`nightlights_demo.py` renders eight 960x600 JPEGs using the hard renderer, with
1–3 contacts per frame and fixed 3 px-radius visible demo lights. Frame 01/03 are
harbor scenes; the others are offshore. Frames 05–08 show a red on/off distress
proxy with externally reported SOS, explicitly **signal-assisted**, not a Morse
waveform or flare detector. All detections are run on reopened saved JPEGs;
truth labels are stored separately. The same sequence is analyzed for flashing.
Failures are preserved, not relabeled. See demo.md for detected-versus-truth rules.

The catalog retains its original per-file keys and adds a reserved `nightLibrary`
array. Consumers must not iterate that array as if it were an ordinary real-frame
entry. Demo `b` boxes use 480x300 coordinates, lights use original 960x600 pixels,
and classes are validated against catalog.go. No Go integration is included.

## Exact commands

All run from the repository root with the existing CPU-only virtualenv:

```sh
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier easy --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier easy --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier easy --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier easy --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier hard --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier hard --split dev
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --out data/nightlights --tier hard --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier hard --split test
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --library
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_demo.py
/home/ubuntu/nightlights-venv/bin/python -m pytest edgeml/tests/test_nightlights.py -q
```
