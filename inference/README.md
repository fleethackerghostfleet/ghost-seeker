# Inference engine (interchangeable)

**Status: reference/contract only — the running sidecar is [`edgeml/`](../edgeml/sidecar/README.md).**
This directory retains protocol notes and a manually runnable, model-free Python
example (`stub/server.py`), not the deployed inference engine. The authoritative
wire contract is [`inference.proto`](../proto/ghostseeker/v1/inference.proto);
the live Go client is [`edge/inference/`](../edge/inference/inference.go).
The stub is also referenced by the historical
[security assessment](../docs/security/STIG_PENTEST_REPORT.md).
No checked-in test or CI job was found that starts or imports this stub, and
there are no Python tests in this directory. `make demo-sidecar`, the demo
Compose service, the packaged sidecar and CI's sidecar tests use edgeml instead.
Keep the stub for manual HTTP/JSON contract exploration; it is runnable reference
code, not real-model inference.

The edge never imports a model. It calls `InferenceService` (see
`proto/ghostseeker/v1/inference.proto`) over HTTP. The Go remote client and
Python sidecars use Connect with protobuf-JSON. Anything that answers these
three calls is a valid engine:

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
- `edgeml/src/edgeml/sidecar/`: implemented CPU sidecar with ONNX Runtime or
  OpenVINO detection, BoxMOT tracking, sighting aggregation, and optional
  MobileCLIP embedding/classification. It also supports a scripted `--mock`
  engine and the `tomer`/`both` prioritizer modes. Models and runtime dependencies
  are provisioned separately; see [sidecar setup](../edgeml/sidecar/README.md).

Stateful clients send `source` and `capturedAtUnixMs` so tracks stay separate
per camera. `AnalyzeResponse` can include sighting lifecycle events and an
image-priority score in addition to the stateless fields above. The Go edge
uses `-dedup sightings` for object-level items or `-prioritizer tomer` for the
integrated image-level score; transfer selection remains in the Go scheduler.

Wire format for JSON: `POST /ghostseeker.v1.InferenceService/Analyze` with
`{"image": "<base64>", "knownLabels": [...], "wantEmbedding": true}`.
