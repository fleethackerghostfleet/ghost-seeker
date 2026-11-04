# Detector improvement round and Gemini comparison

The stage-1b baseline is preserved in `hard_{dev,test}_v1.{json,md}` and the
original `hard_test` report remains unchanged. All numeric detector tuning used
hard-dev only. The seen v1 hard-test was not rerun. A fresh hard-test2 uses seed
base 2700000, disjoint from all previous splits; rendering itself is unchanged.
The candidate below was frozen before generating or reading test2. Test2 is
evaluated once by the full-frame evaluator; the crop comparison is a separate,
predeclared measurement with no subsequent detector tuning.

Frozen SHA256:

```text
ae0ce075cb278944aab01b71dec3870835d1e630824ec95641f0ecf5b08c2a95  edgeml/src/edgeml/nightlights/core.py
6e6bee1ad3fa285ca6c564da689c22f75cb96afbc1cb3d8eb98da7476e7abde1  edgeml/scripts/nightlights_synth.py
e4ce9247f535250bbd5345da7fa35f9c85aa9cd96f2e67dcdc2e861a5609507c  edgeml/scripts/nightlights_eval.py
f067f1d3b1be37da6c518e0b502b6fb4db69abfd0b7f620bd925cec8a8adf4c9  edgeml/scripts/nightlights_gemini.py
```

## Detector changes and limits

- Estimate hue from chromatic pixels in a small dilated halo instead of white
  saturated cores. Expand red toward orange. Lower peak/contrast thresholds for
  dim one-pixel emitters; retain minimum area one (already the previous minimum).
- Allow modest motion elongation. Retain texture, area and eight-candidate caps.
- Reject white/yellow candidates on long approximately regular pier rows using
  full-image context; reject detections below the box's conservative waterline
  prior (85% height). This is **not** a learned horizon/hull segmentation system.
- Suppress isolated sodium-band candidates instead of calling them yellow.
  Orange lights with a vertical partner may still be legitimate navigation
  stacks. Sodium and orange-red ship lights overlap physically; this rule can
  lose real isolated orange-red sidelights and retain some shore lights.

The first hard-dev pass improved recall but turned many sodium lights into red
false positives. A second dev-only pass added the sodium-context check. A unit
test then exposed an area ceiling rejecting a saturated 4 px halo; the bounded
area floor was corrected before freezing. No test2 inspection informed changes.

## Sanity / unresolvable labels

OpenCV radius zero emits ONE pixel, not a subpixel or an absent light. The hard
renderer does deliberately destroy some evidence: permanent occlusion, and white
replacement of a single-pixel coloured emitter before bloom. The latter removes
direct emitter/halo colour, although a reflected streak may retain a colour cue.
`unresolvable` means the complete **direct-light pattern** cannot be observed,
not that a contextual guess or externally supplied distress signal is impossible.
Records carry explicit reasons; existing dev records are annotated on evaluation,
and newly generated test2 labels store them. Nothing is removed from rule totals.
The historical light metric excludes occluded emitters from visible-light recall,
as before, while all contact cases remain scored. JSON and Markdown report rule
accuracy separately for resolvable and unresolvable cases. Low SNR, blur and
merging alone are NOT silently labeled unresolvable or removed.

## Fair Gemini comparison protocol (declared before test2 generation)

Use 112 contacts each from hard-dev, hard-test2 and easy-test: 336 requests total.
Each request contains the four consecutive contact crops, padded by 20%, saved
as JPEG quality 95 at native resolution. Both Gemini and our frozen detector
receive the identical JPEG-decoded crops; neither receives truth or externally
identified distress signals. Crop-context loss affects our global pier-row filter.
The prompt includes the rule table, not labels. Gemini returns strict JSON
`{lights:[{color,rel_y}],rule_id}` with top-to-bottom normalized y. Configuration:
Gemini 2.5 Flash, global Vertex endpoint, temperature 0, thinking budget 0,
max output tokens 1024, response MIME application/json; four workers, hard cap 400.

Gemini only returns y order, not x/precise position. Therefore **both** methods
use per-contact colour-multiset overlap for comparison precision/recall; these
numbers must NOT be compared directly with the full-frame spatial-match metric.
Wrong counts and nuisance lights penalize precision. Per-colour/per-rule details,
unresolvable strata, and individual predictions are in gemini_comparison.json.
Rel_y is validated but not used to invent spatial matching. Rule accuracy includes
all latent labels, including distress (which cannot generally be inferred from
these four frames alone). Full-frame benchmark distress remains signal-assisted.

Responses, HTTP errors and latency per call are cached under
`data/nightlights/gemini/<request_sha256>.json`, keyed by endpoint, full prompt,
images and generation configuration. JPEG crops are saved in its crops directory.
There are no automatic retries; rerunning unchanged requests costs zero calls.
Failures/invalid JSON score as empty detections and incorrect rules, not excluded
examples. The OAuth token is captured in memory only and never printed/cached.
The first call is a preflight: service failure stops before submitting the batch.

## Commands (repository root)

```sh
/home/ubuntu/nightlights-venv/bin/python -m pytest edgeml/tests/test_nightlights.py edgeml/tests/test_nightlights_gemini.py -q
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split dev --version v2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_synth.py --tier hard --split test2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test2 --version v2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier easy --split test --version v2
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --library
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_demo.py
/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_gemini.py --workers 4 --max-calls 400
```
