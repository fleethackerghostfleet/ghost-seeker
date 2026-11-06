# Two-cluster UDS validation evidence

## Current result: rc2 release artifacts, fresh clusters

**STEP 5 passed:** [rc2 report](rc2/README.md) and
[runbook findings / deviations](rc2/RUNBOOK_FINDINGS.md).
Runbook: `origin/prod-uds` **71f42d9** (#272). Release: **uds-v0.1.0-rc2**.
All four named amd64 bundles/packages passed published SHA256 checks before
the old clusters were deleted. Fresh shore/ship Core clusters were created;
**no local app images or packages were built in this phase**.

- Both clusters finish **25/25 Running and Ready**.
- Verified gateway TLS/HTTP2: unauthenticated **401**, authenticated **200**;
  actual SyncService/Exchange access logs show **HTTP/2, grpc_status=OK**.
- Seven live uploads: one initial P1 plus six outage frames. Shore count
  **1 → 1 → 7**, edge queue **0 → 6 → 0**, failures **0 → 2 → 0**.
  Recovery acknowledged **1,211,166 bytes**; backlog manifest arrivals were
  **P1, P2, P3, P4, P4, P4**. The 15-second 64-kbps leg did not complete an
  exchange; full drain occurred at 16 Mbps, not at sustained low rate.
- With ship internet/DNS blocked, app pods restarted, packaged models loaded,
  and another real JPEG scored (boat **0.77753**) and synchronized: **8/8**
  complete. Blocking is **OFF**, Docker DNS unblocked, unrelated host filter
  rules restored exactly; shaper remains at **16 Mbps**.
- Visually checked [feed](rc2/img/shore-feed.png),
  [P1 detail](rc2/img/shore-item-detail.png),
  [link/sync status](rc2/img/shore-link-sync-status.png).

rc2 is a laboratory validation, not fleet certification. Imagery licensing
restrictions below still apply; do not redistribute screenshots without
resolving upstream permissions. Earlier local-build runs below are
**superseded/supporting evidence only**, not rc2 results. Historical statements
such as “current” or “no commit” describe their original handoff, not STEP 5.

## Superseded local run: PR #270 / f0121e4

**Handoff boundary:** the local clean deployment and screenshots below had
already completed before the instruction to stop local builds and await an rc2
fresh-install brief. The active air-gap check was then wrapped, with blocking
OFF. No rc2 artifacts or new clusters were installed; this is local-build
validation, **not rc2 release validation**.

The current evidence targets `origin/prod-uds` at
`f0121e459e6197deb287abdbf3f8e8cf4cc71718`, after PR #270, on branch
`devin/1793981091-uds-two-cluster`. No commit/push has been made.
All five production images were rebuilt with unique tag
**`prod-f0121e459e61-20261106170117-9abc79bd`**. Go images use `-tags prod`;
sidecar build uses `WITH_TRACKING=1`. The packaged model init verifies its
checksums, including both COLREGS catalogs.

The final clean run explicitly removed **only both application packages and
their app namespaces/PVCs**, then recreated auth from the existing private
files and deployed shore before ship. **Neither UDS Core cluster was recreated
or removed.** This destructive app-data reset was only for a clean validation
baseline; the subsequent air-gap uninstall/reinstall retains its namespace/PVCs.
Workloads now use `###ZARF_REGISTRY###/` and `private-registry`, exactly as
[UDS_DEPLOY.md](../../UDS_DEPLOY.md) specifies. Configuration uses absolute
`UDS_CONFIG` paths, not the unsupported `--config` flag. The deployment helper
now rejects stale staging without that registry contract and waits for each
role's workloads and Package readiness before proceeding.

Authoritative artifacts:

| Directory | Contents |
| --- | --- |
| [final-deploy/](final-deploy/) | Unique image build, split package/bundle build and deployment logs; artifact SHA256SUMS; revision; driver logs |
| [step3-final/](step3-final/) | Gateway auth, seven live ingests, SSE, priority order, outage counters, both cluster pod snapshots, gateway HTTP/2/gRPC status |
| [img-final/](img-final/) | Latest feed, complete P1 detail drawer, shore pipeline status screenshots |
| [airgap/final/](airgap/final/) | Latest blocked-egress restart/reinstall, local-registry image pulls, models, inference, sync and sustained low-rate evidence |

### Final authenticated sync / outage results

Gateway checks with normal CA/hostname verification:

```text
without operator token: HTTP 401 protocol=2
with operator token:    HTTP 200 protocol=2
```

The host shaper remains the sole ship-to-shore route:
`shore.uds.dev:443 -> 172.19.0.1:443 -> 127.0.0.1:9443 -> shore tenant gateway`.
The ship hostAlias and `172.19.0.1/32:443` egress policy are harness configuration,
not production manifest edits. Actual edge exchanges at the gateway use HTTP/2
with successful gRPC trailers; see `step3-final/gateway-access.log`.

| Native counter | Before outage | During outage | After recovery |
| --- | ---: | ---: | ---: |
| Edge stored items | 1 | 7 | 7 |
| Edge complete / manifested items | 1 | 1 | 7 |
| Edge queued items | 0 | 6 | 0 |
| Edge consecutive failures | 0 | 2 | 0 |
| Edge acknowledged bytes | 309,721 | 309,721 | 1,520,887 |
| Shore received items | 1 | 1 | 7 |
| Shore acknowledged bytes | 309,721 | 309,721 | 1,520,887 |

All **7** new inputs were accepted; **6** accumulated during outage. Recovery
acknowledged **1,211,166 bytes**. First backlog SSE arrivals were:
`P1 0.6362236420`, `P2 0.6304815819`, `P3 0.4386668727`, then three P4s
in descending score order (cursors 9, 10, 11, 13, 16, 17). This proves
**priority-first manifest arrival**, not strict whole-JPEG completion order.
The natural P1 boat confidence was **0.87247**; no scores/policy/database were
altered. The evidence helper's short 15-second 64-kbps leg did not finish a new
exchange; the separate sustained test is recorded under `airgap/final/`.

Screenshots were rendered and visually checked against this seven-item state,
before the additional air-gap ingests:

- [Latest shore feed](img-final/shore-feed.png)
- [Latest P1 item detail](img-final/shore-item-detail.png)
- [Latest shore link / sync status](img-final/shore-link-sync-status.png)

The first full-viewport detail capture clipped the side drawer; the helper now
captures the drawer itself, which was rechecked. No application layout changed.
Shore-only UI still intentionally says `On ship —`. Its detected-object label
counts records rather than actual detections: this seven-record capture has
**4 detection-bearing records and 5 detection entries**, not 7 objects.

### STEP 4: latest air-gap results

**PASS: ship runtime restart and fresh app-package deployment while blocked.**
See [airgap/final/result.json](airgap/final/result.json) and the full driver log
in `final-deploy/airgap-final-run.log`.

- GHCR with a pinned resolved IP (no DNS dependency), direct `1.1.1.1:443`,
  and normal external DNS failed. These same destinations worked before
  isolation. Host curl was executed in the ship node's network namespace
  because its BusyBox wget lacks HTTPS support.
- The shaper path remained reachable with verified TLS and HTTP/2:
  `HTTP 401 protocol=2 verify=0` without a token. Authenticated real JPEG
  ingest subsequently synchronized successfully through it.
- Both deleted ship app pods returned **2/2 Running**. Offline model-init
  checksum logs passed and the real inference server loaded successfully.
  The restart frame's boat confidence was **0.77753**; the post-reinstall
  frame's was **0.80137**. Both were accepted with 202 and matched on shore.
  Exact JPEG members, sizes and SHA256s are in the per-phase `*-input.json`.
- Ship app package removal succeeded. Zarf intentionally retained the
  namespace, Secrets and two PVCs; no Core package was removed. All three app
  image references were then evicted from containerd, and `UDS_CONFIG=... uds
  deploy <local-ship-bundle>` reinstalled the package while blocking remained
  ON. Events show successful pulls of edge, models and sidecar from
  **`127.0.0.1:31999`**, not an external registry. Model/sidecar pulls after
  eviction took 183/188 ms. This does not prove cold layer download: layers
  and Core/Istio images may remain cached.
- No ImagePullBackOff/failed external pull was observed. Pepr/Zarf admission
  and the local registry remained functional. Their scoped logs are captured
  as `pepr-system-*.log` and `zarf-*.log` under `airgap/final/`.

**Important firewall finding:** DOCKER-USER alone does not isolate Docker's
embedded DNS proxy. The initial external lookup still succeeded, disproving
that assumption. Node NAT redirects gateway DNS to `127.0.0.11`; the host-side
resolver can forward outside the ship subnet. The script therefore also uses
tagged host INPUT rules and a tagged OUTPUT drop to `127.0.0.11` in **each ship
container's network namespace**, installed with host `nsenter`/iptables (the
load-balancer container has no iptables executable). Only the shaper's gateway
TCP/443 and host-initiated replies are allowed at host INPUT; forwarding within
the ship subnet remains allowed. Kubernetes service discovery still worked.
Docker-name resolution is intentionally blocked alongside external DNS.
`off` deletes exact tagged rules, not unrelated rules or chains.

