# Ghost Seeker

Ghost Seeker's current product is the containerized live demo: camera replay →
edge ML sidecar → edge storage and prioritization → constrained link → shore →
operator console. **Start with [the demo runbook](docs/DEMO.md).**

The product source stays in the established `cmd/`, `edgeml/`, and supporting
paths below. **Experiments, offline evaluations, and research tooling live in
[`research/`](research/README.md).**

## Run the product demo

From the repository root, with Docker Compose installed and access to the demo
clips and exported models:

```sh
make demo-clips demo-models   # one-time download; needs gcloud access to the team bucket
make compose-demo            # build and start the live stack
# Open http://localhost:8090
make compose-demo-logs       # follow logs
make compose-demo-down       # stop and delete demo data and credentials
```

If the assets are already available, skip the downloads and set
`MODELS=/path/to/D` when starting the demo. Clips belong in
`scenarios/ghostfleet_demo/frames/`. Both are external assets, not build inputs.
The runbook also covers running the same services in four local terminals.

The demo uses generated development credentials and localhost port bindings.
“Product” identifies the supported demo code; deployment security requirements
are documented in [SECURITY.md](docs/SECURITY.md) and [FIELDING.md](docs/FIELDING.md).

## What belongs to the product?

| Path | Role in the demo/product |
|---|---|
| [`cmd/`](cmd/README.md) | Edge, shore, operator app, and credential utilities |
| [`edge/`](edge/) and [`shore/`](shore/) | Ingestion, scoring, queues, storage, and synchronization |
| [`internal/`](internal/) | Shared Go services, policies, auth, catalog, and operator logic |
| [`web/`](web/) | Embedded operator UI and its tests |
| [`edgeml/`](edgeml/README.md) | Installable ML runtime, sidecar, and runtime tests |
| [`algorithm/`](algorithm/README.md) | Shared `ghost_priority` scoring library used by the demo's `--prioritizer both`; also has an offline CLI |
| [`proto/`](proto/) and [`gen/`](gen/) | Service contracts and generated Go bindings |
| [`deploy/`](deploy/README.md) | Docker images, live demo Compose stack, alternate topology, and Zarf packaging |
| [`scenarios/ghostfleet_demo/`](scenarios/ghostfleet_demo/README.md) | Live replay scenario and clip downloader; optional headless comparison harness |
| [`ui/`](ui/README.md) | Edge/shore static-console placeholder; the operator UI is in `web/` |
| [`docs/`](docs/README.md) and [`tools/`](tools/) | Product documentation and repository verification |

Tests stay beside the code they validate. Research may import product code;
product code must not import `research/` or `edgeml_research`. Product Docker
build contexts explicitly exclude research. Shared libraries remain here even
when experiments also use them.

## Development entry points

```sh
make help
make build             # product binaries only, in bin/
make build-research    # simulator, benchmark, retuner, test PKI
make build-all         # both groups

python3 -m venv .venv
. .venv/bin/activate
pip install -e 'edgeml[test]' -e research/edgeml
make test-product      # Go, web, runtime Python, and dependency-boundary checks
make test-research     # offline Go/Python and algorithm tests
make verify            # complete clean-commit verification; see docs/VERIFY.md
```

Go 1.26.10+, Node 22+, and Python 3.10+ are required for the relevant local
checks. `make sim`, `make demo`, and `make demo-compare` are offline research
comparisons; `make compose-demo` launches the live product demo.

See the [repository map and migration notes](docs/REPOSITORY.md),
[detailed technical reference](docs/REFERENCE.md), and
[research guide](research/README.md). GitHub Actions remains manual-only; use
[local verification](docs/VERIFY.md) without consuming hosted Actions minutes.
