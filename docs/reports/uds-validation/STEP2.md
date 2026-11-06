# Two-cluster production UDS validation — Step 2

## Revision and scope

Branch `devin/1793981091-uds-two-cluster` was rebased onto `origin/prod-uds`
at `b0b37760795018ff5b4edf7a2f26905db924d464` after PR #269 merged. The
existing Core 1.16.0 clusters were reused, not recreated. No commit or push
was made. The final deployment uses the **split** shore and ship packages.

Production image tag: `prod-b0b377607950-20261106163143-7d98ece6` for
`ghost-seeker/{shore,operator,edge,edgeml-sidecar,edgeml-models}`. The Go
Dockerfile uses `-tags prod`. The sidecar build uses `EDGEML_TRACKING=1`
(Docker build argument `WITH_TRACKING=1`). Image inspection returned:

```text
tracking import OK
COLREGS True True
```

Models were fetched into the private harness build state. The existing model
packager includes both `colregs_lights.json` and `colregs_night_config.json`.
The final builds used mirror.gcr.io bases proactively; no Docker Hub 429 was
observed. Image build/package/bundle/deployment logs are in `logs/` with the
unique tag in their filenames. Earlier non-split attempt logs are retained
as historical diagnostics, not final deployment evidence.

## Reproduce

Prerequisites are the Step 1 tools/clusters and the raised inotify limits.
The scripts default to state outside the repository at
`~/.local/share/uds-two-cluster`. Go lives at `~/sdk/go`; `go` and `gofmt`
also have symlinks in `~/.local/bin` because fresh execution shells did not
retain an exported Go PATH. No dependency maintenance was necessary.

```sh
export PATH="$HOME/sdk/go/bin:$HOME/.local/bin:$PATH"
scripts/uds/two-cluster-link.sh start
scripts/uds/two-cluster-deploy.sh
kubectl --context k3d-uds-shore rollout status statefulset/shore -n ghost-seeker-shore
kubectl --context k3d-uds-ship rollout status statefulset/edge -n ghost-seeker-ship
python3 scripts/uds/two-cluster-status.py
go run ./scripts/uds/sync-probe \
  -token-file "$HOME/.local/share/uds-two-cluster/auth/unit-token"
scripts/uds/two-cluster-link.sh status
```

The executed final build/deploy was separated into build-only and deployment
phases (equivalent to the default combined invocation):

```sh
TAG=prod-b0b377607950-20261106163143-7d98ece6 BUILD_ONLY=1 scripts/uds/two-cluster-deploy.sh
TAG=prod-b0b377607950-20261106163143-7d98ece6 SKIP_BUILD=1 scripts/uds/two-cluster-deploy.sh
```

Omit `TAG` for a new timestamp/SHA/random tag on each build. `SKIP_BUILD=1`
is only for an already-built tag, not rebuilding changed sources under the
same tag. `STATE`, `LOG_DIR`, contexts, namespaces, and `LINK_IP` are
parameterized. The link wrapper separately accepts `LINK_PORT`, `LINK_TARGET`,
and initial `BPS`; the split deployment intentionally uses gateway port 443.
It needs passwordless `sudo -n setcap` for a privileged listener. Only the
host harness binary receives `cap_net_bind_service`; pods remain unprivileged.

The deployment script stages `deploy/zarf` and `bundles/prod-uds` outside
the repository, replaces application image references with the unique tag,
then creates/deploys `deploy/zarf/{shore,ship}` and
`bundles/prod-uds/{shore,ship}`. It uses Zarf package variables via `--set`
and role-specific `UDS_CONFIG`, not bundle Helm-only overrides:

* Shore: `ghost-seeker-shore.NAMESPACE=ghost-seeker-shore`; default gateway
  hosts `shore` and `ghost-seeker` expose shore/operator respectively.