**Sustained 64-kbps payload progress, not a full drain:** a third live JPEG
(157,273 bytes) was queued during an outage, then the rate was held at
64,000 bits/s for **180.9 seconds**, still air-gapped. Successful exchanges
advanced the last-success timestamp from `1793984818388` to `1793984984133`,
manifested the new item, and acknowledged **88,504 additional bytes**
(302,675 → 391,179). Thus this run completed actual payload-bearing exchanges,
not just idle heartbeats. The whole item remained incomplete: 9/10 complete,
85,301 next-eligible-form bytes queued, one consecutive failure, and a native
link estimate of approximately 491 kbps despite the 64-kbps shaper. Those
measurements show continued retries/partial progress; they do not establish a
root cause for the incomplete full-image transfer or stable 64-kbps throughput.
No full-drain claim is made for that window.

After restoring 16 Mbps, **10/10 items completed** on ship and shore and ship
failures returned to **0**; see `post-restore-status.json`. Blocking is now
**OFF**, both ship containers' Docker DNS is **UNBLOCKED**, and a comparison
against the original host filter rules confirmed unrelated rules unchanged.
Final both-cluster pod snapshots are in `airgap/final/*-pods-final.txt`.

Targeted checks printed:

```text
Ran 10 tests in 1.881s
OK
Ran 85 tests in 11.695s
OK
PASS: final shell syntax and Python compilation
PASS: revised screenshot helper Python compilation
PASS: host filter rules restored exactly; unrelated rules unchanged
PASS: 600 harness/report files contain no current credential values or base64 encodings
```

