# Private artifact guide

The Git package holds reusable code, compact numeric evidence and findings. The broader authorized research archive is private at:

```text
gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/research/codex/payload-20261105-acc4/artifacts/
```

Use existing authorized Google Cloud credentials to list the prefix and retrieve the archive inventory and checksum manifest. No credentials are stored here. Object names and generations in that inventory are authoritative; do not infer a final bundle name from this guide. Verify SHA-256 before extracting or rerunning. The repository `MANIFEST.sha256` covers this sanitized Git delivery; the private archive has its own manifest.

## Phase 1

Original user-facing outputs were `outputs/research/`, `outputs/ghost-seeker-research.zip`, `outputs/research-inputs.zip`, and `outputs/ghost-seeker-gcp.zip`. The report ZIP includes its `report.md`, `README.md`, `gallery/`, `analysis/`, `source/` and `evidence/` trees. References in the repository phase 1 findings map to those archive paths:

| Original package path | Purpose |
| --- | --- |
| `gallery/index.html` and `figures/decision-summary.png` | Private inspection gallery and decision figure |
| `source/literature/report.md`, `source/literature/sources.md` | Full independent literature review and primary-source ledger |
| `source/evaluation/test-review.md` | Held-out review and source/status caveats |
| `source/candidates/cloud-cost-review.md` | Detailed cost and pruning review |
| `evidence/results/002-payload-audit/` | Actual encoded image examples and payload measurements |
| `evidence/local-input-snapshot.json`, `evidence/cleanup.json` | Input preservation and resource cleanup records |
| `evidence/results/005-dev-nanodet/benchmark/` | Frozen NanoDet development cache used for phase 2 (`CACHE`) |

`research-inputs.zip` contains the exact sampled inputs and runtime workload, not the full source corpus. Its recorded SHA-256 is `183d226524dc585fc706525445b251e61c195b305c518920bbe6988b5cbe2115`. Preserve its internal-use licenses and source/annotation provenance. The original `ghost-seeker-gcp.zip` contains the reusable environment skill, not credentials. The repository [environment guide](gcp-environment.md) is a sanitized companion.

## Phase 2

Original user-facing output was `outputs/research-phase2/`. Its `report.md`, `README.md`, `figures/` and unfilled `operator-review/index.html` provide the complete local presentation. Private image variants, raw annotations, operator responses and logs remain external. The repository edition deliberately excludes personal operator feedback.

The private working archive preserves the following source-relative directories. The final archive inventory maps them to object names:

| Original research path | Purpose |
| --- | --- |
| `research-phase2/inputs/data/` | Frozen phase 1 development inputs |
| `research-phase2/representation/dev-forms/` | Actual encoded JPEG forms and form metadata (`FORMS`) |
| `research-phase2/results/causal-v1/` | 2,304 original runs, packet sets, receipt logs, geometry metrics and locks |
| `research-phase2/transport/chunkexperiments/results/` | 432 chunk-size runs |
| `research-phase2/transport/chunkexperiments/results-completion/` | 144 completion-bias runs |
| `research-phase2/results/chunk-geometry/` | Post-simulation geometric evaluation of chunk receipts |
| `research-phase2/go-replay/fixtures/` | Two-image encoded Go fixtures and metadata (`GO_FIXTURES`) |
| `research-phase2/go-replay/` | Historical/current source hash locks, Go replay results and independent audits |
| `research-phase2/cpu-profile/results/` | Hardware record and actual 12-minute CPU workload logs |
| `research-phase2/review/` | Independent arithmetic, causal, annotation and interpretation checks |

Source images and models remain private. Their hashes support identity and reproducibility; inclusion of hashes here does not grant redistribution rights. Do not tune new policies against the already-observed held-out set or treat the 100-image training challenge as independent held-out evidence.
