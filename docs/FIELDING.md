# Ghost Seeker fielding

This is a **demo package, not an accredited operational system**. Packaging
does not add storage eviction, authentication, TLS, or a deployment-specific
security review. Use only in an isolated, approved environment until those
controls are provided. The operator application is in-memory; shore and edge
data are SQLite databases.

## Stage a release

Build on a connected Linux host with Go 1.23+, Node 22 (for its tests), Python
3.11 (for edgeml tests), Docker, and Make. Go builds and the core containers
do not need Node or Python at runtime.

```sh
make test
node --test web/*.test.cjs
make build                       # CGO-free static bin/edge, bin/shore, bin/sim, ...
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
shown below. Operator configuration is flags only.

### `edge`

| Flag | Environment | Default | Purpose |
|---|---|---|---|
| `-id` | `EDGE_ID` | `edge-1` | Edge identifier |
| `-data` | `DATA_DIR` | `data/edge` | SQLite store and blobs |
| `-listen` | `LISTEN` | `:8081` | Console and sync HTTP listener |
| `-shore` | `SHORE_URL` | `http://localhost:8080` | Shore base URL |
| `-infer` | `INFER_URL` | `mock` | Inference URL or `mock` |
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
| `-ui` | `UI_DIR` | `ui` | Static UI directory |
| `-edge` | `EDGE_ID` | empty | Edge ID to address downlink to; empty means all |

### `ghost-seeker` operator app

| Flag | Default | Purpose |
|---|---|---|
| `-addr` | `:8080` | UI HTTP listener |
| `-retain` | `260` | Maximum simulated items in memory |
| `-backfill` | `84` | Simulated historical items at startup |
| `-auto-cdr` | `15s` | Average watch-officer escalation interval; `0` disables |
| `-shore` | empty | Enable live shore mode with this shore URL |
| `-edge` | `http://localhost:8081` | Optional edge URL used in live mode |
| `-link-listen` | `:18080` | Link shaper listener; empty disables shaping |
| `-link-bps` | `2000000` | Initial shaped link rate in bits/s |
| `-poll` | `1s` | Live shore polling interval |

## State and ports

Shore and edge keep SQLite databases, WAL and shared-memory files under their
`DATA_DIR`; container and Kubernetes deployments mount persistent storage at
`/data`. Shore's PVC defaults to 5 GiB and edge's to 10 GiB. The operator app
does not persist state: its simulated data and live UI state are in memory.
Back up SQLite only after stopping the service, and preserve WAL files with the
database. Storage cap/eviction is not implemented.

| Service | Port | Notes |
|---|---:|---|
| Shore | 8080 | Sync/console HTTP; `GET /healthz` |
| Edge | 8081 | Console/sync HTTP; `GET /healthz` |
| Operator | 8090 in containers/Kubernetes; `:8080` by default | `GET /` serves the UI; no `/healthz` |
| Operator link shaper | 18080 | Disabled in the Kubernetes operator deployment |
| edgeml sidecar | 9090 | `GET /healthz`, Connect JSON RPC |
| Toxiproxy admin API | 8474 | Compose demo only |

The demo endpoints do not provide JWT sync authentication or TLS. Do not expose
them to an untrusted network.

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

## TODO (pending another PR)

- Add storage caps, retention, and safe eviction behavior.
- Add JWT-based sync authentication and key rotation.
