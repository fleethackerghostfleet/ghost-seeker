> Repository edition. The private inspection images and detailed evidence are mapped in [the artifact guide](artifacts.md).

# Ghost Seeker: maritime image prioritization at the edge

**Keep the current color+pHash hybrid for now.** None of the three new similarity methods demonstrated a clear benefit worth replacing it. The more promising next implementation is a payload choice that fits the link while preserving useful object detail, evaluated against what an operator actually receives. Smaller JPEGs helped in development, but recognition and operational usefulness still need validation.

Fifteen isolated cloud jobs completed successfully, including the original comparison and additional payload, policy, runtime, and preservation experiments. Production code and shared data were unchanged. Devin merged [PR #29](https://github.com/fleethackerghostfleet/ghost-seeker/pull/29) at **9:39 p.m. New York time on November 4**. Its `algorithm/` tree, and the subsequently observed main commit, exactly match the research baseline. That study used an isolated checkout; this package is a later, separate research-only delivery.

[Decision figure](artifacts.md#phase-1) · [Private inspection gallery](artifacts.md#phase-1) · [Reproduction instructions](artifacts.md#phase-1) · [Complete review and sources](artifacts.md#phase-1)

## The result that matters

The end use is useful maritime imagery reaching a Navy operator over a constrained link. Pixel change, a detector prediction, and mission importance are different things. The experiment therefore measures selected annotated appearances, actual JPEG bytes, CPU cost, and failure cases separately. It does not establish network receipt, human recognition, threat identification, or live time-to-report.

The clearest held-out comparison uses the six source recordings marked **complete** in the existing annotations. At 64 kbps, each nominal 60-second pool has 480,000 bytes. The denominator is 23 annotated track-window appearances and 12 sequence-local track IDs, not 23 distinct vessels.

| Object evidence / method | Annotated track-windows retained | Annotated tracks retained at least once |
|---|---:|---:|
| No detector / current hybrid | 22/23 | 12/12 |
| No detector / tiled hash | 22/23 | 12/12 |
| No detector / local color or registered residual | 21/23 | 12/12 |
| NanoDet / current hybrid | **23/23** | **12/12** |
| NanoDet / tiled hash | 22/23 | 12/12 |
| NanoDet / local color or registered residual | 19/23 | 11/12 |
| NanoDet / no-similarity control | 19/23 | 11/12 |
| NanoDet / uniform control | 20/23 | 11/12 |

These are small, annotation-conditioned results. “Complete” still has documented exclusions and interpolated boxes. They support retaining the existing method; they do not certify operational accuracy or make NanoDet a validated maritime detector.

Across all annotation statuses, the hybrid retained 49/64 track-windows without the detector and 50/64 with it, versus 39–42/64 for the new methods at this budget. **Those aggregate counts are diagnostic only.** Most apparent losses occur in a partial tender sequence whose QA explicitly excludes it from headline recall because boat identities fragment across cuts. Its many IDs cannot be counted as distinct boats. Raw results remain available; the report does not hide the problematic population.

The residual methods often behaved like removing redundancy suppression: on development data, local color and registration produced exactly the no-similarity selections in every tested pool/budget. On held-out data their selected positive sets matched that control in every 64-kbps pool. Some selected timestamps clustered closely while the hybrid spread selections across more of the window. This explains the observed behavior more convincingly than an assumption that greater pixel sensitivity equals useful novelty. See [independent held-out review](artifacts.md#phase-1).

## Why payloads deserve attention first

At 9.6 kbps, the nominal minute allows **72,000 bytes**. Among 273 development frames, only **30 (11.0%)** fit individually under the existing 1280-pixel, quality-80 JPEG setting. A ranker cannot select an oversized whole image. Lowering JPEG quality preserved dimensions and improved fit; lowering dimensions made every sampled frame fit but reduced object detail.

| Payload, longest side / JPEG quality | Median bytes | Frames individually fitting 72 KB | Hybrid retained track-windows at 9.6 kbps, no detector / NanoDet |
|---|---:|---:|---:|
| 1280 / 80 | 90,293 | 11.0% | 2/15 · 2/15 |
| 1280 / 50 | 58,834 | 81.3% | 5/15 · 6/15 |
| 640 / 80 | 29,694 | 100% | 7/15 · 8/15 |

These **development-only representation experiments were planned and recorded before held-out outcomes were inspected**, then executed as follow-ups. They change encoded payloads while holding image evidence and ranking rules fixed. The all-status counts above have the same annotation caveats. On the much smaller complete-status development slice—only two positively annotated recordings—the corresponding retention is 0/4, 1/4, and 2/4 for both providers. No held-out payload improvement is claimed.

Byte savings are not free. At 640 pixels, 15 of 88 annotated frame-box observations have a short side below 16 pixels; at 320 pixels, 15 have a short side below 8 pixels. These correlated box observations are not independent objects, and neither pixel counts nor JPEG PSNR establishes recognizability. The [resolution examples](artifacts.md#phase-1) and gallery preserve actual encoded imagery for inspection.

The practical follow-up is an explicit representation choice—overview, crop, or full image—with exact byte accounting and a minimum useful-detail criterion. That should be evaluated with operator judgments and genuine predicted regions, including missed or unknown objects. This run did not implement or validate an adaptive crop scheduler.

## What was tested and what failed

The three frozen candidates were a tiled color/pHash comparator, a tail-pooled local color residual, and a gated translation-registered residual. Each could reduce the hybrid’s redundancy score when local evidence changed. That design cannot repair the global method’s own sensitivity to motion or illumination. Labels never enter descriptor extraction, scoring, or selection.

Controlled probes showed sensitivity to an 8×8 inserted marker that the global descriptor largely diluted; all methods still missed the 2×2 marker. JPEG compression generated false novelty. These are generated sensitivity probes, not demonstrations of tiny-vessel recall. The development annotation set has no tracks below the strict original-area small-object threshold used in the main evaluator.

Independent review found a concrete registration failure: a new object in a border exposed by camera movement can be removed from the comparison by overlap masking. Bidirectional alignment did not prevent it. This is an additional reason not to adopt the frozen registration method. The implementation and failure evidence remain available for research, with no production integration.

Primary-source research covered perceptual hashes, registration/structural comparison, sliced detection, and compact visual embeddings. [OpenCV’s hashing documentation](https://docs.opencv.org/4.13.0/d4/d93/group__img__hash.html) supplies primitives; [SAHI](https://arxiv.org/abs/2202.06934) motivates small-object slicing; [MobileCLIP](https://machinelearning.apple.com/research/mobileclip) motivates efficient learned features. None establishes success for this maritime bandwidth task. Learned embeddings and sliced inference were researched, not benchmarked or trained in this run. The complete [source ledger](artifacts.md#phase-1) distinguishes literature findings from our hypotheses.

## Edge cost, with the right limits

The repository target is an Intel rugged-laptop-class CPU, at most four ML threads, 2 GB ML RSS, and at least two processed frames per second per camera. Experiments used one Intel `n2-standard-8` proxy with workload affinity restricted to logical CPUs 0–3 and four reported OpenCV threads. It does not reproduce laptop thermals or competing shipboard workloads.

A follow-up processed 120 actual 720p development frames representing a nominal minute at two FPS. The 5-FPS source makes sampling alternate between 0.4 and 0.6 seconds. With NanoDet, the sum of measured decode, analysis, JPEG encoding, candidate preparation, and ranking stages was:

| Method | Measured stage sum for 120 frames |
|---|---:|
| Current hybrid | 20.68 s |
| Tiled hash | 21.71 s |
| Local color | 29.41 s |
| Registered residual | 34.69 s |

All fit within a minute on this scene. These are measured stage sums, not independent sustained pipeline runs or reporting-latency guarantees; selection, transmission, payload writes, and unrelated workloads are excluded. The shared detector-mode process peaked at about 162 MiB while holding all candidate caches. No candidate skipped a comparison on this workload.

That last point matters: synthetic data overstated pruning gains. At 300 generated images, registration took 262 seconds without pruning and 62 seconds with exact pruning, already exceeding a minute before image processing. Exact order, score, and nearest-reference parity passed at all 12 synthetic method/size combinations and six real-scene N30 checks. See [cost review](artifacts.md#phase-1) for per-stage distributions, memory definitions, and workload differences.

## Extra activities executed

Beyond the descriptor comparison, the run executed six JPEG capacity/detail variants; four controlled development representation comparisons; a genuine CPU detector smoke test and locked detector ablation; synthetic and real-scene ranking-cost studies; and a policy-history audit.

The policy audit reproduced a constructed case where an oversized image that is never selected still suppresses a fitting image because ranking happens before byte selection. However, changing history to selected images produced **no selection difference in any of the 50 actual development pool/budget cases**. This is a demonstrated edge case, not evidence to replace the current budget-independent ranking contract. Selection also remains different from acknowledged delivery.

The final extra activity preserved a checksum-verified **227.8 MB private input snapshot** locally, including the exact 1-FPS evaluation inputs and 120-frame runtime workload. The temporary lab can expire without erasing the reproduction inputs. [Input snapshot](artifacts.md#phase-1) · [Snapshot checksum and validation](artifacts.md#phase-1).

## Confidence, review, and reproducibility

The main study used 1,263 sampled frames: 273 development and 990 held-out, from 18 sequences. The nine held-out sequences represent eight original recordings; the two Norfolk segments stay together. There are 19 held-out pools. Code, model bytes, sampling, encoding, source modules, and index were locked before one held-out run per provider configuration. Parameters were not retuned on held-out outcomes.

All methods share the same evidence, payloads and byte allowances within a run. Controls include color-only, pHash-only, hybrid, no similarity, and uniform selection. Full rankings are reused across budgets. A partial final pool receives the full nominal 60-second allowance, so these figures are not a continuous-link simulation. One positive held-out track-window has no sampled annotated frame; 63/64 is the all-status sampling ceiling, not a residual ranking error. An image recorded within two seconds of a first annotation is a retrospective capture metric, not two-second delivery.

Independent review caught and corrected requested-versus-effective thread reporting, an incomplete code lock, a window-boundary event metric, misleading annotation-gap interpretations, and Monte Carlo interval instability. Paired uncertainty now uses exact original-source bootstrap enumeration, preserving source dependence. Intervals remain descriptive and cannot compensate for weak annotations, small source counts, or unrepresentative footage.

Validation includes the unchanged baseline’s **88 tests**, **36 research tests**, exact budget replay for the principal runs, independent summary recounts, input checksums, and the encoded-image gallery review. Source labels and QA were preserved; no autonomous visual judgment was promoted to human ground truth.

This dataset includes handheld/social footage, captions, camera-platform occupants, and incomplete small-object labels. It does not establish Navy watch-camera performance across night, weather, optics, or sensor motion. The next evaluation should define operator interest and event freshness, use representative recordings with independently reviewed annotations, measure recognizable content received under a real link, and run sustained profiling on the target laptop. The completed experiments give concrete reasons to prioritize that work over adopting these three descriptor changes.

The reusable GCP workflow is described in [the environment guide](gcp-environment.md); the private artifact archive preserves the portable skill package. The isolated research VM and its boot disk are cleaned up after all results and inputs are preserved; verification is recorded in [cleanup evidence](artifacts.md#phase-1). Shared VMs, source datasets, catalogs, IAM, and Devin’s repository work were not changed. Detailed private artifacts remain external in the task’s cloud prefix and private archive; this repository contains sanitized code and condensed evidence.
