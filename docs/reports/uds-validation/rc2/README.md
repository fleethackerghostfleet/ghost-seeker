# rc2 fresh-install results

Validated **uds-v0.1.0-rc2**, with `docs/UDS_DEPLOY.md` from **71f42d9**.
The previous harness/evidence was committed locally as **736bebf**, then rebased
onto that revision (rebased safety commit **aadb050**). No push was made.

## Integrity and clean installation

Downloaded only the four amd64 bundle/package assets and SHA256SUMS with
`gh release download uds-v0.1.0-rc2` into `~/.local/share/uds-rc2/`.
Exact published named-entry verification output:

```text
uds-bundle-ghost-seeker-ship-amd64-0.1.0-rc2.tar.zst: OK
uds-bundle-ghost-seeker-shore-amd64-0.1.0-rc2.tar.zst: OK
zarf-package-ghost-seeker-ship-amd64-0.1.0-rc2.tar.zst: OK
zarf-package-ghost-seeker-shore-amd64-0.1.0-rc2.tar.zst: OK
```

This verifies integrity against the release's published hashes, not signatures
or SBOM provenance. SBOMs were not downloaded; their renamed release filenames
are why the full manifest was not checked indiscriminately. See
[findings](RUNBOOK_FINDINGS.md) and `logs/SHA256SUMS.published` for details.

Stopped the old owned shaper, turned blocking off, deleted **both** old clusters,
and installed Core 1.16.0 on new nodes using §10's OCI bootstrap. Old/new
default-namespace UIDs and pre-app `crictl images` inventories are in `logs/`;
the replacement nodes had no Ghost Seeker images before release deployment.
Shore then ship were deployed from the verified **release bundles**, using
absolute `UDS_CONFIG` paths. Standalone Zarf packages were verified, not separately
deployed. No app image build, package create, old app tarball, or rc1 was used.
The host Go link-shaper wrapper is harness tooling, not an application image.

The installed image tag is the release's
`prod-c74106e2509c-20261106165828-258bab3e`, not either earlier local-build tag.
`logs/*-runtime-image-ids.log` records registry-prefixed references and digests.
Both split Packages became Ready; final snapshots show **25/25 Running and
Ready per cluster**, including shore/operator and edge/inference respectively.

## Authentication, transport and live input

Fresh §3 credentials use distinct shore/edge operator values and an HS256 unit
JWT (`sub=ship-001`, `role=unit`, one-day expiry). Files live only beneath the
new external state directory: auth directory 0700, all four files 0600. The
signing key is provisioned only to shore, never ship. No dev-auth helper ran.

The harness path is:

```text
edge -> shore.uds.dev:443 -> 172.19.0.1:443 TCP shaper
     -> 127.0.0.1:9443 -> shore tenant gateway -> shore
```

Gateway `/api/v1/items` checks, with curl HTTP/2 and real TLS verification:

```text
HTTP 401 protocol=2
HTTP 200 protocol=2
```

Gateway logs in `logs/shore-gateway-access.log` show actual edge
`/ghostseeker.v1.SyncService/Exchange` requests using **HTTP/2** with
**grpc_status=OK**, status 200. The diagnostic EnvoyFilter is harness-only.
Native status captures use owned local port-forwards, not a public
ConsoleService route. There is no native cumulative successful-exchange counter.

SSE stayed open for **40 seconds before ingest** and subsequently delivered
the live events on that same stream. The first live JPEG returned 202, was
scored with a real boat detection (**0.80814** confidence), and arrived as P1
in the initially empty shore. A naturally higher-confidence outage frame
(**0.87247**) became the new P1; no ranking policy/database manipulation occurred.

## Outage and priority recovery

| Counter | Before | During bps=0 | After 16-Mbps recovery |
| --- | ---: | ---: | ---: |
| Edge stored items | 1 | 7 | 7 |
| Edge complete items | 1 | 1 | 7 |
| Edge queued items | 0 | 6 | 0 |
| Consecutive failures | 0 | 2 | 0 |
| Edge acknowledged bytes | 309,721 | 309,721 | 1,520,887 |
| Shore received items | 1 | 1 | 7 |
| Shore received bytes | 309,721 | 309,721 | 1,520,887 |

