# Product and research boundaries

The live demo in `deploy/compose/demo.compose.yml` defines the current product
boundary. Product source stays at its existing Go import paths and Python
`edgeml` runtime imports. The root README is the product entry point;
`research/README.md` is the experiment entry point.

## Migration map

| Before | Now |
|---|---|
| Root README's detailed technical reference | `docs/REFERENCE.md` |
| `cmd/{sim,bench,retune,piv-testpki}` | `research/cmd/{sim,bench,retune,piv-testpki}` |
| `internal/{bench,retune}` | `research/internal/{bench,retune}` |
| `edgeml/{reports,configs,eval_qa,scripts,tools,yolov8n.pt}` | Same suffix under `research/edgeml/` |
| Offline `edgeml` Python helpers | `research/edgeml/src/edgeml_research/`; see its README for imports |
| Offline ML tests | `research/edgeml/tests/`; runtime tests stay in `edgeml/tests/` |
| `datasets/`, `frames/`, `ui-mockup/` | Same directory under `research/` |
| `docs/reports/` | `research/reports/` |
| `inference/README.md`, `inference/stub/` | `docs/INFERENCE.md`, `research/inference/stub/` |
| `tools/imagegen/` | `research/tools/imagegen/` |
| `scenarios/sailboats_example.json` | `research/scenarios/sailboats_example.json` |

`make build` now builds only product commands. Use `make build-research` or
`make build-all` for the relocated tools. Existing `make sim`, `make demo`,
and `make demo-compare` shortcuts build the simulator themselves.
`make compose-demo` and the four `make demo-{sidecar,shore,ui,edge}` service
commands retain their entry points.

## Shared code and assets

- `algorithm/ghost_priority` stays in the product tree because the demo's
  `both` prioritizer imports it. Its offline CLI and tests remain alongside
  that shared library.
- `internal/catalog` and `web/static` include synthetic/demo assets actually
  used by the product. Their generation scripts are research tooling.
- The live replay scenario and clip downloader remain under
  `scenarios/ghostfleet_demo`. Its optional comparison harness uses the
  research simulator; the live service does not.
- `edgeml` retains reusable detector adapters and contracts. Evaluation-only
  modules live in the separately installed `edgeml_research` package.
- Tests belong beside the code they verify, regardless of language.

## Keeping the boundary clear

Product Go packages may not import `research/`; product Python packages may
not import `edgeml_research`. `make check-layout` verifies these rules, Docker
COPY inputs, and the live demo's bind mounts. Go and sidecar Docker contexts
use allowlists, so new experiment files do not silently enter product builds.

New experiments belong in `research/`. Promoting one into the product requires
runtime tests, explicit dependencies, and any needed container/runbook updates.
Keep downloaded frames, model exports, and run outputs out of Git. Existing
LFS archives and the research weight snapshot are preserved; no history was
rewritten. GCS object names and historical result identifiers are unchanged.

## Release branches

The repository also has `dev`, `demo`, and `prod` branches. Current feature work
integrates through `dev`; this layout cleanup targets that branch as well.
Folder names describe the purpose of code, while branches identify versions
selected for development, demonstration, or production. Promote reviewed commits
through the existing branch workflow; a folder move does not promote a release.
All branches can retain the research history while container build contexts ship
only product inputs. No branch protection, default-branch setting, or release
branch is changed by this reorganization.
