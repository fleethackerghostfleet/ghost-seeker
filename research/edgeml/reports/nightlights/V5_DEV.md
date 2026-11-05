# v5 dev-only freeze candidate

Synthetic/procedural measurements only, not at-sea accuracy. Tuning and evaluation
used hard/dev and easy/dev only. No test, test2, test3 or test4 dataset was run or
generated during this iteration. v4 artifacts are retained unchanged.

## Method

Reflection association no longer disables independent acceptance of other lights.
Each candidate can qualify through its own compact high-contrast PSF, or through
colour-pure, high-contrast, same-colour vertical grouping. Sodium ambiguity, pier
rows and dense horizontal rows still reject independently associated candidates.
Grouped bloomed points must also have a compact half-peak footprint centered
across the column; this prevents broad glare corners from masquerading as pairs.
Confidence uses each light's own reflection/PSF evidence, not whether any sibling
was reflected. The existing resolved-pair confidence term remains in use.

The threshold remains **0.85**. Strict complete colour-multiset equality, zero
white allowance, rule matching and assisted-distress exemption are unchanged.
No labels or nuisance lists are read at inference. Reflection support retains
its synthetic-generator optimism caveat.

## Dev results

Urgent columns are true/false automatic boosts. False boosts are all/automatic.

| Dataset | Light P | Light R | Rules /112 | Automatic /98 | RAM | NUC | Draught | False boosts | Night gate | Day false-night |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| easy/dev v4 | 100% | 100% | 112 | 98 | 7/0 | 7/0 | 7/0 | 0/0 | 120/120 | 0% |
| easy/dev v5 | 100% | 100% | 112 | 98 | 7/0 | 7/0 | 7/0 | 0/0 | 120/120 | 0% |
| hard/dev v4 | 76.64% | 50.31% | 38 | 24 | 2/0 | 1/0 | 1/0 | 0/0 | 132/136 | 0% |
| hard/dev v5 | 72.41% | 51.53% | 39 | 25 | 2/0 | 2/0 | 1/0 | 0/0 | 132/136 | 0% |

The additional correct urgent boost is NUC seed 300201 (pattern confidence
0.9321875). Light counts change from 82 correct / 107 predicted / 163 observable
to 84 / 116 / 163. **Precision decreases:** additional unmatched shore/pier points
remain, despite no false boosts. This is a modest recall improvement, not a
general clutter-rejection solution or a resolution of the occluded CD/NUC ambiguity.

Across all positive strict-match candidates before confidence suppression,
hard/dev has six would-be false boosts in both v4 and v5. Maximum confidence is
0.7916666667 (occluded CD 300106 interpreted as NUC), leaving a margin of
0.0583333333 to 0.85. Easy/dev has no would-be false boost in either version, so
its maximum and margin are not applicable. The threshold is retained to preserve
this margin rather than promote weaker ambiguous patterns.

Dark-contact results are unchanged from the corrected v4 candidate path:
easy/dev TP/FP/FN **28/0/0**, hard/dev **28/9/0**. The v5 dark/burst summaries have
versioned filenames and do not overwrite the v4 summaries.

## Reproduction

```sh
for tier in hard easy; do
  ~/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier "$tier" --split dev --version v5
  ~/nightlights-venv/bin/python edgeml/scripts/nightlights_boost_eval.py --tier "$tier" --split dev --version v5
done
~/nightlights-venv/bin/python -m pytest edgeml/tests -q
PATH="$HOME/sdk/go/bin:$PATH" go test ./internal/...
```

Dark summaries were generated with `nightlights_dark_eval.evaluate(split='dev',
tier=tier)` for each of `easy` and `hard`, writing the returned JSON to
`dark_burst_<tier>_dev_v5.json` instead of the script's unversioned CLI target.
Would-be false boosts were measured by reinterpreting the saved dev lights with
confidence set to one, retaining the night and assisted-signal gates, then taking
the original minimum light confidence among incorrect positive-boost matches.

Checks: **198 passed, 3 skipped** in edgeml pytest; `go test ./internal/...`
exited zero, including the shared Python/Go confidence fixtures. New unit cases
cover retaining an unreflected light alongside a reflected sibling (while
rejecting a sodium point), and accepting a bloomed unreflected pair but not an
isolated unreflected bloom. Existing broad-glare rejection remains covered.
