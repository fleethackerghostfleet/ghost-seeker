# Research and experiments

This is the workspace for offline experiments, development utilities, datasets,
model evaluation, and historical reports. The supported live demo is documented
in the [root README](../README.md) and [demo runbook](../docs/DEMO.md).

| Area | Contents |
|---|---|
| [`edgeml/`](edgeml/README.md) | ML experiments, evaluation/export package, reports, annotations, configs, scripts, and tests |
| [`cmd/`](cmd/) | Go `sim`, `bench`, `retune`, and development-only `piv-testpki` commands |
| [`internal/`](internal/) | Benchmark oracle and offline retuning implementation |
| [`datasets/`](datasets/README.md) | Dataset registry and ingestion tooling; GCS object names are unchanged |
| [`frames/`](frames/README.md) | Original Git LFS frame archives and their provenance manifest |
| [`scenarios/`](scenarios/) | Experimental scenario templates |
| [`reports/`](reports/) | Historical integration and engineering measurements |
| [`inference/stub/`](inference/stub/) | Model-free Python service sketch |
| [`ui-mockup/`](ui-mockup/) | Standalone UI design prototype |
| [`tools/imagegen/`](tools/imagegen/README.md) | Synthetic imagery generation and annotation utilities |

## Run research from the repository root

```sh
make build-research
make sim
make demo-compare                       # headless replay; needs demo clips
# Or run an individual tool:
go run ./research/cmd/bench
go run ./research/cmd/retune -demo data/demo-reviews.jsonl

pip install -e 'edgeml[test]' -e research/edgeml
make test-research
```

The `algorithm/ghost_priority` package remains at the root because the live
sidecar uses its scorer. Its [offline CLI](../algorithm/README.md) remains
available with `PYTHONPATH=algorithm python -m ghost_priority`.
The headless comparison harness remains next to the shared live replay scenario
under `scenarios/ghostfleet_demo/`.

Research code can depend on the product packages. A product build or runtime
must not depend on this directory. Promote reusable code into the product area
with runtime tests and deployment changes when it becomes part of the demo.

Reports record the experiment and code layout at the time they were written;
old commands inside them may need the [migration map](../docs/REPOSITORY.md).
No reports, model snapshots, archives, or dataset provenance were discarded.
