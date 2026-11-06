# Two-cluster bootstrap (2026-11-06)

Base: `origin/prod-uds`, `b6e0f0fa18d1e71d2e93d5d516219eb7776fdd7d`.
Branch: `devin/1793981091-uds-two-cluster` (maintainer-approved exception
to the usual origin/dev branch base). No commit or application deployment yet.

## Tools

Installed release binaries in `~/.local/bin`: k3d v5.8.3, uds-cli v0.41.0,
Zarf v0.89.0, kubectl v1.37.1 (matching the bundle's k3s v1.37.1).
Go 1.26.10 is in `~/sdk/go`; export `PATH="$HOME/sdk/go/bin:$HOME/.local/bin:$PATH"`
for later application builds. The snapshot did not contain the blueprint's Go
installation; its absence was checked, not merely worked around through PATH.

Release downloads used:

```sh
curl -fLsS https://github.com/k3d-io/k3d/releases/download/v5.8.3/k3d-linux-amd64 -o ~/.local/bin/k3d
curl -fLsS https://github.com/defenseunicorns/uds-cli/releases/download/v0.41.0/uds-cli_v0.41.0_Linux_amd64 -o ~/.local/bin/uds
curl -fLsS https://github.com/zarf-dev/zarf/releases/download/v0.89.0/zarf_v0.89.0_Linux_amd64 -o ~/.local/bin/zarf
curl -fLsS https://dl.k8s.io/release/v1.37.1/bin/linux/amd64/kubectl -o ~/.local/bin/kubectl
chmod +x ~/.local/bin/{k3d,uds,zarf,kubectl}
mkdir -p ~/sdk
curl -fLsS https://go.dev/dl/go1.26.10.linux-amd64.tar.gz | tar -xz -C ~/sdk
```

## Bootstrap

```sh
sudo sysctl -w fs.inotify.max_user_instances=1024 fs.inotify.max_user_watches=1048576
scripts/uds/two-cluster-up.sh
```

The sysctl change is runtime-only, not persisted across VM reboots. The script
checks the instance limit and gives the command rather than elevating itself.
The original host limit of 128 caused the second cluster's Istio CNI to exit:

```text
installer failed: create CNI config file: watcher create: couldn't initialize inotify: too many open files
```

The node's file descriptor limit was 1048576 and host file-nr was only 10528,
so ordinary file descriptor exhaustion was checked as an alternative. Raising
the inotify limits and restarting the ship CNI restored readiness. Pepr had
also failed to reach its API while its ambient mesh was incomplete. Restarting
Pepr after ztunnel became ready restored its admission and watcher deployments.
The first bootstrap then failed its fast Pepr admission probe before those
restarts completed. Recovery did not delete or recreate either cluster:

```sh
kubectl --context k3d-uds-ship rollout restart daemonset/istio-cni-node -n istio-system
kubectl --context k3d-uds-ship rollout restart deployment -n pepr-system
# Wait for both Pepr deployments to roll out, then select the ship context.
kubectl config use-context k3d-uds-ship
uds deploy ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-slim-dev:1.16.0 --confirm --no-progress --no-color --no-log-file --packages core-base
uds deploy ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-slim-dev:1.16.0 --confirm --no-progress --no-color --no-log-file --packages core-identity-authorization
```

Run deployment commands through the script's credential-filtered logger, not
directly to a shared transcript: the upstream bundle can print credentials.
`TARGET=ship RESUME_CORE=1 scripts/uds/two-cluster-up.sh` implements this
non-destructive package recovery. Separate package invocations are intentional:
the attempted invocation with two repeated `--packages` flags deployed only
core-base in this run; the CLI's underlying selection behavior was not investigated.

Fresh bootstrap uses these exact package-scoped overrides (no K3D_EXTRA_ARGS
needed; confirmed in the published bundle's `uds inspect --list-variables`):

| Role | CLUSTER_NAME | K3D_API_PORT | K3D_HTTP_PORT | K3D_HTTPS_PORT |
| --- | --- | --- | --- | --- |
| shore | uds-shore | 6551 | 9080 | 9443 |
| ship | uds-ship | 6552 | 10080 | 10443 |

Each flag is `--set uds-k3d-dev.VARIABLE=value`. The default network creation
is already cluster-name scoped; K3D_NETWORK remains unset. All names and ports,
LOG_DIR and BUNDLE are environment-parameterized. TARGET defaults to both.
An existing cluster is never passed to the destructive uds-k3d package. Reuse
requires both Core package records and all non-completed pods to be Ready;
it does not independently verify the installed package version.

## Result and routing

| Role | Context | Docker network | Tenant LoadBalancer | Host HTTPS |
| --- | --- | --- | --- | --- |
| shore | k3d-uds-shore | k3d-uds-shore, 172.18.0.0/16 | 172.18.0.201:443 | 9443 |
| ship | k3d-uds-ship | k3d-uds-ship, 172.19.0.0/16 | 172.19.0.201:443 | 10443 |

Each cluster has 23/23 pods Running and Ready. Shore has zero restarts;
ship has two historical metallb-frr-k8s-statuscleaner restarts. Each has one
Ready server, its own load balancer, registry, Pepr, Istio, Keycloak and authservice.
Both clusters use the bundle defaults for pod/service CIDRs; these are separate
networks, not directly routed pod-to-pod.

Inside each cluster, CoreDNS rewrites `*.uds.dev` to **that cluster's** tenant
gateway, and `*.admin.uds.dev` to its admin gateway. Do not assume
`shore.uds.dev` in ship resolves to shore. No host DNS or /etc/hosts changes
were made by this harness.

From the ship node network, the usable shore endpoint is the Docker host's
ship-bridge address **172.19.0.1:9443**, with TLS SNI/Host **shore.uds.dev**.
The published shore port forwards through the bundle's NGINX to its tenant
gateway. Verified with the host's curl executed inside the ship node netns:

```sh
PID=$(docker inspect k3d-uds-ship-server-0 --format '{{.State.Pid}}')
sudo nsenter -t "$PID" -n curl -sv --http2 --connect-timeout 5 --max-time 15 \
  --resolve shore.uds.dev:9443:172.19.0.1 https://shore.uds.dev:9443/api/v1/ -o /dev/null
```

TLS 1.3, trusted wildcard certificate, ALPN h2, and HTTP/2 404 from istio-envoy
were observed. 404 is expected: no Ghost Seeker Package/routes exist yet. No
`-k` or TLS-verification bypass was used. The bundled wildcard certificate
expires 2026-12-04; refresh it before later runs beyond that date.

A control check from the same ship netns to shore's direct LB
172.18.0.201:443 timed out, consistent with Docker bridge isolation. Use the
host published port, not direct cross-bridge LB routing. A later host-side link
shaper can forward to localhost:9443 and preserve the TLS stream; a ship-network
shaper can use 172.19.0.1:9443. Pod-level network policy, app sync and shaped
traffic are deliberately not validated in step 1.

## Evidence

`logs/` includes deployment streams with credential-related lines and table
rows omitted, raw pod/service/cluster/network snapshots, readiness/reuse output,
the invalid-TARGET check, DNS rewrites and both network probes. A private-key,
JWT and credential-value heuristic found no matches. No kubeconfigs, Secrets,
certificate private keys or credentials are included. Full application test
suites and prod-gate were not run for this bootstrap-only step.

Checks printed:

```text
PASS: syntax and existing-cluster reuse/health check
PASS: invalid TARGET rejected
shore: 23 pods; 23 Running and Ready; 0 total restarts
ship: 23 pods; 23 Running and Ready; 2 total restarts
PASS: no private-key/JWT/credential-value patterns
```

No protected preview ports or pre-existing servers were touched.