The 10 harness tests cover auth/evidence parsing and firewall normal, empty,
invalid-network/command and idempotence cases; the 85 tests are the current
manifest validator suite. Full suites / prod-gate remain unrun (no PR/commit).

### Reproduction and safety

Existing Core clusters must already be running. **The first command below
deletes the two application namespaces and their PVC data**, not Core:

```bash
CLEAN_APP_DATA=1 LOG_DIR="$PWD/docs/reports/uds-validation/final-deploy" \
  scripts/uds/two-cluster-deploy.sh
EVIDENCE_DIR="$PWD/docs/reports/uds-validation/step3-final" \
  python3 -B scripts/uds/two-cluster-evidence.py
EVIDENCE_DIR="$PWD/docs/reports/uds-validation/step3-final" \
SCREENSHOT_DIR="$PWD/docs/reports/uds-validation/img-final" \
  python3 -B scripts/uds/two-cluster-screenshots.py
AIRGAP_REDEPLOY=1 AIRGAP_EVIDENCE_DIR="$PWD/docs/reports/uds-validation/airgap/final" \
  python3 -B scripts/uds/two-cluster-airgap-verify.py
```

Use a **new** evidence directory on repetition; Step 3 refuses to overwrite one.
Use fresh frame indices (`INPUT_OFFSET`, `AIRGAP_FRAME_OFFSET`) when retaining
app databases. Keep auth outside the repo (0600 files / 0700 directory).
Protected ports 8105/8106/8109 and unrelated servers were untouched.
These are internal maritime-capture transport/deployment tests, not an accuracy
benchmark or permission to redistribute the original imagery/screenshots; the
license restrictions in the historical notes below still apply.

## Superseded evidence (b0b3776)

