# Fixture harness isolation

The Go replay harness is compiled only with the `research_replay` build tag,
after `go_replay/run.py` copies it into the pinned snapshot's scheduler package.
The runner passes that tag automatically. It is not a standalone Go package
and is excluded from ordinary repository checks; raw private fixtures are still
required for the research replay.

# Edge image prioritization research

Research tools and evidence for prioritizing maritime images of interest under limited edge compute and communications. The findings support retaining the existing similarity baseline while evaluating context-preserving representations with selective detail. They do not establish operational Navy utility, recognition performance, or a production scheduling policy.

Start with [phase 2 findings](docs/phase2-findings.md) and [phase 1 findings](docs/phase1-findings.md). This directory contains research code only; it does not change production defaults. The delivery base is `1a6382ed554c63466c6fc34fafa1bbf0bb70b659`; the phase 1 algorithm source matches the original `a9c2de09f24dc242d274b507c4eeb7110d14db79` study baseline. The current Go replay is pinned separately to the delivery base.

| Directory | Contents |
| --- | --- |
| `phase1/` | Candidate similarity methods, benchmark evaluator, exact pruning and annotation sensitivity checks |
| `phase2/representation/` | Real JPEG forms, predicted crops, geometry and byte accounting |
| `phase2/transport/` | Causal whole-packet, chunk and completion-bias models with tests |
| `phase2/run_study.py` | Eight methods, two batch sizes, four link rates and four schedules |
| `go_replay/` | Isolated tracked-source snapshot runner and actual Go service replay tests |
| `evidence/` | Numeric summaries, independent audit results, source locks and delivery validation |
| `figures/` | Aggregate scientific figures containing no source imagery |
| `docs/` | Reviewed findings, assumptions, private artifact mapping and cloud environment guide |

## Local checks

Use Python 3.11 or newer in a disposable environment. The dependency versions reproduce the study environment. Run from the repository root:

```bash
python3 -m venv .venv-research
.venv-research/bin/pip install -r research/edge-image-prioritization/requirements.txt
.venv-research/bin/python research/edge-image-prioritization/test.py
```

The 72 focused Python tests require no private images, detector model or cloud access. They cover ranking/parity, lock validation, annotation semantics, geometry, JPEG accounting, causal availability, capacity integration, receipt deadlines, dependencies, resumable outages and chunk competition. The portable package was also checked against private inputs: all 2,304 causal runs reproduced the original run, metric and summary JSON exactly. Validation records are in `evidence/delivery-validation.json`.

## Reproduce with authorized private inputs

Obtain inputs using the [artifact guide](docs/artifacts.md), preserving licenses and hashes. Keep data and output directories outside this tracked package. `DATA` below is the extracted `data/` directory; `MODEL` is the preserved NanoDet ONNX file; `CACHE` is the frozen development NanoDet benchmark directory from the phase 1 archive. These variables deliberately have no personal-machine defaults.

```bash
RESEARCH=research/edge-image-prioritization
python "$RESEARCH/phase1/evaluation/run_benchmark.py" \
  --data-root "$DATA" --baseline-path algorithm \
  --candidate-module "$RESEARCH/phase1/candidates/candidate_methods.py" \
  --split dev --out "$NEW_DEV"
python "$RESEARCH/phase1/evaluation/run_benchmark.py" \
  --data-root "$DATA" --baseline-path algorithm \
  --candidate-module "$RESEARCH/phase1/candidates/candidate_methods.py" \
  --split test --lock "$RESEARCH/evidence/phase1/locks/default.json" \
  --out "$NEW_TEST"
```

For the detector experiment, add `--model-path "$MODEL"` and use `locks/nanodet.json` for the held-out lock. Reproduction of completed held-out evaluation is not a fresh validation set for tuning. Future tuned methods need a new independent evaluation design.

```bash
python "$RESEARCH/phase2/representation/encode_forms.py" \
  --records "$CACHE/records.json" --data-root "$DATA" --out "$FORMS"
python "$RESEARCH/phase2/run_study.py" \
  --data-root "$DATA" --forms-dir "$FORMS" --cache-dir "$CACHE" \
  --baseline-path algorithm --out "$CAUSAL"
python "$RESEARCH/phase2/run_chunks.py" --source "$CAUSAL" --out "$CHUNKS"
python "$RESEARCH/phase2/run_completion.py" \
  --source "$CAUSAL" --chunks "$CHUNKS" --out "$COMPLETION"
```

Every output path must be new. Phase 2 verifies input, annotation, encoded-form and cached-baseline hashes before running. It uses only development sources. Annotation information enters evaluation after packet selection and simulation. Chunk scripts produce receipt/accounting logs; geometric scoring of those logs and the separate Singapore challenge are preserved in the full private archive, not replaced by packet-count claims. The checked-in condensed geometry summaries are explicitly filtered to constant links, one-second batches, all/complete annotation status, and 0/16px detail thresholds; the full scenario matrix remains external.

The Python link model has zero initial credit, no capacity borrowing, actual sequence horizons and complete-payload receipt. Its reliable byte resume, 64-byte overhead and fixed propagation are assumptions. See [transport assumptions](docs/transport-model.md), [chunk findings](docs/chunk-findings.md), and [completion ablation](docs/completion-findings.md). TTL is a latest-start rule anchored to queue arrival, not a guarantee of fresh receipt. The phase 2 benchmark assumes zero compute time plus explicit batch wait; CPU measurements are a separate experiment.

## Replay the actual Go implementation

Use an existing Go 1.23.4 executable and the private fixture directory containing `fixtures.json` plus referenced JPEGs:

```bash
python "$RESEARCH/go_replay/run.py" --repo . \
  --revision 1a6382ed554c63466c6fc34fafa1bbf0bb70b659 \
  --fixtures "$GO_FIXTURES" --out "$GO_OUTPUT" --go go
```

The runner copies tracked Go inputs into a new scratch snapshot, adds research tests, records all source hashes and uses isolated Go caches. It never edits the source checkout. Dependency download access may be needed. One existing link-package test binds a localhost HTTP listener; execution environments must permit that test. The four replay scenarios and one scene-gated full-pull control use actual scheduler/store/link/shore APIs with virtual timing. They do not validate the HTTP authentication middleware or represent physical network measurements. See [current Go findings](docs/go-current-replay.md).

## Provenance and private artifacts

The full private archive lives under `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/research/codex/payload-20261105-acc4/artifacts/`. See [exact original output names and archive mapping](docs/artifacts.md). JPEGs, models, annotations, operator answers, raw logs, cloud credentials, toolchains and bulk snapshots are excluded from Git. `evidence/source-study-manifest.json` maps original research sources to portable copies; historical execution locks remain unchanged. `MANIFEST.sha256` covers this delivery package except itself.
