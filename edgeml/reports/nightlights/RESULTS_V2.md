# Improvement round results

See STAGE1C.md for frozen hashes, exact commands, split seeds and protocols.
No Go changes or commits. Full-frame hard-test v1 was preserved, not rerun.

## Full-frame / four-frame-track spatial benchmark

Colour correctness requires a 6 px position match. Distress receives external
signals here, but automatic-rule accuracy excludes its 14 contacts. Each split
contains 112 contacts. Test v1 and test2 have different seeds: their difference
is not a paired estimate of improvement; the paired dev change is more informative.

| Split/candidate | Light P | Light R | Rule accuracy | Automatic rules | Shore FP | Pier FP |
|---|---:|---:|---:|---:|---:|---:|
| hard-dev v1 | 9.20% | 23.93% | 18/112 | 4/98 | 157 | 217 |
| hard-dev v2 | 19.76% | 40.49% | 25/112 | 11/98 | 168 | 89 |
| hard-test v1 (old) | 8.72% | 24.07% | 21/112 | 7/98 | 143 | 248 |
| hard-test2 v2 (fresh locked) | 13.72% | 31.33% | 24/112 | 10/98 | 187 | 121 |
| easy-test v1 | 100% | 100% | 112/112 | 98/98 | 0 | 0 |
| easy-test v2 | 100% | 100% | 112/112 | 98/98 | 0 | 0 |

Hard-dev red correct colours increased 9/69 -> 28/69; red spatial misses fell
52 -> 35. This costs red false positives (1 -> 55), primarily orange shore
confusion. Total background FP fell 374 -> 257, but shore FP alone increased.
Test2 has 12/73 correctly coloured reds and 52 spatial misses: tiny blurred
red/orange discrimination remains a major weakness. Test2 background is red 59,
white 232, yellow 15, blue 2 (308 total); dev v2 red 55, white 196, yellow 6
(257 total). Complete matrices and per-rule counts are in the corresponding reports.

Unresolvable direct-light patterns remain in all totals:

| Split | Resolvable rule correct | Unresolvable rule correct |
|---|---:|---:|
| hard-dev v2 | 16/76 | 9/36 |
| hard-test2 v2 | 22/78 | 2/34 |

These strata include signal-assisted distress. A correct label in an unresolvable
case is not proof of visual recovery: external signals and contextual guesses
can produce correct labels. Single-pixel saturation erases direct colour, but a
reflection can retain a cue; this scope is explicit in STAGE1C.md.

## Gemini vs ours: identical JPEG crops, NO external signals

These are colour-**count** P/R (per-contact multiset intersection), not spatial
P/R. A wrong-location shore light of the same colour can receive count credit.
Both methods receive exactly the same four saved JPEG crops. Neither receives
truth or external distress signals. Do not compare these numbers directly to
the full-frame table above. A paired v1-on-crops comparison was not performed.

| Split | Our P | Our R | Our rule correct | Gemini P | Gemini R | Gemini rule correct |
|---|---:|---:|---:|---:|---:|---:|
| hard-dev | 26.74% | 63.80% | 9/112 (8.04%) | 46.63% | 46.63% | 20/112 (17.86%) |
| hard-test2 | 25.99% | 63.25% | 8/112 (7.14%) | 50.81% | 37.95% | 21/112 (18.75%) |
| easy-test | 100% | 100% | 98/112 (87.50%) | 100% | 100% | 91/112 (81.25%) |

Gemini is more conservative, giving higher hard-tier count precision and rule
accuracy, but lower recall. Neither approach is operationally reliable here.
Our lower crop rule accuracy versus the full-frame automatic score reflects
both loss of broad pier-row context and inclusion of unassisted distress cases.

| Split | Calls | HTTP/transport failures | Invalid JSON | Mean latency | Median | p95 |
|---|---:|---:|---:|---:|---:|---:|
| hard-dev | 112 | 0 | 7 | 1.443 s | 1.237 s | 3.515 s |
| hard-test2 | 112 | 0 | 3 | 1.787 s | 1.277 s | 1.794 s |
| easy-test | 112 | 0 | 0 | 1.393 s | 1.328 s | 1.909 s |

Exactly 336 calls, four workers, zero retries. All responses and individual call
latencies are cached under gitignored data/nightlights/gemini. All ten invalid
responses were truncated JSON with `finishReason: MAX_TOKENS`; they are scored as empty detections (recall penalty) and
incorrect rules, never excluded. Empty output does not add a precision denominator.
No prompt/config adjustment or retry was made after seeing these results.
Per-colour/per-rule/observability breakdowns and predictions are retained in
gemini_comparison.json.

Crop rule strata (ours / Gemini): dev resolvable 6/76 / 18/76, unresolvable 3/36 /
2/36; test2 resolvable 8/78 / 18/78, unresolvable 0/34 / 3/34.

## Demos and checks

Eight moderate scenes were regenerated and redetected; see demo.md for each
truth -> predicted contact and exact JPEG sizes. Failures are preserved, including
head-on -> NUC and pilot -> long tow. They should not drive automatic alerts.
The real library remains 0 night / 48 frames, eight SAR not applicable, zero
light candidates. Demo frames remain 960x600 and below 150 KB, with existing
catalog classes, 480x300 boxes and signal-assisted distress metadata.

```text
..............................................                           [100%]
46 passed in 0.32s
```

The cache test proves unchanged requests are reused, including content-identical
requests across splits; it makes no network calls. Tests cover valid/empty/invalid
JSON, failure denominators, the call cap, halo colour, sodium rejection and
unresolvable labeling. No app/Go integration was performed.