Everything below, plus `STEP2.md`, `step3-initial/`, `step3/`,
`step3-priority/`, `img/`, and `airgap/` **except `airgap/final/`**, is historical
evidence from before PR #270. It is retained to make corrected attempts and the
first air-gap result auditable; it is **not** evidence for current manifests.
`STEP1.md` documents the still-reused Core clusters and their original bootstrap.

**Result:** real JPEGs were accepted and scored by the production ship edge,
received by shore through TLS/HTTP2 and the host shaper, and observed in the
authenticated shore SSE stream and operator console. A six-frame outage
backlog drained after recovery, with the highest-scored **P1 arriving first**.
No production code/manifests were changed, and nothing was committed or pushed.

See [STEP1.md](STEP1.md) for Core/network setup and [STEP2.md](STEP2.md) for
the split package deployment. The base remains `b0b3776`; the production image
tag is `prod-b0b377607950-20261106163143-7d98ece6`.

## Gateway authentication and pod evidence

Host requests used `curl --http2`, normal CA/hostname verification, URL
`https://shore.uds.dev:9443/api/v1/items`, and
`--resolve shore.uds.dev:9443:127.0.0.1`. This tests a concrete resource under
the `/api/v1` prefix, not an undocumented bare-prefix endpoint.

```text
without token: HTTP 401 protocol=2
with operator token: HTTP 200 protocol=2
```

Only status/protocol is shown; no request headers were dumped. The Python
capture helper passes a bearer header to curl's stdin configuration from the
private token file, never in argv or stdout. Browser login exchanges the same
real operator token for a session cookie, without saving the login screen.

Final `kubectl get pods -A -o wide` output for both contexts is saved in:

* [step3-priority/shore-pods-final.txt](step3-priority/shore-pods-final.txt)
* [step3-priority/ship-pods-final.txt](step3-priority/ship-pods-final.txt)

Application pods remain `2/2 Running`, zero restarts: shore/operator on
`k3d-uds-shore`, edge/edgeml-sidecar on `k3d-uds-ship`. Before-run snapshots
are `step3-priority/{shore,ship}-pods.txt`; initial snapshots also remain under
`logs/step3-{shore,ship}-pods.txt`.

## Live ingest and honest P1 selection

All captures were raw, real maritime JPEGs from existing `frames/*.zip`
archives, pushed through `POST /api/v1/ingest/frames?source=...` on an owned
ship-edge service port-forward. No demo analyzer, synthetic image, replay
fallback, source-hint classification or scorer override was used. The edge
was already running `prioritizer=edgeml`, real ONNX models, tracking enabled,
and the COLREGS assets from Step 2. A 202 response is emitted after storing/
scoring, rather than merely enqueueing the HTTP request.

The first capture, while shore was empty, returned:

```json
{"id":"01M6RVR1T5J2H89AMNH9QW0V35","duplicate":false}
```

Shore listed that same ID as **P1**, score **0.6240419922**, with a `boat`
detection confidence **0.84822** and all four forms available. The stream
had connected **before** the POST and delivered an `event: item` with
`event:"created"`, cursor **7**, and the same ID/detection/P1 tier. Evidence:

* [step3-initial/accepted-live-0.json](step3-initial/accepted-live-0.json)
* [step3-initial/live-items.json](step3-initial/live-items.json)
* [step3-initial/shore-stream.sse](step3-initial/shore-stream.sse)

P1 is a **relative shore rank**, not a fixed confidence threshold. The
implementation in `shore/api/api.go:tier` assigns the first item P1, then
top 10%, next 25%, next 35%, remainder; policy pins can override it. No pins
or policy changes were applied. Later arrivals honestly moved this original
image to P2.

The first mixed backlog run succeeded but its highest new item was P2,
because the original P1 was already on shore. This run is retained in
`step3/`, not misrepresented as P1-first. To exercise a new P1 backlog item,
18 **unmodified real frames** were inspected with the existing inference
sidecar. [step3/candidate-selection.json](step3/candidate-selection.json)
records every candidate/detection. A frame with natural boat confidence
**0.87247** was selected, without changing the model, policy, score or shore
database. Its resulting edge score **0.6362234777** exceeded existing items.