Six items accumulated without shore growth. Recovery acknowledged
**1,211,166 additional bytes** and completed all six. Missing protobuf zero
fields are interpreted as zero. Native process counters reset on pod restart;
do not compare pre/post-restart edge byte counters as cumulative totals.

Actual first-arrival SSE cursors for the backlog:

| Cursor | Tier | Edge-reported score |
| ---: | --- | ---: |
| 9 | P1 | 0.6362238259 |
| 10 | P2 | 0.6304804428 |
| 11 | P3 | 0.4386665164 |
| 13 | P4 | 0.1999975076 |
| 16 | P4 | 0.1999973668 |
| 17 | P4 | 0.1999971364 |

This proves prioritized **manifest arrival**, not strict whole-image completion
order. The 64-kbps leg lasted 15 seconds without completing an exchange;
successful full drain occurred after restoring 16 Mbps. The earlier local
180.9-second low-rate run is supporting evidence only, not an rc2 result.

## Offline restart

Before blocking, the ship node reached GHCR (HTTP/2 401), 1.1.1.1 (HTTP/2 301),
both with TLS verify=0, and resolved external DNS. With tagged restrictions on,
GHCR/direct external HTTPS timed out and external DNS failed, while verified
TLS/HTTP2 shore access remained available through the shaper.

Deleted edge and inference app pods returned **2/2 Running**. Packaged model
checksums and offline model loading succeeded. Another real JPEG returned 202,
scored boat **0.77753**, and synchronized completely: **8/8** shore items,
**1,677,709** shore acknowledged bytes. This phase tests cached/local-registry
**restart**, not fresh offline package reinstall or deliberate image-cache eviction.

Final state: **OFF**, both node/LB Docker DNS paths **UNBLOCKED**, and host
`iptables -S` identical before/after. Shaper: **16 Mbps**. Evidence is in
`airgap/` and final status snapshots in `logs/`.

## Screenshots, reproduction and limits

Visually verified:
- `img/shore-feed.png`: eight uploads, real imagery and one P1.
- `img/shore-item-detail.png`: P1 vessel detail.
- `img/shore-link-sync-status.png`: shore records and transfer/link status.

The shore-only UI cannot display native ship queue counters. Its “detected
objects” label counts records, not actual detector entries; authoritative
detections and counters are in the API captures.

Driver: `scripts/uds/two-cluster-rc2.py` phases `bootstrap`, `deploy`, `evidence`,
`airgap`, `screenshots`. Bootstrap is destructive to these two **owned** clusters;
perform the download/named checks first, and copy the non-secret config profiles
in `logs/` to `~/.local/share/uds-rc2/config/`. The driver stores new private state
separately from all earlier local-build runs. Command/output transcript, auth
script body without expanded values, configs, deployment logs and checksums are
under `logs/`. Harness Python tests: **16 passed** (`logs/harness-tests.txt`).

Real input provenance/hashes are captured in `evidence/inputs.json` and
`airgap/restarted-offline-input.json`. Sources are the existing SMD onboard and
maritime timelapse archives in this checkout. Licensing remains unresolved:
SMD is unknown/academic-research/quarantined per
`edgeml/reports/DATA_CLEANUP.md`; timelapse permissions are unknown. Do not
publicly redistribute these derived screenshots without resolving permissions.
This is operator-directed live API ingestion, not a certified evaluation
dataset, inference accuracy claim or fleet certification. At the STEP 5 handoff,
production prod-gate and full application suites had not been rerun. STEP 6 PR
preparation subsequently passed `make test` (including vet), all 365 web tests,
`make prod-gate`, and all 16 harness Python tests. Full outputs are external to
the repository in `~/.local/share/uds-pr-prep/`. No runtime blocker remains.