* Ship: `ghost-seeker-ship.NAMESPACE=ghost-seeker-ship`, `FRAMES_SOURCE=pvc`,
  `SHORE_URL=https://shore.uds.dev:443`, `SHORE_EGRESS_CIDR=172.19.0.1/32`,
  `SHORE_EGRESS_PORT=443`.
* A harness-only StatefulSet `hostAliases` patch resolves `shore.uds.dev`
  to `172.19.0.1` on edge, without editing production manifests or changing
  shore URL/TLS identity. Shore's existing public hosts include `shore.uds.dev`.

Auth reuses the local generator called by `tasks-dev.yaml`
(`deploy/zarf/dev_auth.py`), routing its Secrets to the split namespaces and
contexts before deploying packages. `auth/{signing-key,operator-token,unit-token}`
are outside the repo, mode 0600, with a mode-0700 directory. The signing key
validates real HS256 unit tokens; production auth is not bypassed. Existing
complete auth files are reused on later deployments; partial state is rejected.
The generated unit token expires after 30 days. Do not remove/rotate auth files
while workloads still use the old credentials. No credential values are in
this report, argv, or logs.

## Topology and final workloads

```text
edge on k3d-uds-ship / ghost-seeker-ship
  HTTPS shore.uds.dev:443 (hostAliases -> 172.19.0.1)
  -> host TCP shaper 172.19.0.1:443
  -> 127.0.0.1:9443 (shore published HTTPS)
  -> shore tenant gateway -> shore:8080 on k3d-uds-shore
```

Both UDS Packages are `Ready`. Final pod snapshots:

```text
shore context: k3d-uds-shore, namespace: ghost-seeker-shore
operator-7fbc757b9-mgdxt   2/2 Running 0 restarts
shore-0                    2/2 Running 0 restarts

ship context: k3d-uds-ship, namespace: ghost-seeker-ship
edge-0                           2/2 Running 0 restarts
edgeml-sidecar-86cb8786b-v2jqz   2/2 Running 0 restarts
```

There is no ship application workload on shore and no shore application
workload on ship. All-namespace snapshots are in `split-{shore,ship}-pods.txt`.

## TLS, HTTP/2, trailers and observed sync

The authenticated probe uses the application's Connect link implementation,
normal certificate/hostname validation, and an HTTP/2-only transport. It dials
the shaper IP while preserving `shore.uds.dev` TLS identity. Final output:

```json
{"alpn":"h2","httpStatus":200,"protocol":"HTTP/2.0","verifiedChains":2}
{"grpcStatus":"0"}
{"link":{"up":true,"estimate_bps":64000,"last_success_unix_ms":1793983057179},"success":true}
```

This is supplemented by **actual edge traffic**, not just a host probe. A
harness-only EnvoyFilter adds a credential-free access log at shore's tenant
gateway, recording decoded gRPC trailer status. An observed edge record:

```text
UDS_SYNC method=POST path=/ghostseeker.v1.SyncService/Exchange protocol=HTTP/2 grpc_status=OK status=200 agent=grpc-go-connect/1.18.1 (go1.26.10)
```

The existing gateway access log additionally shows authority
`shore.uds.dev:443` and route `.ship-sync`. `observed-sync-counts.json` counts
only `grpc-go-connect` edge requests in the captured logging window:

```json
{"source":"shore gateway observed edge exchanges in captured window","success":29,"other":0}
```

The probe has a distinct `uds-harness-sync-probe` user agent and is excluded
from this count. This is a **gateway-observed window count**, not a native
cumulative application metric. The application's current status API exposes
last-success/link state and consecutive failures, but no cumulative success
counter; no production code was changed to add one. Final native status:

```json
{"role":"ship","link":{"consecutiveFailures":"0","estimateBps":143790.18957195885,"lastSuccessUnixMs":"1793983046806","up":true},"prioritizer":"edgeml"}
{"role":"shore","link":{"estimateBps":143790.18957195885,"lastSuccessUnixMs":"1793983046804","up":true}}
```