The final run's selected P1 is `01M6RVWHXV9YMY7Y333W1PC82V`, source
`uds-20261106T164314Z-outage-2`, from
`SMD_VIS_Onboard_frames/frames/MVI_0797/MVI_0797_f00120.jpg`.
Its SHA-256 is
`1ce3ac4c7f481d8aec7e9e8cb336143211bb1cc03033225e8f0a63883ee3d6f0`.
All selected archive members, byte sizes and hashes are recorded in each
run's `inputs.json`; `accepted.json` maps responses to input indices/sources.
`during-edge-items.json` captures real edge feature detections and scoring
reasons. `after-shore-items.json` captures the corresponding shore records.

## Outage, counters and backlog drain

The final run accepted **7 new frames**, including one baseline live frame
and **6 during the outage** (three boat-containing and three zero-detection
frames). Earlier diagnostic runs account for the pre-existing records.
The shaper was set to **0 → 64,000 → 16,000,000 bits/s**; its raw control
log is [step3-priority/shaper-log.txt](step3-priority/shaper-log.txt).

| Native counter | Before | During outage | After recovery |
|---|---:|---:|---:|
| Edge stored items | 9 | 15 | 15 |
| Edge completed items | 9 | 9 | 15 |
| Edge acknowledged manifests | 9 | 9 | 15 |
| Edge heartbeat queued items | 0 | 6 | 0 |
| Edge heartbeat queued bytes | 0 | 1,278 | 0 |
| Edge unmanifested items | 0 | 6 | 0 |
| Edge consecutive failures | 0 | 2 | 0 |
| Edge acknowledged bytes sent | 1,865,622 | 1,865,622 | 3,076,788 |
| Shore received items | 9 | 9 | 15 |
| Shore completed items | 9 | 9 | 15 |
| Shore received bytes | 1,865,622 | 1,865,622 | 3,076,788 |

Native numeric sources are authenticated `ConsoleService/GetStatus` on edge
and shore and `GetQueue` on edge, plus the durable `/api/v1/items` listing.
The shore RPC uses an owned local port-forward: it is intentionally **not**
exposed on the public shore gateway. Protobuf-omitted zeros are interpreted
as zero in this table. `shore.bytesSent` is the API field name; the shore
implementation populates it from `Edges.BytesReceived`.

The heartbeat/GetQueue byte count covers the **next unfinished eligible
form per item**, not all full-image bytes waiting behind those forms. Thus
1,278 bytes is not an assertion that all six JPEGs total only 1,278 bytes.
The eventual acknowledged-byte increase was **1,211,166 bytes** on both
ends. During-queue entries, annotated with their **final** shore tiers:

| Final shore tier | Pending items | Next-form bytes during outage |
|---|---:|---:|
| P1 | 1 | 240 |
| P2 | 1 | 301 |
| P3 | 2 | 405 |
| P4 | 2 | 332 |

This is an offline join, not a historical shore tier during disconnection;
see `backlog-by-final-tier.json` and `during-queue.json` for the raw values.

Edge last-success stayed at **1793983398588** during the outage and advanced
to **1793983422748** after recovery. Shore last-success stayed at
**1793983398582** and advanced to **1793983422740**. Shore count/bytes did
not change during the outage. All six later arrived and completed, proving
that the shaper was in the actual sync path rather than bypassed.

**Low-rate limitation:** 64 kbps was held for approximately 15 seconds, but
the low-rate snapshot still showed no new completed exchange. First new
shore receipts were at **16:43:41.125Z**, after the rate was raised at
**16:43:40Z**. This run proves outage/recovery and priority ordering, not
sustained successful full-payload transfer at 64 kbps. Snapshots call several
APIs sequentially and are not atomic; a zero `GetQueue` preview while an
exchange is in flight must not be mistaken for completed backlog drain.

### Durable SSE first-arrival order

The final stream starts at existing health cursor **57**, **before** the new
POSTs, avoiding replay of older events. It stays on the shore TLS gateway
throughout. The six outage IDs first appeared in this actual SSE order:

| SSE cursor | Source suffix | Raw edge score | Tier at first captured event |
|---:|---|---:|---|
| 67 | outage-2 | 0.6362234777 | **P1** |
| 70 | outage-4 | 0.6304819172 | P2 |
| 74 | outage-5 | 0.4386671413 | P3 |
| 75 | outage-1 | 0.1999975510 | P3 |
| 76 | outage-3 | 0.1999975027 | P4 |
| 79 | outage-6 | 0.1999974827 | P4 |

