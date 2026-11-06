# Live directive open-vocabulary matching

## Verdict

The live directive-to-edge-to-sidecar path supports five arbitrary visual
phrases without a protobuf change. Held-out clean/v3 frame-tag results show
useful but incomplete buoy/jetski retrieval; **life-saving appliances had zero
recall**. Buoy test false-positive rate exceeded the 5% dev calibration budget.
This is an operator-interest cue, **not a validated SAR alarm or a fine object
localizer**. No model training or licensing decision was performed.

## Runtime and bounds

* A capable edge is discovered through authenticated `GET /ship/inference`,
  backed by the existing capability RPC; shore caches it for 15 seconds and
  fails closed when unavailable. Existing `/api/v1` responses are unchanged.
* Unknown noun phrases in command/research directives compile to normal policy
  labels. The edge extracts the effective policy's boost/suppress/tier phrases
  and sends `open-vocab:<phrase>` in `AnalyzeRequest.known_labels`. A directive
  exceeding five novel phrases is blocked; combined policy layers are bounded
  deterministically to five. Removed policy prompts do not persist in requests.
* MobileCLIP-S1 embeds up to eight YOLO proposals, the full frame and a 2×2 grid
  (5–13 regions). Evaluation observed mean 10.66 regions/frame. No ground-truth
  boxes are used for inference. One best accepted region becomes a normal
  labeled detection per phrase. Matching frames survive sighting warm-up and
  scene deduplication and participate in the existing policy boost rules.
* Positive text prototypes average normalized `a photo of a <phrase>` and
  `a photo of a <phrase> at sea` vectors. Generic negatives are open sea, waves
  and sky, empty ocean horizon, sea spray/foam, blurry water, a boat and a person
  (exact templates in `sidecar/open_vocab.py`). Both cosine similarity ≥0.275
  and positive-minus-best-negative similarity ≥0.05 must hold in one region.
* A thread-safe 128-entry LRU shares text embeddings across Analyze and
  EmbedText; zero prompts skip all additional text/grid/crop inference. There
  are 9 cached embeddings for one prompt and 17 for five, including negatives.
  Detection confidence is an acceptance score, not a calibrated probability.
* Optional ONNX text assets are loaded beside the existing image model. Missing
  assets advertise `openVocabulary=false` and arbitrary-text requests fail with
  `failed_precondition`. Image-only inference remains supported.

## Held-out evaluation

