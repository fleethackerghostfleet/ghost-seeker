# Research findings — 2026-11-05

The prototype works locally with real IBM Granite and Apple MobileCLIP models,
real detector crops, and the production scorer/scheduler. **It is not ready for
production promotion: language accuracy remains below the requested 95% target,
and new-label accuracy has not been established.**

## What was measured

| Check | Result | Interpretation |
|---|---|---|
| Granite first command set |63/95 supported;21/25 unsupported correctly rejected|Initial prompt was insufficient; report retained|
| Fresh 120-case set after prompt changes |90/100 supported;18/20 unsupported correctly rejected|95% language gate failed; later changes are regression fixes, not a new held-out result|
| Final compiler regression on published set |87/100 supported;20/20 unsupported correctly rejected|Final guards trade broader phrasing for clarification; quality gate still failed|
| 1,000-item warm preview,30 distinct commands |p95 **0.457s**; all requests succeeded|Below 5s on the native development Mac|
| Sampled aggregate backend + Granite + vision peak RSS |**758,890,496 bytes** (~759MB)|Below 1GB in this run; includes actual FP32 text encoder|
| Granite process-to-health cold start |0.497s|Native process launch only; excludes download, export, container build and Docker VM startup|
| MobileCLIP tokenizer |Exact match for 50 prompts|Standalone runtime tokenizer agrees with reference|
| Exported text/image FP32 cosine agreement |0.99999988 /0.99999994 minimum|Numerical export parity passed|
| INT8 text parity |0.811 minimum /0.956 mean cosine|Failed 0.98/0.995 gate; FP32 selected|
| Actual detected crops |45 encoded and imported; no novel labels accepted in smoke test|Conservative abstention; not evidence of useful novel-label recall|

Resource measurements used macOS 26.6.2 on arm64, native CPU processes, two model
threads, and a 2,048-token Granite context. The 1,000 records repeat 45 real crop
embeddings; that is a scale test, **not 1,000 independent labeled examples**. Each
measured request introduced a distinct concept so text encoding was included.
RSS was sampled while requests ran; idle macOS RSS can hide compressed or paged
weights. This is an observed process-memory result, not a guarantee for all
hardware or workloads. Full streaming detector throughput and Docker/VM overhead
were not measured.

## Correctness verification

- Go research backend tests and race checks cover production scorer parity,
  actual scheduler ordering in both modes, unknown/human protections, preview
  isolation, failed persistence, restart/undo, stale previews, draft follow-ups,
  new arrivals, incompatible encoders and HTTP failures.
- 18 Python tests cover vision validation, tokenizer behavior, abstention,
  malformed requests, stateless recovery, model download integrity and evaluator
  accounting. Existing production/research dependency-boundary checks pass.
- Browser testing imported 45 real crops, compared before/after rankings, applied
  a follow-up to an unapplied draft, and restored the baseline through previewed
  undo. Integration review added guards for unsupported human-override commands
  and disabled editing while a preview is running.
- Compose YAML, Dockerfile input paths and explicit context allowlists were
  checked. Docker is unavailable on this host, so image builds and Compose
  startup remain unverified. All three services ran natively on the Mac.

## Remaining promotion gates

1. Reach 95% supported-command accuracy and reliable rejection on a fresh,
   independent set after the final compiler is frozen. Constrained JSON does not
   guarantee semantic correctness; review the preview readback.
2. Annotate a representative new-concept development/held-out crop set. Tune
   thresholds only on development data and report per-concept precision, recall
   and abstention on held-out data. The available smoke data does not establish
   ground truth for rescue boats, rafts or other new concepts.
3. Measure the full streaming vision loop and containerized deployment on target
   edge hardware while commands execute. Preserve the 1 GB/5s targets.

Raw reports, including failures, are in [reports/](reports/). Export numerical
checks are in [vision/export-validation.json](vision/export-validation.json).
The first and fresh evaluations remain distinct from subsequent regression runs.

## Follow-up: semantic category resolution

A reported no-op on `prioritize fishing vessels` exposed a fast-parser bug: the
phrase became a novel `fishing vessel` concept instead of matching the existing
`fishing boat` subtype. Simple command operations now also ask Granite to resolve
unfamiliar category names. The model selects from the supplied vocabulary or
retains the original phrase. No fishing-vessel alias table is used. The UI exposes
the interpretation and an explicit count of changed priorities; unmatched rules
and unchanged previews produce warnings.

The resolver development probe passed **14/15** targeted cases using the base
subtype catalog. It recognized fishing vessel(s), retained red fishing boat,
rescue boat and fishing net, but incorrectly dropped `small` from `small fishing
vessel`. This is a known semantic limitation and **not a fresh held-out accuracy
result**. The exact prompt, candidate catalog, schema and raw outputs are retained
in [the targeted report](reports/granite-semantic-resolution-targeted.json).

The resource benchmark above predates this added resolution step and is retained
as historical evidence; it must not be presented as acceptance of the changed
compiler. A semantic resolution runs once per unfamiliar concept at command
time, never per image, with a shared five-second deadline. Failed model requests
cannot activate partial changes.

End-to-end verification with the actual running Granite service and 45 real
crops confirmed `prioritize fishing vessels` resolves to `fishing boat`: EdgeML
rank **12 → 1**, priority **2.072 → 5.945**, with only that image's priority
changed. The same category boost works through Tomer (rank **13 → 1** against
its own baseline), although these imported records lack Tomer values and use
its documented fallback. Recorded warm preview times for the exact phrase were
**73 ms** in each mode. Red fishing boat and rescue boat remained new concepts;
the UI correctly reported no matching images. The active state file was unchanged.
See [the live smoke report](reports/semantic-resolution-smoke.json). Browser
verification also confirmed Granite's interpretation, one changed image, and an
enabled Apply button; the proposed policy was left unapplied.