The first posted outage frame was `outage-1`; the later, higher-scored
`outage-2` arrived first. First-arrival scores are descending. The scheduler
sorts **manifests** by descending priority (`edge/scheduler/scheduler.go`),
then interleaves payload forms using ladder weighting/priority per byte.
This evidence does **not** claim that an entire P1 full image completed
before any P2 metadata. The API's durable events include updates and
relative-tier changes; only each new backlog ID's first event is counted.

Raw and derived evidence:

* [step3-priority/shore-stream.sse](step3-priority/shore-stream.sse)
* [step3-priority/backlog-arrival-order.json](step3-priority/backlog-arrival-order.json)
* [step3-priority/counter-timeline.json](step3-priority/counter-timeline.json)
* [step3-priority/result.json](step3-priority/result.json)
* `step3-priority/{before,during,low-rate,after}-{edge-status,shore-status,queue,edge-items,shore-items}.json`
* [step3-priority/gateway-access.log](step3-priority/gateway-access.log):
  actual HTTP/2 SyncService requests with `grpc_status=OK`, API access and
  gateway routing; no Authorization headers.

## Operator screenshots

Playwright connected to Chrome CDP at `http://localhost:29229`, opened
`https://ghost-seeker.uds.dev:9443`, typed the operator token directly from
its file into the password input, and captured only authenticated screens.
The three PNGs were visually inspected:

* [img/shore-feed.png](img/shore-feed.png): 15 uploaded records, one P1 and
  live maritime images; no queued/uploading records.
* [img/shore-item-detail.png](img/shore-item-detail.png): the new P1 cargo
  vessel image and its actual source/time.
* [img/shore-link-sync-status.png](img/shore-link-sync-status.png): measured
  ship-to-shore estimate **1.30 MB/s**, **2.93 MiB** transferred (UI labels
  it MB), and **15** shore records.

Two UI caveats are recorded rather than hidden: `On ship —` is expected in
the shore-only operator deployment, whose manifest explicitly sets `-edge=`;
native ship queue counters are captured independently above. Also, the UI
labels all 15 ungrouped image records as “detected objects”; the API actually
has **9 records with detections, 11 total detection entries**. No detection
accuracy or object-identity claim is made from that UI label. UI card scores
are normalized presentation scores, not the raw scheduler scores above.

## Reproduce and checks

Prerequisites: the Step 2 deployment, running shaper, archives already on
disk, curl with HTTP/2 support, Python, and Playwright for screenshots.
Playwright was installed with `python3 -m pip install --user playwright`;
the existing Chrome/CDP instance was reused, not replaced or stopped.

```sh
# Choose a new directory; the helper refuses to overwrite evidence.
EVIDENCE_DIR="$PWD/docs/reports/uds-validation/new-run" \
  python3 -B scripts/uds/two-cluster-evidence.py
EVIDENCE_DIR="$PWD/docs/reports/uds-validation/new-run" \
  python3 -B scripts/uds/two-cluster-screenshots.py
```

The capture helper opens/cleans up its own edge/shore service port-forwards
on 18083/18084, opens `curl -N` SSE before ingest, captures statuses/JSON,
checks that shore did not grow during the outage, verifies drain and
descending/P1-first SSE order, and restores 16 Mbps even on failure. It uses
real auth from `STATE/auth/operator-token` (default state outside the repo).

The selected files must not already exist in edge's hash-dedup store. For
repeat captures on retained data, set `INPUT_OFFSET` to select disjoint
archive members, or use a separately provisioned empty application dataset.
The helper rejects a duplicate rather than presenting it as new ingestion;
different images/model scores or an existing higher-ranked feed can make
the P1-first assertion legitimately fail. It does not clear production data
or manipulate scores to make the assertion pass. Existing final evidence
and the 15 stored records have been preserved.

Checks run:

```text
Evidence capture completed: docs/reports/uds-validation/step3-priority
"accepted_frames": 7
"outage_frames": 6
"shore_before": 9
"shore_during": 9
"shore_after": 15
"first_backlog_tier": "P1"
"priority_order_verified": true
```

