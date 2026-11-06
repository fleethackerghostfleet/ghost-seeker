# rc2 fresh-install runbook findings

Runbook under test: `docs/UDS_DEPLOY.md` at `origin/prod-uds` **71f42d9**
(#272), release **uds-v0.1.0-rc2**, metadata version **0.1.0-rc2**.
Only the four amd64 app bundles/packages and SHA256SUMS were downloaded.
No rc1 asset, locally built app image, or older app package was used.

## Findings / limitations

| Step | What it says | Actual observation | Applied fix / disposition |
| --- | --- | --- | --- |
| §4, release checksum example | Fetch files listed by SHA256SUMS and run `sha256sum --check SHA256SUMS`. | The published manifest includes `sbom/ship/...` and `sbom/shore/...` paths, whereas release asset names are flattened/renamed; it also lists metadata not requested in this test. A blanket check of only the four downloaded artifacts would necessarily report missing files. | Per the test brief, selected the four **exact unchanged artifact lines** into `SHA256SUMS.artifacts` and ran `sha256sum --check` in the download directory. All four passed before any cluster deletion. Published manifest and actual asset names are preserved under `logs/`; no SBOM or provenance verification is claimed. |
| §10, one-cluster laboratory example | Uses `uds-runbook`, one API/HTTP/HTTPS triplet, and assigns shore and ship to the same context. | That exact example cannot provide two distinct clusters on one VM, and repeated host port bindings would collide. | Two separate Core config files: `uds-shore` 6551/9080/9443 and `uds-ship` 6552/10080/10443. Contexts are `k3d-uds-shore` / `k3d-uds-ship`. Old clusters were explicitly deleted first. |
| §10, Docker subnet selection | Does not specify a Docker network/subnet. | The existing harness requires shore 172.18.0.0/16 and ship 172.19.0.0/16; automatic address-pool reuse after deleting clusters is not a runbook guarantee. | Created two empty Docker bridges with those exact subnets and configured Core's `uds-k3d-dev.K3D_NETWORK` to join them. New clusters still have fresh nodes, registries, app namespaces and PVCs. This is a laboratory harness addition, not a fleet requirement. |

The four config files actually passed through absolute `UDS_CONFIG` paths are
copied into `logs/`. They retain the runbook's split variable namespaces and
Core/app separation. App config uses development `DOMAIN=uds.dev` to match the
laboratory Core certificate; it is not a fleet PKI recommendation.

## Deliberate harness-only additions (not runbook defects)

- Stopped the owned shaper before deleting both owned clusters. No shared
  servers or ports 8105/8106/8109 were stopped or rebound.
- Existing host inotify limits (1024 instances / 1048576 watches, established
  during Step 1) were retained and checked. Default-limit failure did not recur
  in this fresh run; no new sysctl change was made.
- Added `--no-color --no-progress --no-log-file` to UDS commands solely for safe
  redacted transcript capture. Credential-related output/table rows are
  redacted; no insecure/skip-signature options were added.
- Followed §3's OpenSSL and HS256 JWT generation, with `EDGE_ID=ship-001`,
  `role=unit` and one-day expiry. Values persist only in the new private
  external state directory, not the transcript. Secret creation uses the
  runbook's `--from-file` approach with private files instead of process
  substitution. Shore and edge use **different** operator tokens; the capture
  helpers were adapted to respect that supported runbook configuration.
- §10's source-build/package commands were intentionally not executed: this is
  the §4 **release-artifact** path, not a source-build validation.
- Host TCP shaper at `172.19.0.1:443` forwards to `127.0.0.1:9443`; the edge
  StatefulSet hostAlias maps `shore.uds.dev` to that gateway. App config narrows
  `SHORE_EGRESS_CIDR` to `172.19.0.1/32`. These are needed for the laboratory
  degraded link and are separate from the release manifests.
- A shore tenant-gateway EnvoyFilter enables HTTP/2/gRPC-trailer access logs.
  EnvoyFilter is an implementation-sensitive diagnostic (Core itself also
  emits the standard warning); it is not a required fleet application change.
- ConsoleService counters use owned diagnostic port-forwards because they are
  intentionally not public gateway routes. API/SSE checks still use verified
  TLS/HTTP2 through the gateway. SSE remains open for 40 seconds before ingest,
  as §5 requests.
- Air-gap controls include DOCKER-USER forwarding restrictions, host INPUT
  restrictions and ship-network-namespace OUTPUT drops for Docker's embedded
  resolver. DOCKER-USER alone does not block host-proxied DNS. All rules are
  tagged and removed after the check. Only app pods are restarted; no rc2
  package removal/redeployment or local application build is part of that check.

## Audit trail

- `logs/00-startup-transcript.txt`: protective local commit, fetch/rebase and
  exact rc2 download invocation.
- `logs/command-transcript.log`: executed bootstrap, auth, deployment and
  verification commands with redacted output and exit codes. The auth script
  body is recorded without expanded secret values; tracing is disabled.
- `logs/SHA256-verification.txt`, `SHA256SUMS.published`,
  `SHA256SUMS.artifacts`, `release-asset-names.txt`: release integrity evidence.
- Old/new default-namespace UIDs and pre-app node image inventories show the
  replacement clusters are fresh, not reused application installations.
