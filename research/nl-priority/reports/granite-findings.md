# Granite research findings

The local research stack meets the measured native-Mac latency and memory targets, but **does not yet meet the 95% language-quality gate**. Production promotion is not supported by these results.

## Pinned runtime

- Model: IBM `granite-4.0-350m-GGUF`, `granite-4.0-350m-Q4_K_M.gguf`.
- Model revision: `b8208a86a58427e1739265318028eb5895b74bf2`.
- Artifact SHA-256: `771c588a49607f274a2bba3185733607ebe6f74b996ab90e2d6bee0d98bcec52` (236,985,760 bytes).
- llama.cpp revision: `45b6604b20f17b7d7638042306c0cd1b6af5999c`.
- CPU-only native build; Metal, BLAS, and Accelerate disabled. Generation and prefill each use two threads; one slot; 2,048-token context; batch/ubatch 128; GPU layers zero; prompt-cache RAM zero.
- Machine: Apple M2, 16 GiB RAM, eight logical CPUs. Other implementation and evaluation work ran concurrently; results are not an otherwise-idle-device certification.
- Process-launch-to-health cold start: **0.497 seconds**, excluding model download. This is a single native measurement with model files already on local disk, not a cold-disk percentile.

The model was downloaded directly from IBM's official Hugging Face repository and verified before activation. No cloud inference, paid GitHub feature, or substitute language model was used.

## Language quality

| Evaluation | Supported commands | Unsupported requests rejected | Interpretation |
|---|---:|---:|---|
| Initial 120-case corpus |63/95 (66.3%)|21/25 (84%)|Diagnostic first run; `granite-first-evaluation.json`|
| Fresh 120-case holdout |**90/100 (90%)**|**18/20 (90%)**|First unseen run after prompt revision; `granite-heldout-v2.json`|
| Post-guard regression |89/100 (89%)|20/20 (100%)|Intermediate guards; `granite-postguard-regression.json`|
| Final regression |**87/100 (87%)**|**20/20 (100%)**|Final protection/polarity guards; `granite-final-regression.json`|

The first schema placed a free-text clarification before operations and elicited generic chatbot refusals. A smaller operations-only schema, empty operations for clarification, and explicit Input/Output examples greatly improved extraction. Host validation remains necessary: constrained JSON guarantees structure, not intent correctness.

On the fresh holdout, paraphrases scored 24/32 and compounds 16/16. Remaining failures included interpreting lower priority as a boost, changing emphasis strength, and plural/singular removal mismatches. Two unrelated statements were incorrectly treated as instructions. Subsequent guards reject unrelated requests and negative-direction reversals; they also ask for clarification on some valid phrasing. The final regression scored 87%, so the 95% target remains failed. Its 100% rejection result applies to these 20 published cases, not all possible unsupported requests.

Both corpora have hand-authored expected outcomes, immutable recorded hashes, and per-case actual responses. They are bounded engineering evaluations, not statistically representative field estimates. The model was not fine-tuned on either corpus. Novel-label vision quality is evaluated separately; these command tests do not establish visual recognition accuracy. Per-path scores count only successful responses on that path and must not replace the full supported-command score.

## Complete-preview resource measurement

The test used an isolated backend and **1,000 entries made by repeating 45 real crop embeddings with unique IDs**. This is a synthetic scale test of real embedding data, not 1,000 independent images or a recognition-quality dataset. Three warmup requests preceded 30 distinct new-concept requests through Granite, matching MobileCLIP text encoding, classification, production scoring, and production scheduling. The UI's queue was untouched.

| Metric | Result | Target |
|---|---:|---:|
| Warm command-to-preview p50 |0.402 s|—|
| Warm command-to-preview p95 |**0.457 s**|≤5 s|
| Sampled aggregate peak RSS |**758,890,496 bytes**|≤1,000,000,000 bytes|
| Research backend individual peak RSS |172,638,208 bytes|Included above|
| Vision service individual peak RSS |314,802,176 bytes|Included above|
| Granite individual peak RSS |272,154,624 bytes|Included above|

RSS was sampled every 50 ms across the three actual processes. The sum of individual maxima can differ from the simultaneous aggregate maximum. Native macOS can compress/page memory; RSS is not a container memory-limit proof. Docker/VM overhead, Linux container execution, and vision-loop throughput regression were **not measured** on this machine. The FP32 text encoder was used because the vision quantization experiment did not meet its parity threshold. See `granite-runtime-1000.json` for samples and timings.

The resource benchmark predates final admission/polarity guard changes; scoring, scheduling, and vision execution are unchanged. `granite-provenance.json` records separate benchmark/final binary hashes and final compiler file hashes for `granite-final-regression.json`. This distinction prevents attributing earlier measurements to an unmeasured binary.

## Reproduction

1. Use `tools/fetch_granite.py` to fetch and verify the pinned model. On Python installations without a configured CA bundle, set `SSL_CERT_FILE` to the operating system CA bundle; do not disable TLS verification.
2. Run the documented research stack. Execute `tools/eval_commands.py --url URL --corpus eval/heldout-v2.json --label regression --output REPORT.json`. Repeating the published holdout is a regression run.
3. Use `tools/scale_queue.py INPUT.json OUTPUT.json --count 1000` to prepare a labeled synthetic scale queue, then ingest it into an isolated backend. Run `tools/benchmark.py --url URL --payload eval/benchmark-commands.json --pid BACKEND_PID --pid VISION_PID --pid GRANITE_PID --queue-size 1000 --output REPORT.json`. The script checks the actual queue size. Distinct command bodies measure new concepts; repeated commands measure cache hits.
4. Run `python -m unittest discover -s tools -p test_tools.py` for artifact-integrity and evaluation-tool checks.

The research demo should retain Preview → Apply and explicit explanations. A larger untouched command set, stronger intent validation or task-specific tuning, and container measurements are required before claiming the original acceptance gates are satisfied.
