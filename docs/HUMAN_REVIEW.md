# Human review and offline retuning

Start the simulation console with durable capture:

```sh
go run ./cmd/ghost-seeker -review-log data/reviews.jsonl
```

Open **Operator Review** and enter your reviewer name. It is saved locally;
use **Set / change reviewer** to change it. Review keys stay disabled until set.

- **1–4**: choose a priority; **Space / Enter**: accept the suggested priority.
- **X / Delete**: dismiss; **C**: send to Commander; **S / Right arrow**: skip.
- **Z / Backspace**: undo the last review (an appended correction, not deletion).
- When the **why** prompt appears, choose the contact that drove your decision,
  or **Wrong detection** for a bad detection box. Escape skips attribution.
- **W**: optional note on your last review. Enter saves; Escape cancels.
  Review hotkeys do not fire while typing in inputs.

The server records item/image/source/time, reviewer name, mission model name
and key, model/edge scores and tiers, human tier (0 for dismissal), action,
agreement, detection classes/confidences/attributes/boxes/scores, active
directive texts, and optional reason/note (280 characters). Corrections and
undo reference the original record. Each append is synced to disk.

The **Review data** panel shows totals, agreement and reviewer counts and links
to CSV / JSONL exports. Exports contain effective reviews: corrections applied,
undone reviews removed. Keep the original append-only logs for provenance.

## Propose weights offline

Use a copied, stable log or exported JSONL, not a file currently being appended:

```sh
go run ./cmd/retune -min-records 10 -out review-report.md \
  -weights-out proposed-weights.json data/reviews.jsonl other-reviews.jsonl
# Standalone 300-record example (demo path must not already exist):
go run ./cmd/retune -demo demo-reviews.jsonl
```

The tool reads logs without opening them for writes. It deduplicates record IDs,
replays corrections, and excludes undone, wrong-detection and dismissed labels.
When combining raw logs and effective exports, duplicate IDs from files with
correction events take precedence, then corrections are replayed. For remaining
ties, more populated reason/note fields win, followed by canonical JSON order;
the merge does not depend on input path order.
Legacy records without a model key are matched by catalog key or display name;
unrecognized models fail rather than guessing. Per item/mission, each reviewer's
latest label counts once; the upper median is used for even numbers of labels.
Conflicting labels are reported. Item-id hashing makes the 80/20 split repeatable
and keeps the same item out of both sets across reviewers.

Production detection/frame/tier scoring is used without directives. Named reason
classes receive credit; otherwise the strongest-scoring detection does. Only
classes meeting the minimum **training item** count are eligible. Coordinate
search uses 0.05 steps, clamps weights to [0,1] and to ±0.30 of the original,
and penalizes under-prioritizing human P1/P2. Attribute lifts are not fitted.
Holdout labels are not used for fitting. Candidate weights must pass the shared
synthetic benchmark floors on seeds 101–103; rejected proposals retain the
original weights in JSON. No source files or running-app settings are changed.

Inspect holdout metrics, class counts and benchmark results. A passing synthetic
gate is not permission to deploy automatically. Review any holdout regression
and obtain mission-owner approval. Apply accepted class weights **by hand** to
the matching key in `internal/catalog/catalog.go` (`catalog.Models[key].W`), then:

```sh
go run ./cmd/bench
go test ./...
```

## Limitations

- Names are self-reported under a shared operator token, not authenticated identities.
- Durable reviews are **not captured in shore/live mode**; that mode retains its
  existing console overlay/PATCH behavior.
- The simulation feed itself is ephemeral; the review log survives restarts.
- Empty `-review-log` disables review endpoints (503). Do not run multiple writers
  against the same file. Back up logs and protect them as operator-generated data.
- Notes are context only; retuning uses tiers and class attribution, not free text.
- Sparse classes/empty holdout produce “too little data”; no fitting is performed.
- Synthetic examples demonstrate mechanics, not evidence of real-world improvement.