Unit tests cover first-event ordering/coalesced duplicates, empty streams,
and malformed JSON. No production code was changed or rebuilt in Step 3;
the full suites/prod-gate remain unrun. The final shaper remains running at
16 Mbps. Ports 8105/8106/8109 and pre-existing servers were untouched.

### Failures and corrections retained as evidence

* Initial capture got a 404 for public `ConsoleService/GetStatus`. The
  gateway deliberately exposes `/api/v1/` and sync, not console RPCs. The
  harness now port-forwards shore for counters; no gateway policy was weakened.
* Initial mixed backlog had no new P1. The successful run selected a naturally
  higher-confidence frame as documented above, retaining the first run.
* Screenshot automation initially used an unprefixed item ID, then selected
  a hidden legacy Details link. It now uses UI `item:` IDs and the real
  `#details` route. Only the affected status capture was repeated after the
  latter correction. No application code was changed for screenshots.

## Dataset and image licensing

Inputs are existing real captures in `frames/SMD_VIS_Onboard_frames.zip`
and `frames/30_Days_at_Sea_Timelapse_frames.zip`, **not demo-only generated
imagery**. Their archive integrity/provenance is in `frames/manifest.json`.
JPEGs were extracted to private external harness state; no extra source
JPEG copies were added here. `inputs.json` records exact members and hashes.

**License status is unresolved, not CC/public domain.** Repository
`edgeml/reports/DATA_CLEANUP.md` records SMD as unknown/academic-research use,
quarantined for evaluation, and the timelapse license as unknown. These
captures are only internal deployment/transport evidence, not an accuracy
benchmark or a claim that SMD is cleared for evaluation. The screenshots
contain derived views of that imagery: **do not publish or redistribute
the screenshots or original imagery until the upstream permissions are
resolved**. This validation does not grant any new image rights.

## STEP 4: initial air-gap run (b0b3776)

The ship app restarted and was removed/reinstalled from its local bundle with
internet egress blocked. Evidence is in [airgap/](airgap/). GHCR (pinned IP,
normal TLS verification) and direct `https://1.1.1.1` timed out; external DNS
failed. Shore remained reachable through the shaper with HTTP/2 and verified
TLS (`HTTP 401 protocol=2 verify=0` without authentication).

DOCKER-USER alone was insufficient: Docker redirects the node's nominal
gateway DNS to `127.0.0.11`, whose host-side resolver bypasses that chain.
`two-cluster-airgap.sh` therefore also installs tagged host INPUT restrictions
and tagged OUTPUT drops for Docker's embedded DNS in the ship containers'
network namespaces. Kubernetes service DNS remains functional. Docker-name
resolution in those containers is intentionally unavailable while blocked.
`off` removes only the exact tagged rules and empty harness chains.

Both restarted pods became 2/2 Running; packaged models passed checksum checks
and loaded offline. Real SMD frames 23 and 25 (zero-based sorted JPEG indices)
were accepted, scored and synchronized, with boat confidences **0.77753** and
**0.80137**. The three app image references were evicted from containerd before
fresh deployment; [redeployed-events.log](airgap/redeployed-events.log) records
pulls from `127.0.0.1:31999`, not an external registry. Core/Istio images stayed
cached: this was not an empty-node bootstrap test. Pepr and Zarf admission
worked; no external image-pull failure was observed.

Zarf removed the app workloads but retained its namespace, auth and PVCs. A
harness assertion incorrectly expected namespace deletion and was corrected;
the offline redeploy resumed with those PVCs retained. Earlier DNS/control
attempts are retained in `airgap/attempt*/` and `run-before-redeploy.log`.
No UDS Core package was removed. At 64 kbps, successful heartbeat exchange
timestamps advanced during **80.4 seconds**; no queued payload remained at the
start, so that observation is not a low-rate payload-drain claim.

Final state: **blocking OFF**, Docker DNS unblocked in both ship containers,
shaper at **16 Mbps**. Reproduce (removes/reinstalls the ship app package):
`AIRGAP_REDEPLOY=1 python3 -B scripts/uds/two-cluster-airgap-verify.py`.
Use fresh app input state or `AIRGAP_FRAME_OFFSET` to avoid duplicate frames.
