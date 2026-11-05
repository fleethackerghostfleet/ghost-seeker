# Ghost Seeker fielding

This is a **demo package, not an accredited operational system**. Unit
SyncService authentication and edge storage caps are opt-in; there is no TLS
or deployment-specific security review. Use only in an isolated, approved
environment. The operator application is in-memory; shore and edge data are
SQLite databases.

## Stage a release

Build on a connected Linux host with Go 1.23+, Node 22 (for its tests), Python
3.11 (for edgeml tests), Docker, and Make. Go builds and the core containers
do not need Node or Python at runtime.

```sh
make test
node --test web/*.test.cjs
make build                       # CGO-free static binaries, including bin/mint-unit-token
make images                      # four versioned images
make images-save                 # dist/ghost-seeker-images.tar
make package-check
make zarf-package                # dist/zarf-package-ghost-seeker-amd64-0.1.0.tar.zst
```

If Docker Hub is rate-limiting the build host, use the public ECR library
mirrors for base images:

```sh
make images \
  GO_IMAGE=public.ecr.aws/docker/library/golang:1.23-alpine \
  PY_IMAGE=public.ecr.aws/docker/library/python:3.11-slim
```

The package includes `ghost-seeker/shore:0.1.0`,
`ghost-seeker/edge:0.1.0`, `ghost-seeker/operator:0.1.0`, and
`ghost-seeker/edgeml-sidecar:0.1.0`. Keep the package, image archive, checksums,
and release metadata together on approved transfer media. Verify provenance
through the organization's release process; these tags are not signatures or
immutable digests.

## Offline single-binary run

The Go operator app embeds its UI. After building, run it without Node, Python,
Docker, or network access:

```sh
./bin/ghost-seeker -addr :8090
# Open http://127.0.0.1:8090
```

The operator app is a synthetic, in-memory simulation unless `-shore` selects
live shore mode. Standalone edge and shore binaries use the checked-out `ui/`
directory by default:

```sh
./bin/shore -data data/shore -listen :8080
./bin/edge -data data/edge -listen :8081 -shore http://localhost:8080
```

The Go packages use pure-Go SQLite. Cross-compile the binaries for a Linux
USV, for example:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o bin/ghost-seeker-linux-arm64 ./cmd/ghost-seeker
```

Build on a connected staging host and transfer the binary and any required
static `ui/` files for edge/shore. The operator UI assets are embedded in
`bin/ghost-seeker`; `bin/edge` and `bin/shore` serve the UI from `UI_DIR`.

## Configuration reference

Edge and shore flags are initialized from environment variables; an explicit
flag overrides the environment. Empty environment values use the defaults
shown below. Operator settings are flags, with the token flags also accepting
the environment variables shown in the table.

### `edge`

| Flag | Environment | Default | Purpose |
|---|---|---|---|
| `-id` | `EDGE_ID` | `edge-1` | Edge identifier |
| `-data` | `DATA_DIR` | `data/edge` | SQLite store and blobs |
| `-listen` | `LISTEN` | `:8081` | Console and sync HTTP listener |
| `-shore` | `SHORE_URL` | `http://localhost:8080` | Shore base URL |
| `-infer` | `INFER_URL` | `mock` | Inference URL or `mock` |
| `-unit-token` | `GS_UNIT_TOKEN` | unset | HS256 unit token for shore sync |
| `-unit-token-file` | `GS_UNIT_TOKEN_FILE` | unset | Read unit token from a file; takes precedence over `GS_UNIT_TOKEN` |
| `-max-bytes` | `GS_EDGE_MAX_BYTES` | `0` (unlimited) | Maximum total retained form payload bytes |
| `-max-items` | `GS_EDGE_MAX_ITEMS` | `0` (unlimited) | Maximum retained item rows |
| `-scene-every` | — | `60s` | Scene interval for sightings mode; `0` disables interval |
| `-dedup` | `DEDUP` | `hash` | `naive`, `hash`, or `sightings` |
| `-ui` | `UI_DIR` | `ui` | Static UI directory |
| `-scenario` | `SCENARIO` | empty | Optional scenario JSON to replay |
| `-frames` | `FRAMES_DIR` | empty | Optional frame directory to replay |
| `-period` | — | `5s` | Replay capture period |
| `-tick` | — | `3s` | Scheduler tick |
| `-init-bps` | — | `64000` | Initial link estimate in bits/s |

