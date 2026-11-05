# Provider handoff

A teammate can replace object, visual-interest or quality scoring independently. The analyzer calls the providers once, validates and caches their outputs, and adds color and pHash similarity descriptors. Ranking and selection operate on that cache. Neither provider receives labels, bandwidth or operator policy.

## Contract

Interfaces are in `ghost_priority.contracts`, which imports only the Python standard library. Implementations do not need to inherit a base class; each must expose a nonempty string `name` and an `analyze(bgr)` method.

| Role | Return value | Meaning |
|---|---|---|
| `ObjectProvider` | `ObjectResult(relevance=None, detections=None)` | Direct object relevance, supported detections, or both |
| `VisualProvider` | `VisualResult(interest, components={}, regions=[])` | Visual interest with optional explanations/candidate regions |
| `QualityProvider` | A finite float in [0,1] | Image usefulness/quality signal |

Input is the original nonempty `uint8` NumPy array with shape `(height, width, 3)` in **BGR** order. Providers must not mutate it; the analyzer supplies a read-only view. Convert to RGB or resize inside the provider if its model requires that. Return boxes in original-image coordinates after undoing model resize/padding.

All scores, detection confidences and visual components must be finite in [0,1]. Invalid values are rejected rather than silently clipped. Bounding boxes use `[x, y, width, height]` in original-image pixels, must have positive area and remain within the image. Clip predictions in your adapter if the model emits out-of-image boxes; keep source ground truth separate.

Object detections have this schema:

```python
{"label": "boat", "confidence": 0.8, "bbox_xywh": [120, 80, 90, 40]}
```

Visual candidate regions use:

```python
{"kind": "visual_candidate", "score": 0.6, "bbox_xywh": [120, 80, 90, 40]}
```

These examples illustrate schemas, not measured predictions. Visual regions are not automatically object detections. Component keys must be nonempty and cannot use the reserved names `visual_interest`, `novelty`, `quality` or `object_relevance`.

## Object relevance versus detections

- `ObjectResult()` means object evidence was not evaluated.
- `ObjectResult(detections=[])` means evaluated with no detections.
- `ObjectResult(detections=[...])` lets operator class weights determine relevance later.
- `ObjectResult(relevance=0.8)` supplies direct relevance; no category weighting is applied.
- `ObjectResult(relevance=0.0, detections=[...])` still uses the direct zero. Detections are retained for display, not used to replace that score.

Direct relevance takes precedence whenever it is not `None`. Choose detections without a direct score when the UI needs to reweight classes from cached observations. A direct provider can still explain its score through detections, but category-weight changes will not alter that direct object contribution.

Preserve what the engine actually recognizes. Do not convert a generic `boat` prediction into an unsupported vessel subtype or invent motion, affiliation, tracks or AIS correlation.

## Built-in providers

| Class in `ghost_priority.providers` | Behavior |
|---|---|
| `NoObjects()` | Returns unevaluated object evidence |
| `NanoDetObjects(model_path)` | Generic COCO detections using the pinned OpenCV Zoo NanoDet model |
| `ClassicalVisualInterest(width=320)` | Local contrast and spectral saliency; spatial distinctiveness, not learned unknown-category recognition |
| `SharpnessExposureQuality(width=320)` | A lightweight sharpness/exposure heuristic |

`Analyzer(model_path=None, width=320, threads=0, *, object_provider=None, visual_provider=None, quality_provider=None)` accepts instances directly. `model_path` enables built-in NanoDet and cannot accompany `object_provider`.

`width` caps the longest side for the built-in visual/quality providers and the fingerprint. Injected providers receive original pixels and control their own preprocessing. `threads` configures OpenCV, not arbitrary third-party inference runtimes. CPU-only behavior is provided by the built-ins; a custom provider must enforce its own compute/device requirements.

## Runnable integration smoke test

Save the following as an importable `team_demo.py` beside the `ghost_priority` package. It uses constant **test scores** solely to check integration. It performs no detection or model evaluation.

