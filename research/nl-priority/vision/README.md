# Research vision adapter

This component imports the existing fixed-vocabulary `ZeroShotClassifier` for baseline inference, and adds a separate stateless candidate-label comparison. It never edits baseline labels, human overrides, or unknownness. Only a novel candidate that beats the base vocabulary and background, with cosine at least 0.25 and margin at least 0.02, is accepted. These are conservative **uncalibrated research defaults**, not probabilities. Tune thresholds on a development set, freeze them, then evaluate a separate held-out set before promotion.

The runtime requires ONNX Runtime, NumPy, Pillow, ftfy, and regex. It imports neither Torch nor OpenCLIP. The Docker build copies only the existing NumPy-only baseline classifier and this research package. Text embeddings and candidate prototypes are cached. Image inference is loaded lazily and is only used when importing already detected crops; this adapter is not a new object detector.

## Export

The export environment additionally contains Torch, OpenCLIP, and ONNX. `Dockerfile.export` accepts the repository root as context. Run:

```sh
python -m edgeml_research.nl_priority.export --output /models/mobileclip --cache-dir /models/cache
```

The exporter pins the official MobileCLIP-S1/datacompdr source state by SHA-256, exports FP32 image/text graphs, and evaluates dynamic INT8 text quantization. It compares the standalone tokenizer against OpenCLIP, checks graph parity, and writes `validation.json`, `manifest.json`, and checksums for every runtime asset. The model ID is based on the original weights, so both image and text towers share it. A manifest mismatch prevents startup; an item model mismatch prevents classification. The default source state hash is `497a114a99bfedd5e3f5bbea2bf29288770eb6bc9262957f5ac6e3f368b7a4b7`.

INT8 is selected only if minimum/mean cosine agreement with the FP32 reference reaches 0.98/0.995 on the fixed parity corpus. In the recorded run INT8 failed that gate, so the runtime selected FP32. Do not force the smaller graph just to claim a memory target. This export test verifies numerical fidelity, not the reliability of new labels on real images.

## HTTP contract

Launch `python -m edgeml_research.nl_priority.server --models /models/mobileclip --listen 0.0.0.0:8098`.

- `GET /health`: model ID, dimension, thresholds, peak resident memory, readiness.
- `POST /embed`: `{ "texts": ["rescue boat"] }` returns model ID, dimension, normalized embeddings.
- `POST /classify`: `{ "concepts": ["rescue boat"], "items": [{ "id": "1", "embedding": [], "modelId": "..." }] }` returns the full comparison vocabulary and per-item `label`, `similarity`, `margin`, `accepted`, `experimental`, and `reason`. Original items are not mutated. Base-vocabulary winners return `accepted: false`; they must not overwrite the original classification.
- `POST /image-embed`: `{ "images": [{ "id": "1", "imageBase64": "..." }] }` returns matching embeddings and separately named baseline subtype/unknownness diagnostics. Inputs must already be object crops. No detector is implied.

## Import and evaluate

`python -m edgeml_research.nl_priority.ingest --manifest crops.jsonl --output queue.json [--backend-url http://localhost:8097]` encodes actual detected crops. The manifest accepts a file `path`, optional pixel `box: [x1,y1,x2,y2]`, and original detector/priority metadata. It emits the backend `/api/items` schema and compact preview thumbnails. Missing detector metadata remains explicitly unknown. No ground-truth fields are read as features.

`python -m edgeml_research.nl_priority.evaluate --items queue.json --annotations heldout.jsonl --concept 'rescue boat' --output labels-report.json` reports per-concept precision, recall, support, and abstention. Ground truth has `{ "id": "...", "labels": ["rescue boat"] }` rows and is never sent to inference. Empty/unsupported ground-truth classes yield null metrics, not a passing score.

The 45-crop smoke run used actual existing detector crops, preserved their original metadata, and accepted no novel labels under the conservative defaults. Cached classification of 1,000 repeated embeddings took 98–359 ms across ten requests on the development Mac. This demonstrates the inference path and abstention; it does **not** establish novel-class accuracy or a full-system resource gate.
