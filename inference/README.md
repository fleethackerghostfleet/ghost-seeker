# Inference engine (interchangeable)

The edge never imports a model. It calls `InferenceService` (see
`proto/ghostseeker/v1/inference.proto`) over HTTP (Connect protocol, JSON or
protobuf). Anything that answers these three calls is a valid engine:

| RPC | In | Out | Used for |
|---|---|---|---|
| `Analyze` | image bytes, current known-label vocabulary | detections, L2-normalized embedding, `unknownness` 0..1 | scoring every ingested image |
| `EmbedText` | strings | embeddings in the same space | shore compiles operator intent ("ignore sailboats") into vectors; the edge compares them to stored image embeddings, so no language model runs aboard |
| `Capabilities` | – | model id, open-vocab?, fixed labels, dim | UI readback / linting |

`unknownness` is the engine's job because it depends on the model: suggested
definition is the max of (1 − best detection confidence), (1 − max cosine to
the known-label text prompts), and an outlier score against the recent
embedding history. The edge treats ≥ `thresholds.unknown_min` (default 0.5)
as "unknown" and ranks those first.

Implementations:

- `edge/inference.Mock` (Go, in-process): deterministic, reads ground truth
  from scenario hints. Used by the simulator and tests.
- `inference/stub/server.py`: the Python shape of a sidecar, no model. Start
  it and point the edge at it with `-infer http://localhost:8090`.
- Planned: ONNX Runtime sidecar (OpenVINO EP on the rugged Dell's Intel CPU)
  with a YOLO-family detector + MobileCLIP-class embedding. CPU-only.

Wire format for JSON: `POST /ghostseeker.v1.InferenceService/Analyze` with
`{"image": "<base64>", "knownLabels": [...], "wantEmbedding": true}`.