These lines are shortened from `final-sync-status.txt`; the status helper
normalizes protobuf's omitted zero-valued edge failure field to `"0"`.
No live sensor frames were supplied. This verifies authenticated heartbeat/
exchange synchronization, not prioritized detection payload delivery or
inference accuracy. `FRAMES_SOURCE=pvc` is set without introducing synthetic
or replay input.

## Runtime outage and recovery

```sh
scripts/uds/two-cluster-link.sh set-rate 0
# Allow the edge's exchange timeout/probe schedule to observe the outage.
python3 scripts/uds/two-cluster-status.py
scripts/uds/two-cluster-link.sh status
scripts/uds/two-cluster-link.sh set-rate 16000000
# Allow the next scheduled exchange to recover.
python3 scripts/uds/two-cluster-status.py
```

Observed: `bps=0`, byte counter held at `19828`, edge consecutive failures
rose to `2`, and its protobuf-default `up=false` was omitted. Restoring
16 Mbps recovered `up=true`, advanced the last-success timestamps, and reset
consecutive failures to `0`. The recovered byte counter rose to `29326`,
then `55382` in the final snapshot. This also discriminates against a direct
bypass route: disabling the shaper broke edge exchanges and restoring it
recovered them. The shaper is **left running at 16,000,000 bits/s**.

The shaper wraps `internal/shaper` as a raw TCP proxy, so it never terminates
TLS. Its runtime API is a local mode-0600 Unix socket, not an exposed TCP
control endpoint. The stop wrapper verifies the PID's executable before
signaling it. No pre-existing servers or ports 8105, 8106, 8109 were touched.

## Changes and checks

Only harness/report files were added. No production application, deployment
manifest, shared configuration, or CI files were edited:

* `scripts/uds/two-cluster-deploy.sh`: image/package/bundle build and split deployment.
* `scripts/uds/two-cluster-auth.py`, `test_auth.py`: private reusable auth and tests.
* `scripts/uds/two-cluster-link.sh`, `link-shaper/{main.go,main_test.go}`:
  owned host TCP proxy and runtime rate API.
* `scripts/uds/sync-probe/{main.go,main_test.go}`: TLS/HTTP2/trailer evidence.
* `scripts/uds/two-cluster-status.py`: authenticated status via owned port forwards.
* `scripts/uds/gateway-access-log.yaml`: harness-only access logging, currently
  applied to the shore tenant gateway. Istio warns that EnvoyFilter depends
  on internal implementation details; this diagnostic is not part of either
  production bundle. Remove with `kubectl --context k3d-uds-shore delete
  envoyfilter uds-validation-sync-log -n istio-tenant-gateway` when finished.
* `docs/reports/uds-validation/STEP2.md` and logs: final evidence; Step 1 files
  remain from the previous handoff.

Checks run (no full-suite/prod-gate claim):

```text
go test ./scripts/uds/link-shaper ./scripts/uds/sync-probe
ok  github.com/fleethackerghostfleet/ghost-seeker/scripts/uds/link-shaper (cached)
# After adding probe tests:
go test ./scripts/uds/sync-probe ./internal/shaper ./edge/link
ok  github.com/fleethackerghostfleet/ghost-seeker/scripts/uds/sync-probe 0.003s
ok  github.com/fleethackerghostfleet/ghost-seeker/internal/shaper (cached)
ok  github.com/fleethackerghostfleet/ghost-seeker/edge/link (cached)
python3 -B -m unittest discover -s scripts/uds -p 'test_*.py'
Ran 3 tests in 0.007s
OK
PASS: shell syntax and Python compilation
```

The helper's auth tests cover generation/routing/reuse, partial-state failure,
and rejection of repository-local credentials. The proxy control tests cover
normal, missing/invalid input, zero-rate outage, restore, method and path errors.
The probe tests cover TLS responses, missing TLS, and transport errors.
No UI was changed or visually verified. Full `make test`, web tests,
`make verify`, and the mandatory pre-PR `make prod-gate` remain unrun.
