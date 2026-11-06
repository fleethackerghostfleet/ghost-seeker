> Repository edition. Personal operator answers are excluded. Private imagery, the unfilled review worksheet, and detailed logs are mapped in [the artifact guide](artifacts.md).

# Maritime image prioritization: context, detail, and receipt

**Keep the existing similarity baseline, and develop representation choice around scene context plus selective detail.** The additional work supports that direction, but not a production policy change. Crops can preserve small-object detail efficiently; they also miss much of a scene. Small overviews arrive quickly but can erase the detail that made an image worth sending. More completed image files do not necessarily mean more useful observations.

This continues the [first research study](phase1-findings.md) with actual encoded alternatives, causal transport experiments, a separate maritime image challenge, and independent reviews. This research made no production or Devin-checkout edits; main advanced independently during the work. These are development experiments, not Navy operational validation.

[Decision figure](../figures/phase2-findings.png) · [Operator review worksheet](artifacts.md#phase-2) · [Reproduction and evidence](../README.md)

## What was executed

- Encoded 273 development frames into 1,213 actual JPEG payloads: four full-scene variants and 121 detector-proposed crops. Every encoded size, hash and dimension was checked.
- Replayed 2,304 causal link scenarios across eight policies, four rates, four availability patterns, and one- versus five-second batching. These are lightweight deterministic simulations, not 2,304 cloud jobs.
- Executed 432 separate chunk-size experiments and 144 completion-bias experiments after reviewing a blocking failure. Independent audits checked dispatch decisions, byte offsets, capacity and receipt accounting.
- Downloaded and verified 100 additional repaired Singapore maritime training images containing 871 annotations. Ran real NanoDet inference, encoded primary forms, and tested a separate all-predictions crop ablation.
- Prepared a six-case, 18-image operator worksheet using actual JPEG bytes. It has no prefilled human judgments and does not manufacture operator ground truth.

The follow-ups also include a sustained Intel CPU profile and real Go transport replays at both the historical baseline and the newly observed main commit.

## Complete receipt changes the question

The first study asked which whole images fit a nominal minute's byte allowance. This study instead starts with zero link credit and admits images only after their causal batch closes. A representation counts only when all its modeled wire bytes and propagation delay complete before the recording ends. A partial JPEG consumes bytes but contributes no complete image receipt.

The primary transport model uses 64 bytes of overhead per image, 0.25 seconds propagation, reliable transfer resumed through outages, and a 60-second latest-start limit measured from queue admission. It does not abort already started images at that limit. There is no post-recording drain. Actual clip duration determines available service, so a nine-second recording no longer receives a minute of capacity. Processing is treated as idealized apart from the declared batch wait; cached analysis is not a claim of zero real computation.

Rankings are recomputed only within already completed one- or five-second batches. No prior minute-pool ranking or future frame is reused. One-second batches contain one sampled frame, so that variant effectively has no within-batch redundancy suppression. Five-second batching changes both waiting time and novelty scope. Neither model keeps a novelty history of acknowledged images across batches.

The clearest annotation subset has **four sequence-local track IDs across only two positive recordings**, plus one recording with no positive labels. Three IDs are separate passes of one physical cruise ship. Counts are not four independent vessels or a population accuracy estimate.

At constant 64 kbps, one-second batching, at least 90% of the image-visible annotated box retained, and at least 16 pixels on its retained short side:

| Policy | Track IDs with qualifying simulated receipt | Within 10 seconds of first annotation | Median first receipt delay, received tracks only |
|---|---:|---:|---:|
| Full 1280/q80, priority | 1/4 | 0/4 | 19.81 s |
| Full 1280/q50, value per byte | 2/4 | 1/4 | 10.70 s |
| Overview 640/q80, value per byte | 1/4 | 1/4 | 7.56 s |
| Overview 320/q50, value per byte | 0/4 | 0/4 | — |
| Whole-scene form chosen for predicted 16px detail | 2/4 | 2/4 | 7.65 s |
| Overview followed by crops/full upgrades | 2/4 | 2/4 | 3.54 s |

**These are geometric support criteria, not recognition thresholds.** Changing the detail requirement matters: at 32 pixels, the predicted-detail policy falls to 0/4, full q50 to 1/4, and the progressive policy remains 2/4. Quality 50 and 80 can have identical geometry while differing visibly in compression artifacts. Controlled operator evaluation is still missing.

All-status results are retained as diagnostics: at the same 64 kbps/16px setting, progressive and predicted-detail policies each receive 6/13 IDs, versus 3/13 for full q80 priority and 5/13 for q50. Partial annotations and annotation gaps prevent treating these as true-object recall. All rates, sources and sensitivity settings remain in the evidence, including regressions.

The progressive heuristic sends a 320px overview first, then eligible predicted crops or a full-image upgrade after the overview's modeled receipt. Its static priority uses the existing image score, a declared form weight, and exact bytes. It is not a learned utility estimator, tracked-contact scheduler, or optimized marginal-information policy.

## Where images are lost

The posthoc decomposition separates absent sampled annotations, missing suitable forms, suitable forms never started, dependency blocking, and incomplete or late transfers. Labels enter only this evaluation step.

For the four complete-status IDs at constant 64 kbps, every 320px overview fails the 16px detail criterion before transport. The full q80 policy has suitable representations for all four, but one is received, one starts without completing in time, and two never start. The progressive policy receives two qualifying IDs via crops; suitable forms for the remaining two never start. This points to both representation and scheduling constraints. It does not establish that a crop caused a counterfactual gain, since removing crops would change the queue.

## Crops cannot stand in for the whole scene

The additional Singapore sample was chosen deterministically from canonical, eligible **training** rows, with no inference- or label-based selection. Each image was read at a pinned GCS generation and verified against the repaired catalog's SHA-256, MD5 and size. All annotation joins and normalized-to-pixel box mappings were checked. No exact image hash overlaps the earlier video sample.

Original video grouping is unknown for these rows. The sample is 100 images, not 100 proven independent scenes; the upstream ground-truth labels were not independently human-reviewed here. This is a cross-dataset engineering challenge, not a fresh held-out claim.

| Available representation | Annotations with ≥16px detail | Annotations with ≥32px detail | Median encoded bytes per image |
|---|---:|---:|---:|
| Full 1280/q80 | 781/871 | 518/871 | 93,652 |
| Full 1280/q50 | 781/871 | 518/871 | 57,449 |
| Overview 640/q80 | 518/871 | 180/871 | 27,567 |
| Overview 320/q50 | 180/871 | 35/871 | 4,837 |
| Top two predicted crops | 276/871 | 231/871 | 13,588 |
| All predicted crops, subsequent diagnostic | 440/871 | 393/871 | 24,379 |

Every row also requires at least 90% containment of the visible annotated box. Counts concern annotated instances before scheduling. Crops are never created from ground-truth boxes; the last row only removes the top-two cap from the same frozen predictions.

Top-two crops contain at least one qualifying annotation in **99/100 images**, while preserving only **276/871 instances** at the 16px criterion. Image-level success conceals missed content. Conversely, among 72 annotations smaller than 32² source pixels, crops preserve 18 at that criterion while the 640px overview preserves none. Context and detail complement each other.

Visual review also caught threshold brittleness: a crop that visibly contains a boat can miss the 90% containment rule by a few percentage points. Sensitivity at 75%, 90% and 95% is preserved. A failed geometric threshold does not mean an image is useless. Generic detector confidence likewise does not establish maritime interest: the development examples include predictions centered on clouds, source-video graphics and own-platform structures, alongside unfamiliar content that may still matter.

## Chunking helps blocking, but is not itself utility

One Python replay of actual JPEGs had a full-image upgrade start at 3.995 seconds, remain incomplete at the 9.2-second cutoff, and block a higher-priority small overview arriving at four seconds. In a separate model, 1 KiB and 8 KiB payload chunks allow that overview to arrive at 5.146 and 6.042 seconds respectively. A 32 KiB chunk still blocks it past the cutoff.

The broader tradeoff is less simple. Small chunks add headers and can scatter service across several incomplete images. At 64 kbps, 8 KiB preemption increases progressive complete-form receipts from 252 to 276 across all nine recordings, yet leaves the complete-status 2/4 and all-status 6/13 geometric track counts unchanged. At 256 kbps, progressive all-status coverage moves from 9/13 to 10/13 with 1 or 8 KiB chunks, but falls to 8/13 with 32 KiB chunks.

A separately recorded completion-bias experiment boosts priorities as locally serialized bytes accumulate. With 8 KiB chunks at 64 kbps it increases full-image receipts from 12 to 17 and progressive receipts from 276 to 288. Qualifying track counts remain unchanged across the constant-rate comparisons. Analytic fixtures also demonstrate that favoring near-complete images can delay a newly arriving small image past its deadline.

The production Go scheduler already chunks transfers, uses remaining unacknowledged bytes and has different form ordering, manifests, protobuf overhead and ACK behavior. The Python blocking example is a model-assumption finding, **not a discovered production defect**. Local serialization offsets are not acknowledgements. Direct Go replay is needed before drawing implementation conclusions.

## Sustained Intel CPU profile

A task-owned Intel `n2-standard-8` worker ran the actual image decode, NanoDet analysis, four full-scene JPEG variants and up to two predicted crops, with affinity restricted to logical CPUs 0–3 and four effective OpenCV threads. The fixed stress phase processed **2,889 frames in 600.053 seconds: 4.815 FPS**. Its 28 complete 100-image cycles ranged from 4.646 to 4.861 FPS; pipeline p95 was 210.15 ms. A subsequent two-minute paced phase processed 240 frames at a scheduled two FPS with **zero missed 500 ms deadlines** and p95 210.06 ms. Peak process memory was 105.3 MiB.

The stress maximum was 514.78 ms, including an outlier in the representation/write stage whose precise cause is unproven, so this is not a claim that every frame always finishes under 500 ms. The run repeated the same 100 Singapore images; model construction was outside the timer, the paced phase was warm, and ordinary file writes were not fsynced. It excludes production queue/transport work, competing workloads, thermal throttling on the rugged laptop, and additional cameras. This provides sustained cloud-proxy evidence for the encoding path, not target-device certification. Raw frame timings and deadline arithmetic are preserved and independently checked. The owned VM and boot disk were deleted after verified preservation; matching resource lists are empty.

## Real Go replay

The same two-image fixture was executed through real Go edge/shore stores, scheduler, virtual link and shore service at both historical `a9c2de09` and current-main snapshot **`1a6382ed`**. Six JPEGs and two FACT forms contain 161,754 unique application bytes. All received bytes were hash-verified. Current results are:

| Link and outage | Charged wire bytes | Virtual time to drain all forms | JPEGs received within 60 s |
|---|---:|---:|---:|
| 9.6 kbps, constant | 180,794 | 169.262 s | 4/6 |
| 9.6 kbps, outage at 2–12 s | 188,324 | 199.037 s | 3/6 |
| 64 kbps, constant | 165,423 | 23.678 s | 6/6 |
| 64 kbps, outage at 2–12 s | 207,350 | 52.419 s | 6/6 |

These exact virtual-link charges include protobuf, framing, control traffic and retries. They show why fixed per-image overhead can misrepresent the real stack. This fixture drains a queue beyond source-clip duration; it is not directly comparable with the Python clip-horizon coverage table. The direct-service path also does not exercise HTTP authentication middleware or HTTP gzip compression, and it is not a physical radio test.

A separate current-main control used synthetic `event=scene` metadata to exercise the real scene branch. FACT and THUMB were sent; FULL stayed entirely unsent through 61.23 seconds until an explicit pull. Its 51,872 bytes then reached shore at 68.728125 seconds and were acknowledged at 69.043250 seconds. This tests policy behavior, not whether the depicted image should be classified as a scene. An adapter must preserve this existing pull-only behavior rather than blindly applying the experimental progressive ladder.

Twenty-three existing current test functions, four replay subcases and the scene/pull control passed. The pinned current source snapshot has 140 verified files. The algorithm directory still matches the first study exactly; scheduler, protocol and store changes were evaluated separately. No branch used by Devin was altered.

## What this supports next

The current evidence supports implementing and validating a conservative scene-context path with selective detail upgrades, while keeping the existing similarity baseline. It does not justify making generic detector crops the sole image stream or choosing a new production scheduler from this small development set.

The [operator worksheet](artifacts.md#phase-2) makes the next human-dependent question concrete: whether actual compressed representations provide useful awareness, enough detail, and enough context. The source worksheet starts unfilled, uses label-blinded A/B/C ordering, and keeps answers local with JSON export; responses remain outside this repository. Side-by-side variants can cue interpretation; this is not a controlled recognition trial. New representative camera recordings and independently reviewed annotations remain necessary for operational validation.

Independent audits checked all 2,304 primary replays, all 9,216 geometric metric rows, all 4,096 primary summaries, the miss decomposition, and the separately versioned transport ablations. Inputs, source hashes, every real JPEG, raw receipts and reviews are preserved privately. The earlier test split was not reused for tuning or presented as fresh validation.
