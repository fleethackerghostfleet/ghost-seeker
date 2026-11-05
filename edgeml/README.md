# Edge ML runtime

This directory is the Python runtime shipped in the Ghost Seeker sidecar.
The live demo runs detection, tracking, classification, sighting aggregation,
night-light interpretation, and optional `ghost_priority` image scoring here.
The Go edge owns storage and transfer scheduling.

- [Sidecar setup and API](sidecar/README.md)
- [Live demo runbook](../docs/DEMO.md)
- [Interfaces and data contracts](CONTRACTS.md)
- [Offline ML research](../research/edgeml/README.md)

## Runtime source

| Path | Purpose |
|---|---|
| `src/edgeml/sidecar/` | HTTP service, live pipeline, and Tomer scoring adapter |
| `src/edgeml/runtime/` | CPU detector sessions, backends, and thread controls |
| `src/edgeml/tracking/` | Track adapters, realness, and sighting aggregation |
| `src/edgeml/embed/` | Embeddings and zero-shot classification |
| `src/edgeml/nightlights/` | Light interpretation and burst decisions |
| `src/edgeml/detectors/` | Reusable detector adapters and label utilities |
| `src/edgeml/types.py`, `interfaces.py` | Shared contracts |
| `tests/` | Runtime regression tests, including the sidecar's mock HTTP service |

Some adapters are optional; the live demo selects its models in
[`demo.compose.yml`](../deploy/compose/demo.compose.yml). The package contains
no model weights. Models are mounted separately. The `both` prioritizer uses
`algorithm/ghost_priority`, and night-light rules come from `internal/catalog`;
both mounts are retained by the demo Compose stack.

```sh
pip install -e 'edgeml[test]'                # from the repository root
python -m pytest -q edgeml/tests
pip install -e 'edgeml[tracking]' onnxruntime # live-model dependencies
make demo-sidecar
```

Offline loaders, evaluation metrics, export/benchmark runners, scripts,
reports, configs, annotations, and their tests now live in
`research/edgeml/`. Install that package separately to use the
`edgeml_research` namespace. The sidecar image installs only `edgeml`.
