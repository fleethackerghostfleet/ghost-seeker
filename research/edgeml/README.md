# Edge ML research

Offline experimentation is separate from the [runtime sidecar](../../edgeml/README.md).
Run commands from the repository root unless a script says otherwise.

```sh
pip install -e 'edgeml[test]' -e research/edgeml
python -m pytest -q research/edgeml/tests
python research/edgeml/tools/validate_data.py --help
python -m edgeml_research.runtime.export --help
```

| Path | Role |
|---|---|
| `src/edgeml_research/data/` | Dataset loaders, catalogs, and conversions |
| `src/edgeml_research/eval/` | Detection, tracking, sighting, and delivery metrics |
| `src/edgeml_research/runtime/` | Offline export, benchmarking, frame gating, and reference runners |
| `src/edgeml_research/embed/`, `detectors/` | Ship-type experiments and hosted bakeoff adapters |
| `src/edgeml_research/{pipeline,forms,select,stabilize}.py` | Reference pipeline and evaluation helpers |
| `tools/`, `scripts/` | Data prep, labeling, training, evaluation, and report generation |
| `configs/`, `eval_qa/` | Split definitions, experiment settings, and annotations |
| `reports/` | Saved results, figures, and historical findings |
| `tests/` | Tests for the offline helpers and tools |
| `yolov8n.pt` | Preserved research weight snapshot; not a demo model |

Optional dependencies are selected here: `research/edgeml[eval]`,
`research/edgeml[audit]`, or `research/edgeml[gpu]`. Install `edgeml` from the
same checkout first (or in the same pip command). Tracking and runtime model
extras still belong to `edgeml`.

Imports formerly under `edgeml.data`, `edgeml.eval`, the offline
`edgeml.runtime.*` runners, `edgeml.embed.shiptype`, and
`edgeml.detectors.bakeoff` now use `edgeml_research` with the same suffix.
Shared runtime types and adapters continue to import from `edgeml`.
Research uses an editable installation so loaders can resolve the checked-in
`configs/splits.yaml` adjacent to this package. GCS prefixes remain unchanged.