```python
from ghost_priority.contracts import ObjectResult, VisualResult

class DemoObjects:
    name = "demo-constant-objects-v1"

    def analyze(self, bgr):
        return ObjectResult(relevance=0.4)

class DemoVisual:
    name = "demo-constant-visual-v1"

    def analyze(self, bgr):
        return VisualResult(interest=0.2, components={"demo_component": 0.2})

class DemoQuality:
    name = "demo-constant-quality-v1"

    def analyze(self, bgr):
        return 0.7

def make_objects():
    return DemoObjects()

def make_visual():
    return DemoVisual()

def make_quality():
    return DemoQuality()
```

From the `algorithm` directory:

```bash
.venv/bin/python -m ghost_priority analyze --images /path/to/images --object-provider team_demo:make_objects --visual-provider team_demo:make_visual --quality-provider team_demo:make_quality --out runs/provider-smoke-test
```

Each specification is `module:factory`; the factory takes no arguments and returns one provider instance. Python must be able to import the module through its normal search path. Install the team's package or add its directory to `PYTHONPATH` when necessary. There is no automatic plugin discovery. Importing a chosen module executes its Python code, so use trusted team modules. Load models in the factory/provider constructor rather than inside every `analyze()` call.

Programmatic injection uses the same instances:

```python
import numpy as np
from ghost_priority.analysis import Analyzer
from team_demo import make_objects, make_visual, make_quality

analyzer = Analyzer(
    object_provider=make_objects(),
    visual_provider=make_visual(),
    quality_provider=make_quality(),
)
# Synthetic integration input, not an evaluation image.
image = np.zeros((32, 48, 3), dtype=np.uint8)
image[8:16, 12:20] = 180
record = analyzer.analyze(image)
assert record["object_relevance"] == 0.4
assert record["signals"]["visual_interest"] == 0.2
assert record["signals"]["quality"] == 0.7
```

## Wrap an existing teammate model

The adapter should call the model's existing inference function and convert its result. For a model whose existing `predict(bgr)` returns a bounded object-relevance scalar:

```python
from ghost_priority.contracts import ObjectResult

class ObjectScoreAdapter:
    def __init__(self, predict, name):
        self.predict = predict
        self.name = name

    def analyze(self, bgr):
        return ObjectResult(relevance=float(self.predict(bgr)))
```

Instantiate it with the real model's `predict` callable and a versioned name. This wrapper assumes that callable already accepts BGR and produces a finite [0,1] relevance score; add the model's actual preprocessing and score conversion when that is not its contract. For category detections, return `ObjectResult(detections=converted_predictions)` instead and leave `relevance=None`. For visual or quality models, adapt their real outputs into `VisualResult(interest=...)` or a bounded quality float respectively. Do not add capabilities through naming alone.

A no-argument CLI factory can load the real model/configuration from the team's normal configuration mechanism and return this adapter. That loading is model-specific and intentionally not invented here.

## Cached output and validation checklist

The analyzer records:

- `providers`: the object, visual and quality provider names.
- `object_relevance`: supplied direct score or null.
- `detections`: validated detections or null, independently of direct relevance.
- `signals.visual_interest`, `signals.quality`, plus visual components. `signals.novelty` aliases visual interest for old consumers.
- `regions`: explanatory visual regions in original coordinates.
- `fingerprint`: the analyzer's coarse color layout, separate from provider scores.
- `phash`: versioned 64-bit structural hash and preprocessing size; used alongside color by default. See [SIMILARITY.md](SIMILARITY.md).
- `usable`, `unusable_reason`, `engine` and per-provider timings. Exactly constant images are marked unusable by the analyzer even if a custom provider returns a nonzero score.

Before handoff, check a real frame and a small/blank input; verify color order and original-coordinate boxes; distinguish no evaluation from no hits; confirm finite bounded scores; ensure repeat calls do not reload the model; and record actual runtime/device dependencies. Compare runtime and missed examples on held-out data. Labels must remain outside all provider inference calls.

Once records are cached, use `rank_records()` and `select_ranked()` as described in [Ranking and selection](RANKING_AND_SELECTION.md). Swapping a provider requires new analysis; changing only operator policy or bandwidth does not.
