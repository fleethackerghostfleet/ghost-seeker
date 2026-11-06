# AI decision support and human-in-the-loop governance

## What the ML does — and does not establish

The [sidecar](../../edgeml/src/edgeml/sidecar/service.py) performs configured
detection, optional embedding/zero-shot classification, optional tracking and
observation aggregation, and optional frame/night-light prioritization. The
[ingester](../../edge/ingest/ingest.go), [scoring catalog](../../internal/catalog/catalog.go)
and [scheduler](../../edge/scheduler) turn observations and authorized policies
into item priorities and bandwidth scheduling. Defaults matter: the Zarf sidecar
loads a real detector with a verified manifest, does not pass `--embed`, and uses
stateless detection when BoxMOT is absent. The tracking demo is not the approved
default fielding image.

This is **prioritization/decision support, not engagement decisions**. No weapons
control, target-engagement interface, engagement authority or operational safety
approval is established by the repository. “Commander” UI decisions and labels
must not be interpreted as authorization to engage. Program-approved use cases,
operator qualifications, safety constraints and prohibited uses are **TBD**.

Prioritization and transmission can occur automatically before human review;
there is no claim that a human approves every inference or every network packet.
People review observations, correct/dismiss labels, set allowed priorities and
directives, and make operational decisions outside this model's authority.

## Existing review workflow

[HUMAN_REVIEW.md](../HUMAN_REVIEW.md) is the detailed workflow; code is in
[review storage](../../internal/reviews/reviews.go),
[live bridge reviews](../../internal/shorebridge/reviews.go) and
[retune command](../../cmd/retune). Review capabilities depend on the chosen
runtime mode; lab/dataset/simulation examples are not production deployment
instructions and production demo paths are compiled out.

Reviewers identify themselves with a display name, accept/change a tier, dismiss,
skip or send an item to Commander, and optionally record attribution and notes.
Corrections/undo append records referencing originals rather than erasing them;
the documented log append is synced to disk. Effective CSV/JSONL exports apply
corrections and remove undone records; preserve originals for provenance.
Empty review-log configuration disables review endpoints. The Zarf operator
sets `/data/reviews.jsonl` on a persistent volume.

Shared bearer access does **not** authenticate a self-entered reviewer name.
Optional reviewer join URLs are bearer secrets, not individual identity proofing;
they restrict actions but need private distribution and are incompatible with
PIV mode. Team proposals/votes are optional simulation-only, ephemeral and not
an approval authority. Authenticated reviewer attribution, log integrity,
retention, privacy and operational review-mode authorization are POA&M items.

## Existing offline retune guardrails

The documented clean-v2 review set is settled; only `eval_split=dev` imagery is
eligible. Test/quarantine and Singapore Maritime rows are excluded from review
selection, and the documented title-card/newscaster exclusions remain fixed.
Do not recompute/expand the committed review selection. See
[split policy](../../edgeml/configs/splits.yaml) and the detailed review document.

Retune consumes stable copied logs, resolves duplicates/corrections, excludes
undone/wrong-detection/dismissed labels, combines reviewer labels and keeps items
out of both fitting and holdout partitions. Live rank-based reviews are reported
separately and excluded from weight fitting. Sparse classes/empty holdout produce
insufficient-data outcomes. Candidate weights face synthetic benchmark floors;
rejected proposals retain originals. Neither the tool nor its votes automatically
changes source weights or a running deployment.

The current documented promotion step is **human inspection and mission-owner
approval**, then manual catalog weight editing, benchmark and test runs. A
synthetic gate pass is not real-world efficacy evidence. Historical bakeoff and
finetuning reports record domain/coverage and pretraining-overlap limitations;
they do not certify a fielded model.

## Model verification and proposed operational promotion record

[Runtime integrity verification](../../edgeml/src/edgeml/sidecar/integrity.py)
requires approved local manifest entries with SHA-256/immutable source revisions
and covers selected models and required metadata/auxiliary assets before real
providers load. It rejects tampered/unlisted/escaping assets. Local optional text
weights must be in the verified bundle. The manifest needs trusted distribution:
an attacker replacing both model and approval manifest is not defeated by hashes
alone. The demo TOFU helper is explicitly not an approved provenance authority.

The [shipped image](../../edgeml/Dockerfile) sets Hugging Face/Transformers
offline and telemetry-disable variables. Approved runtime bundles are local;
online evaluation/export download utilities are a separate trust path, not a
reason to grant runtime model-download egress.

For operational changes, the following are **required planning placeholders,
not claims of an implemented governance board**:

1. Model/weight owner, mission owner, independent evaluator and release approver:
   **TBD**; separate proposer/reviewer authority according to program policy.
2. Record source/revision/hashes, model/data licenses and training lineage; reject
   internal-only data contamination. Preserve baseline and candidate artifacts.
3. Evaluate representative ship sensors, weather/lighting, small/rare objects,
   false alarms, missed detections, resource/latency impact and degraded operation.
   Operational thresholds and acceptance criteria: **TBD**. Do not tune on locked tests.
4. Obtain documented mission/security/legal approval; update the verified bundle,
   SBOM/release evidence and signed operational policy as applicable. The Ed25519
   command-layer signature is not a model-licensing or model-quality approval.
5. Controlled rollout, operator notification/training, monitoring, rollback trigger,
   rollback owner and approved previous artifacts: **TBD**. Re-assess drift and
   sensor/domain changes; do not silently retrain or promote from operator notes.

See the [inventory](THIRD_PARTY_AND_MODEL_INVENTORY.md), [POA&M](POAM.md) and
[SSP](SSP.md) for authorization, privacy and lifecycle gaps. No online learning,
autonomous engagement authorization or automatic model promotion is asserted.