Source: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/clean/v3/coverage-20261105/catalog/`
(`images/seadronessee.jsonl` and `annotations/seadronessee.jsonl`). Only human
`source=gt` annotations and clean/v3 image URIs were accepted. Every downloaded
image SHA-256 was checked against the catalog. No v1 or IR-maritime data was
used. SeaDronesSee is recorded as CC0-1.0 in the v3 provenance; Singapore
Maritime kayak/sailboat rows are quarantined and were deliberately excluded.

The query mapping is `buoy → buoy`, `jetski → jetski`, and
`life saving appliances → life_saving_appliances`. These queried classes are
held out of closed-set classification: the evaluator never invokes the fixed
catalog classifier or feeds it query labels. YOLO supplies generic proposals
only; all positive decisions come from arbitrary-text similarity plus the
negative margin, full frame and coarse grid. This is not a new trained detector.

For each v3 dev/test split, sort by image SHA-256 and take up to 50/100 images
per queried class plus 50/100 images containing none of the queries; deduplicate
the union by SHA-256. The resulting 508 frames are 154 dev frames from 29
source videos and 354 test frames from 55 different source videos. The evaluator
asserts no dev/test source-video overlap. Some frames contain multiple labels.

Threshold search uses **dev only**, maximizing macro query recall subject to
≤5% negative FPR for **every** query, over cosine 0.20…0.50 in 0.025 steps and
margins `{0, .005, .01, .015, .02, .025, .03, .04, .05, .075, .1}`.
Ties favor the lower cosine threshold, then lower margin. This selected
**0.275 / 0.05** before test was evaluated; no tuning on test followed.

| Dev query | TP | FP | FN | TN | Precision | Recall | Negative FPR |
|---|---:|---:|---:|---:|---:|---:|---:|
| buoy | 29 | 3 | 31 | 91 | 90.63% | 48.33% | 3.19% |
| jetski | 16 | 3 | 34 | 101 | 84.21% | 32.00% | 2.88% |
| life saving appliances | 0 | 0 | 50 | 104 | N/A | 0.00% | 0.00% |

| Held-out test query | TP | FP | FN | TN | Precision | Recall | Negative FPR |
|---|---:|---:|---:|---:|---:|---:|---:|
| buoy | 72 | 15 | 112 | 155 | 82.76% | 39.13% | 8.82% |
| jetski | 48 | 4 | 62 | 240 | 92.31% | 43.64% | 1.64% |
| life saving appliances | 0 | 0 | 100 | 254 | N/A | 0.00% | 0.00% |

Metrics are **frame-tag**, not IoU/AP: a positive frame contains at least one
human-labeled instance, and a prediction is at least one accepted region for
that query. Precision = TP/(TP+FP), recall = TP/(TP+FN), negative FPR = FP/(FP+TN).
N/A means no positive predictions (the machine output uses zero for this empty
denominator). Negatives for each query include all selected frames lacking that
query, including frames positive for another query. The stratified sample is
not a fleet prevalence estimate; precision does not transfer directly to sea
traffic. Adjacent frames within held-out videos are correlated. No claim is
made about untested phrases such as oil slick, life raft or orange life jacket.

Audit SHA-256 values:

* selected manifest: `f20aa4ccc956615157d1aa20fa4ac417c5bff1e1942884bfcb8c39c27bcfbd05`
* image catalog: `4ed8a8fa91e19ba81bae1b90f3a2b3435cddd351ebee7fac8a821761e03b4f24`
* annotation catalog: `add0c4c9bffe0b2d67c694cb5373d15ee2b93ca4e96ae11070087f71816c2834`

## Per-frame CPU latency

Real `EdgemlEngine.analyze`: detector, ByteTrack, sighting aggregator and optional
open-vocabulary crop/grid matcher. Excludes JPEG decoding, network/RPC and model
session creation. Same first 12 test images by manifest order, two repeats,
24 timed frames per condition, fresh tracker source per frame. Text cache is
emptied per condition, one first-frame warm-up is measured separately, then
cached-prompt timings follow. It does not measure sustained same-video tracking.

| Prompts | Mean ms/frame | Median ms | p95 ms | Mean delta vs 0 | First-frame ms |
|---:|---:|---:|---:|---:|---:|
| 0 | 249.58 | 274.80 | 369.85 | — | 1344.68 |
| 1 | 907.25 | 908.74 | 1022.46 | +657.66 | 1254.15 |
| 5 | 850.03 | 825.78 | 1097.65 | +600.45 | 1657.61 |

One and five prompts share the same image region work; their small-sample
ordering reflects measurement variation, not a speedup from additional prompts.
First-frame 0 includes first-use runtime warm-up; 1/5 include cold text caches
but already initialized sessions, so those cold values are not directly
comparable. CPU cost is substantial: **this configuration is not 5-FPS capable
with active open vocabulary**. Validate target edge hardware and ingest cadence
before deployment. Maximum prompt and region counts bound work, not a hard
wall-clock latency guarantee.

Hardware/runtime: Linux 6.8 x86_64, Intel Xeon, 8 vCPUs, CPU-only ONNX Runtime
1.23.2 (4 intra-op threads), NumPy 2.2.6, OpenCV-headless 5.0.0.93 (1 thread),
tokenizers 0.21.4; benchmark uses boxmot 26.0.0, matching the repository adapter.
The latency run did not overlap the evaluator, verifier or image builds.

## Text assets and provenance

New separate release: https://github.com/fleethackerghostfleet/ghost-seeker/releases/tag/models-text-v1
(`demo-data-v1` is unchanged). Text tower derives from MobileCLIP-S1 `datacompdr`
weights; tokenizer is `openai/clip-vit-base-patch32`. ONNX/PyTorch maximum absolute
embedding error was 3.88e-7; existing cached prototypes differed by at most
2.99e-7. `deploy/models/fetch.sh` verifies the separately pinned asset checksums;
`prepare.py` includes a complete verified text pair or retains image-only support.

* text model SHA-256: `7e95132370cfdb1a9ad9c9f0c525034c9412841bba80cabcbee89e238d0d01bf`
* tokenizer SHA-256: `6d9109cc838977f3ca94a379eec36aecc7c807e1785cd729660ca2fc0171fb35`

MobileCLIP's Apple research terms and YOLO11n's AGPL-3.0 considerations remain
**UNRESOLVED**. See `deploy/models/THIRD_PARTY_NOTICES.md`; these results do not
authorize commercial deployment or resolve either model's license.

## Reproduce

With authorized GCP access, the published v3 catalogs downloaded to `$CATALOG`,
verified model assets at `$MODELS`, exported text assets at `$TEXT`, and the
edgeml/ONNX/tracking dependencies installed:

```sh
python edgeml/tools/eval_open_vocab.py --catalog "$CATALOG" --models "$MODELS" \
  --text "$TEXT" --output "$EVAL" --sample 50 --gcloud gcloud
# Place text_model.onnx and tokenizer.json alongside $MODELS/mobileclip-s1/model.onnx.
python edgeml/tools/bench_open_vocab.py --evaluation "$EVAL" --models "$MODELS" \
  --output "$EVAL/latency.json" --frames 12 --repeats 2
```

The evaluator saves the exact selected manifest, hash-verified images, region
features and metrics JSON. Use a fresh output directory after changing the
models or region generator; cached features intentionally avoid re-embedding
unchanged images, not model-version detection. The accompanying
`OPEN_VOCAB.metrics.json` records counts, thresholds, environment and timings.

## Coverage

Go tests cover parser/research capability gating, prompt limits/normalization,
authenticated capability discovery/cache/fail-closed behavior, effective-policy
transport, prompt removal, sighting retention and policy boosts. Python tests
cover arbitrary-text service behavior, bounded labels/regions, shared LRU reuse,
concurrency/eviction, invalid vectors, missing assets, threshold/negative gates,
grid matching and verified optional-asset packaging. Production contract tests
and the gate are unchanged; no protobuf or versioned shore API fields were added.