### `shore`

| Flag | Environment | Default | Purpose |
|---|---|---|---|
| `-data` | `DATA_DIR` | `data/shore` | SQLite store |
| `-listen` | `LISTEN` | `:8080` | Shore HTTP listener |
| `-infer` | `INFER_URL` | `mock` | Inference URL for text embedding, or `mock` |
| `-require-auth` | `GS_REQUIRE_AUTH` | `false` | Require unit JWTs on SyncService |
| `-require-operator-auth` | `GS_REQUIRE_OPERATOR_AUTH` | `false` | Require operator JWTs on SetPolicy |
| `-signing-key` | `GS_SIGNING_KEY` | unset | HS256 signing key; also accepted by mint tool |
| `-signing-key-file` | `GS_SIGNING_KEY_FILE` | unset | Signing-key file; takes precedence over literal key |
| `-ui` | `UI_DIR` | `ui` | Static UI directory |
| `-edge` | `EDGE_ID` | empty | Edge ID to address downlink to; empty means all |

### `ghost-seeker` operator app

| Flag | Environment | Default | Purpose |
|---|---|---|---|
| `-addr` | — | `:8080` | UI HTTP listener |
| `-retain` | — | `260` | Maximum simulated items in memory |
| `-backfill` | — | `84` | Simulated historical items at startup |
| `-auto-cdr` | — | `15s` | Average watch-officer escalation interval; `0` disables |
| `-shore` | — | empty | Enable live shore mode with this shore URL |
| `-edge` | — | `http://localhost:8081` | Optional edge URL used in live mode |
| `-link-listen` | — | `:18080` | Link shaper listener; empty disables shaping |
| `-link-bps` | — | `2000000` | Initial shaped link rate in bits/s |
| `-poll` | — | `1s` | Live shore polling interval |
| `-operator-token` | `GS_OPERATOR_TOKEN` | unset | Operator JWT for shore ConsoleService calls |
| `-operator-token-file` | `GS_OPERATOR_TOKEN_FILE` | unset | Read operator JWT from a file; takes precedence over the literal token |

## State and ports

