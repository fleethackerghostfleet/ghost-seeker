# Human review and offline retuning

## Review the clean-v2 development set

The committed `datasets/review/clean_v2_dev_review.jsonl` and `.sql` are the
settled selection; do not recompute or expand them. Only `eval_split=dev` imagery
is eligible. The locked `test` and `quarantine` sets must never be reviewed, so
operator labels cannot leak into test scores. The builder refuses non-dev,
Singapore Maritime, test, and quarantine rows before downloading anything.

```sh
export PATH="$HOME/google-cloud-sdk/bin:$PATH"
python3 -m venv edgeml/.venv
edgeml/.venv/bin/pip install -e 'edgeml[onnx,test]' open_clip_torch
make demo-models
edgeml/.venv/bin/python edgeml/tools/build_review_set.py
go run ./cmd/ghost-seeker -dataset data/review_set/manifest.json \
  -dataset-dir data/review_set/images
```

Images and the generated manifest stay under ignored `data/review_set/`.
Detector class mapping: COCO `boat` uses MobileCLIP-S1 ship typing when mapped
by `TYPE_TO_CATALOG`, otherwise `vessel`; `airplane` maps to `aircraft`;
`bird` and `person` retain those classes; `surfboard`, `kite`, and other
unmapped kept labels map to `unknown`. Ship type/aspect remains in ship info.

Start the simulation console with durable capture:

```sh
go run ./cmd/ghost-seeker -review-log data/reviews.jsonl
```

Open **Operator Review** and enter your reviewer name. It is saved locally;
use **Set / change reviewer** to change it. Review keys stay disabled until set.

Optional **Team sync** (simulation console only) shares model-adjustment proposals
with other opted-in sessions. The checkbox is saved in this browser. Set a reviewer
name before sharing; otherwise suggestions stay local. Discuss the yellow **TEAM
DISCUSSION** popup before accepting: **Y** agrees, **N** disagrees (votes do not
apply rules), **A** accepts for the team and adds the rule, **R** rejects for the
team, and **L / Escape** hides it only in your session. Votes update live and the
proposal closes for everyone when resolved. Pending proposals and votes are
in-memory, limited to 20, and lost on server restart. Reviewer names are labels,
not authenticated identities. With sync off, local Y/N/L suggestions are unchanged;
the attribution (**why**) bar is unchanged in either mode.

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
When combining raw logs and effective exports, each duplicate ID is resolved
independently: more populated reason/note fields win, followed by canonical
JSON order. All correction records across all inputs are then replayed onto
the merged bases in timestamp/ID order. Unrelated corrections do not influence
base selection; the merge does not depend on input path order.
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