Shore and edge keep SQLite databases, WAL and shared-memory files under their
`DATA_DIR`; container and Kubernetes deployments mount persistent storage at
`/data`. Shore's PVC defaults to 5 GiB and edge's to 10 GiB. The operator app
does not persist state: its simulated data and live UI state are in memory.
Back up SQLite only after stopping the service, and preserve WAL files with the
database. Edge storage caps are opt-in; see [Edge storage caps](#edge-storage-caps).

| Service | Port | Notes |
|---|---:|---|
| Shore | 8080 | Sync/console HTTP; `GET /healthz` |
| Edge | 8081 | Console/sync HTTP; `GET /healthz` |
| Operator | 8090 in containers/Kubernetes; `:8080` by default | `GET /` serves the UI; no `/healthz` |
| Operator link shaper | 18080 | Disabled in the Kubernetes operator deployment |
| edgeml sidecar | 9090 | `GET /healthz`, Connect JSON RPC |
| Toxiproxy admin API | 8474 | Compose demo only |

SyncService unit JWT authentication is opt-in; ConsoleService remains
unauthenticated, and neither service provides TLS. Do not expose them to an
untrusted network.

## Compose demonstration

The Compose demo runs shore, Toxiproxy, and edge; use `frames/` locally if
replaying the Git LFS demo clips:

```sh
make compose
deploy/compose/degrade.sh satcom
# Stop with Ctrl-C, or use docker compose -f deploy/compose/docker-compose.yml down.
```

Shore listens on `:8080`; edge connects through the shaper at `link:18080`.
Toxiproxy's admin endpoint is available at port 8474. This local demo is not an
air-gap deployment test.

## Zarf / Kubernetes

`make images` builds all four local images before packaging. `make zarf-package`
creates the amd64 Zarf package under `dist/`; the shore, edge, and operator
components are enabled by default, while `edge-inference` is opt-in. Deploy the
package to an initialized cluster with a working default StorageClass:

```sh
zarf package deploy dist/zarf-package-ghost-seeker-amd64-0.1.0.tar.zst --confirm
kubectl -n ghost-seeker port-forward service/operator 8090:8090
```

The required `namespace` component is always deployed, so selecting only
`--components=edge` still creates the `ghost-seeker` namespace.

The default `SHORE_URL` is the in-cluster shore service address. For a split
deployment, install the `shore` and `operator` components in the shore/UDS
cluster; install the `edge` component in the shipboard k3s cluster and override
`SHORE_URL` with the shore address reachable from the ship. Deployments may
override `EDGE_ID`, `SHORE_STORAGE`, and `EDGE_STORAGE` as well. For example,
when staging an edge-only cluster:

```sh
zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm --components=edge \
  --set-variables=SHORE_URL=http://shore.example.internal:8080
```

For mock inference, leave `INFER_URL=mock`. To enable the optional edgeml
component in the same cluster, set `INFER_URL=http://edgeml-sidecar:9090` and
deploy `edge-inference` alongside `edge`:

```sh
zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm --components=edge,edge-inference \
  --set-variables=INFER_URL=http://edgeml-sidecar:9090
```

Edge can point across clusters to a separately routed sidecar if the network
permits it.

The edge only captures when it has a source: today that is replay via
`-frames`/`FRAMES_DIR` or `-scenario`/`SCENARIO` (there is no live sensor input
yet). The packaged edge mounts the node directory `FRAMES_HOSTPATH` (default
`/var/lib/ghost-seeker/frames`) read-only at `/frames`; with `FRAMES_DIR` and
`SCENARIO` empty (the defaults) the edge starts healthy but idle and shore
receives nothing. To replay the demo clips, copy `scenarios/ghostfleet_demo`
(including its LFS `frames/`) onto the node and point the edge at it:

```sh
zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm --components=edge \
  --set-variables=FRAMES_HOSTPATH=/srv/ghost-seeker,SCENARIO=/frames/ghostfleet_demo/scenario.json
```

Changing ConfigMap-backed settings (`SCENARIO`, `FRAMES_DIR`, edge caps, and
`SHORE_REQUIRE_AUTH`) or rotating Secret-backed credentials requires restarting
the affected workload because environment variables are read at pod startup:
run `kubectl -n ghost-seeker rollout restart statefulset/edge` for edge changes,
or use `statefulset/shore` for shore auth.

Shore and edge StatefulSets have independent RWO PVCs. Keep these PVCs when
upgrading or rolling back; changing image tags does not migrate the schema or
restore deleted storage. Stop the service before making a SQLite backup, and
retain the matching image/package version for rollback. Cluster deployment,
storage-class behavior, and cross-cluster routing have not been verified here.

## edgeml and offline model files

The sidecar defaults to mock mode and listens on `0.0.0.0:9090` in the
container. The standard image installs edgeml and CPU `onnxruntime`; mount
approved model files read-only at `/models` and override the command to use
`--detector /models/<detector>.onnx` and optionally
`--embed /models/<embedding-directory>`. Model acquisition and licensing are
outside the runtime; keep Hugging Face offline and telemetry disabled.

Tracking is optional because `boxmot` brings in PyTorch and substantially
increases build time and image size. To include it, build with
`--build-arg WITH_TRACKING=1`; this installs `boxmot` and CPU PyTorch from the
PyTorch CPU wheel index. The default image does not include tracking. Real-model
inference, model compatibility, and tracking behavior are **unverified**.

## Verification and troubleshooting

- `make test`: Go vet plus Go tests.
- `node --test web/*.test.cjs`: operator web tests.
- `python -m pytest -q edgeml/tests`: CPU edgeml tests.
- `make package-check`: local manifest checks plus kubeconform/Zarf checks when installed.
- `buf lint && buf generate`: proto lint and generated-code consistency.
- `docker compose -f deploy/compose/docker-compose.yml ps` and `logs`: inspect local demo startup.
- A Kubernetes pod stuck in `Pending` needs a usable StorageClass/PVC; shore and
  edge both require persistent storage.
- An edge that cannot sync should check `SHORE_URL` from the edge's network
  namespace. A Kubernetes service name is not reachable across isolated clusters.
- Sidecar `/healthz` indicates process health only; it does not prove that models
  load or inference is correct.
- CI builds images with `push: false`; no public image deployment occurs.

The Kubernetes manifests have not been deployed to a cluster. Real-model
inference and a field installation remain unverified.

## Sync authentication

SyncService unit authentication is off by default. When enabled, shore accepts
HS256 tokens with `sub` (edge ID), `role: "unit"`, and `exp` claims. The signing
key must be non-default and at least 32 bytes. Missing, invalid, or expired
tokens return `Unauthenticated`; an `ExchangeRequest.edge_id` that differs
from `sub` returns `PermissionDenied`. Unit authentication protects
SyncService. ConsoleService reads remain unauthenticated; `SetPolicy` requires
an operator token only when operator authentication is enabled. Neither
service has TLS.

`*_FILE` credentials take precedence over literal environment values. A
nonempty file path is read at startup and a missing file is an error; do not
set `GS_SIGNING_KEY_FILE` or `GS_UNIT_TOKEN_FILE` by default. Shore reads the
key and edge reads its token at startup, so mint and install a replacement
token, then restart edge before expiry.

`make build` creates `bin/mint-unit-token` for use on a trusted, key-holding
workstation. The token minting tool is deliberately absent from all container
images; shore only verifies tokens and never mints them.

### Enable in Kubernetes/Zarf

Create `shore-auth` in the cluster running shore and `edge-auth` in the cluster
running edge. If shore and edge are in separate clusters, each cluster needs
its own Secret in the `ghost-seeker` namespace. These credentials are passed
through Secret references, never ConfigMaps. The optional shore key reference
lets auth remain off by default; enabling `SHORE_REQUIRE_AUTH` without the
`shore-auth` Secret makes shore fail closed at startup.

```sh
# In the shore cluster
kubectl -n ghost-seeker create secret generic shore-auth \
  --from-file=signing-key=./signing.key

# On a trusted workstation with the same signing key
GS_SIGNING_KEY_FILE=./signing.key bin/mint-unit-token \
  -id edge-1 -ttl 720h > unit.jwt

# In the edge cluster; remove the plaintext token after storing it
kubectl -n ghost-seeker create secret generic edge-auth \
  --from-file=unit-token=./unit.jwt && rm unit.jwt

zarf package deploy dist/zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm \
  --set-variables=SHORE_REQUIRE_AUTH=true,EDGE_MAX_BYTES=10737418240,EDGE_MAX_ITEMS=50000
```

To rotate before expiry, mint a new token, replace the `edge-auth` Secret, and
restart the edge StatefulSet:

```sh
kubectl -n ghost-seeker rollout restart statefulset/edge
```

## Operator authentication

Operator authentication is off by default. Enable `GS_REQUIRE_OPERATOR_AUTH`
on shore to require operator JWTs for `SetPolicy` requests. Use the same
32-byte-or-longer signing key configured on shore to mint a token:

```sh
GS_SIGNING_KEY_FILE=./signing.key bin/mint-unit-token \
  -role operator -echelon ship -id TAO-DDG51 -ttl 720h > operator.jwt

kubectl -n ghost-seeker create secret generic operator-auth \
  --from-file=operator-token=./operator.jwt && rm operator.jwt
```

The operator workload reads this optional Secret as `GS_OPERATOR_TOKEN`; its
token is sent only to shore. Set `GS_REQUIRE_OPERATOR_AUTH=true` in shore's
configuration before deploying or restarting shore. Shore derives `issuedBy`
and echelon from the verified token, not from the request. An operator cannot
add, change, or drop policy layers above their echelon, so higher-echelon locks
are refused.

## Edge storage caps

Both caps default to `0` (unlimited) and can be set independently. They count
retained item rows and the sum of their form payload bytes, not SQLite/WAL
overhead or audit history. To make room, edge evicts only fully delivered
items: the manifest is ACKed and every stored form is ready, complete, and
ACKed through its full byte length. It evicts lowest scheduler priority first,
then oldest capture time (ingest sequence breaks ties). Pins, pending
corrections, and active pull state protect items.

When only protected items remain, admission returns `store.ErrFull`; the replay
ingester retains that capture and retries after five seconds, while other store
callers must retain and retry their input. Each eviction is recorded in the
`edge.db` `eviction_log` (`item_id`, `bytes`, `score`, `reason`, `ts`, with
`ts` in Unix milliseconds). `GetStatusResponse.items_evicted` reports the
cumulative eviction count.
