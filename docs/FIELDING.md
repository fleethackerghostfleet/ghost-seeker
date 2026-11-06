# Ghost Seeker fielding

This is a **demo package, not an accredited operational system**. Unit
SyncService authentication is on by default; edge storage caps are opt-in. There is no TLS
or deployment-specific security review. Use only in an isolated, approved
environment. The operator application is in-memory; shore and edge data are
SQLite databases.

## Stage a release

Build on a connected Linux host with Go 1.26.10+, Node 22 (for its tests), Python
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
  GO_IMAGE=public.ecr.aws/docker/library/golang:1.26.10-alpine \
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
GS_SIGNING_KEY_FILE=data/unit.key ./bin/shore -data data/shore -listen :8080
GS_UNIT_TOKEN_FILE=data/edge.jwt   ./bin/edge  -data data/edge  -listen :8081 -shore http://localhost:8080
# `make run-shore` / `make run-edge` create data/unit.key and mint data/edge.jwt for you.
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
shown below. The operator table lists its flags and supported environment
variables.

### `edge`

| Flag | Environment | Default | Purpose |
|---|---|---|---|
| `-id` | `EDGE_ID` | `edge-1` | Edge identifier |
| `-data` | `DATA_DIR` | `data/edge` | SQLite store and blobs |
| `-listen` | `LISTEN` | `:8081` | Console HTTP listener (sync is outbound to shore) |
| `-public-hosts` | `GS_PUBLIC_HOSTS` | empty | Additional Host names accepted by the DNS-rebinding guard |
| `-shore` | `SHORE_URL` | `http://localhost:8080` | Shore base URL |
| `-infer` | `INFER_URL` | `mock` (prod: empty, required) | Inference sidecar URL; `mock` exists only in demo builds |
| `-unit-token` | `GS_UNIT_TOKEN` | unset | HS256 unit token for shore sync |
| `-unit-token-file` | `GS_UNIT_TOKEN_FILE` | unset | Read unit token from a file; takes precedence over `GS_UNIT_TOKEN` |
| `-max-bytes` | `GS_EDGE_MAX_BYTES` | `0` (unlimited) | Maximum total retained form payload bytes |
| `-max-items` | `GS_EDGE_MAX_ITEMS` | `0` (unlimited) | Maximum retained item rows |
| `-scene-every` | — | `60s` | Scene interval for sightings mode; `0` disables interval |
| `-dedup` | `DEDUP` | `hash` | `naive`, `hash`, or `sightings` |
| `-prioritizer` | `PRIORITIZER` | `edgeml` | Object-level scoring or `tomer` image-level scoring; `tomer` needs a sidecar in `tomer`/`both` mode |
| `-ui` | `UI_DIR` | `ui` | Static UI directory |
| `-scenario` | `SCENARIO` | empty | Demo builds only: scenario JSON to replay |
| `-frames` | `FRAMES_DIR` | empty | Demo builds only: frame directory to replay |
| `-period` | — | `5s` | Replay capture period |
| `-tick` | — | `3s` | Scheduler tick |
| `-init-bps` | — | `64000` | Initial link estimate in bits/s |

### `shore`

| Flag | Environment | Default | Purpose |
|---|---|---|---|
| `-data` | `DATA_DIR` | `data/shore` | SQLite store |
| `-listen` | `LISTEN` | `:8080` | Shore HTTP listener |
| `-public-hosts` | `GS_PUBLIC_HOSTS` | empty | Additional Host names accepted by the DNS-rebinding guard |
| `-infer` | `INFER_URL` | `mock` (prod: empty = text search off) | Inference URL for text embedding; `mock` exists only in demo builds |
| `-require-auth` | `GS_REQUIRE_AUTH` | `true` | Require unit JWTs on SyncService; `false` is for isolated lab links only |
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
| `-link-listen` | — | `:18080` | Automatic normal-distribution link shaper: 0–2 MB/s, center 0.5 MB/s, stddev 0.30 MB/s (~5% outage intervals), resampled every 5 s; empty disables shaping |
| `-poll` | — | `1s` | Live shore polling interval |
| `-operator-token` | `GS_OPERATOR_TOKEN` | unset | Opaque bearer token for the shore/edge ConsoleService transport gate |
| `-operator-token-file` | `GS_OPERATOR_TOKEN_FILE` | unset | Read the opaque token from a file; takes precedence over the literal token |
| `-operator-id` | `GS_OPERATOR_ID` | `operator-ui` | Issuer recorded on policy edits |
| `-dev-token-file` | `GS_DEV_TOKEN_FILE` | `data/operator.token` | Local demo token; `none` requires an explicit operator credential |

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
| Edge | 8081 | Console HTTP; `GET /healthz`; sync connects outbound to shore |
| Operator | 8090 in containers/Kubernetes; `:8080` by default | `GET /` serves the UI; no `/healthz` |
| Operator link shaper | 18080 | Disabled in the Kubernetes operator deployment |
| edgeml sidecar | 9090 | `GET /healthz`, Connect JSON RPC |
| Toxiproxy admin API | 8474 | Compose demo only |

SyncService unit JWT authentication is on by default. The opaque operator bearer
token gates every ConsoleService RPC and `/blob/` request. Neither service
provides TLS. Do not expose them to an untrusted network.

## Compose demonstration

The Compose demo runs shore, Toxiproxy, and edge; use `frames/` locally if
replaying the Git LFS demo clips. Compose requires `GS_OPERATOR_TOKEN`,
`GS_SIGNING_KEY`, and `GS_UNIT_TOKEN`; it disables automatic dev-token creation
inside the containers. For a local demonstration, populate them from the
same generated credentials as the Makefile demo:

```sh
make build demo-key
export GS_OPERATOR_TOKEN="$(make -s demo-token)"
export GS_SIGNING_KEY="$(cat data/unit.key)"
export GS_UNIT_TOKEN="$(./bin/mint-unit-token -id edge-1 -ttl 24h)"
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
override `EDGE_ID`, `SHORE_STORAGE`, and `EDGE_STORAGE` as well. The shore
package's `SHORE_PUBLIC_HOSTS` must include the ship-facing shore hostname for
the DNS-rebinding guard; direct IP literals are always allowed and need no
entry. Include any operator ingress hostname in this list too. For example,
deploy the shore/operator components with:

```sh
zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm --components=shore,operator \
  --set-variables=SHORE_PUBLIC_HOSTS=shore,shore.ghost-seeker,shore.ghost-seeker.svc,shore.ghost-seeker.svc.cluster.local,shore.example.internal
```

When staging an edge-only cluster:

```sh
zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm --components=edge \
  --set-variables=SHORE_URL=http://shore.example.internal:8080
```

The packaged images are production builds (`-tags prod`, see
[Production profile](#production-profile-and-prod-gate)): the edge has no
built-in analyzer, so `INFER_URL` (default `http://edgeml-sidecar:9090`) must
reach an edgeml sidecar. To run the sidecar in the same cluster, deploy
`edge-inference` alongside `edge`:

```sh
zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm --components=edge,edge-inference \
  --set-variables=INFER_URL=http://edgeml-sidecar:9090
```

Edge can point across clusters to a separately routed sidecar if the network
permits it.

Production images have no scenario/frame replay (`-scenario`, `-frames`,
`SCENARIO` and `FRAMES_DIR` exist only in demo builds); the edge captures from
the live input stream. The packaged edge mounts the node directory
`FRAMES_HOSTPATH` (default `/var/lib/ghost-seeker/frames`) read-only at
`/frames` for sensor frame drops. Replaying the demo clips needs a demo build
(`make compose-demo`, or `docker build --build-arg GO_TAGS= ...`).

Changing ConfigMap-backed settings (`INFER_URL`, edge caps, and
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
PyTorch CPU wheel index. The default image does not include tracking.
Real-model inference and tracking in the packaged container remain
**unverified**; the source-based experiments are documented separately in
[the edgeml reports](../edgeml/reports/E.md).

## Verification and troubleshooting

- `make test`: Go vet plus Go tests.
- `make prod-gate`: production-profile contract (see below); also run by `make verify`.
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
inference in this deployment package and a field installation remain unverified.

## Sync authentication

SyncService unit authentication is on by default. Shore requires a signing key
unless explicitly started with `-require-auth=false` for an isolated lab link. It accepts
HS256 tokens with `sub` (edge ID), `role: "unit"`, and `exp` claims. The signing
key must be non-default and at least 32 bytes. Missing, invalid, or expired
tokens return `Unauthenticated`; an `ExchangeRequest.edge_id` that differs
from `sub` returns `PermissionDenied`. Unit authentication protects
SyncService. The opaque operator bearer token gates ConsoleService and
`/blob/` requests. Neither service has TLS.

`*_FILE` credentials take precedence over literal environment values. A
nonempty file path is read at startup and a missing file is an error. Shore
reads the signing key and edge reads its unit token at startup, so mint and
install a replacement token, then restart edge before expiry.

`make build` creates `bin/mint-unit-token` for use on a trusted, key-holding
workstation. The token minting tool is deliberately absent from all container
images; shore only verifies tokens and never mints them.

### Enable in Kubernetes/Zarf

Unit auth is **on by default** in the Zarf package (`SHORE_REQUIRE_AUTH=true`), and each
workload needs its shared opaque operator bearer token: the manifests reference the
`operator-token` keys as *required*, so the pods stay Pending until they exist. Create
the Secrets in the `ghost-seeker` namespace before `zarf package deploy` (each cluster
needs its own copy if shore and edge are deployed separately). Credentials are passed
through Secret references, never ConfigMaps.

| Secret | Keys | Used by |
| --- | --- | --- |
| `shore-auth` | `signing-key`, `operator-token` | shore |
| `edge-auth` | `unit-token`, `operator-token` | edge |
| `operator-auth` | `operator-token` | operator web app |

```sh
# One operator token shared by shore, edge and the operator web app (>= 16 bytes); owner-only file
(umask 077; openssl rand -hex 32 > operator.token)

# In the shore cluster
kubectl -n ghost-seeker create secret generic shore-auth \
  --from-file=signing-key=./signing.key --from-file=operator-token=./operator.token
kubectl -n ghost-seeker create secret generic operator-auth \
  --from-file=operator-token=./operator.token

# On a trusted workstation with the same signing key
GS_SIGNING_KEY_FILE=./signing.key bin/mint-unit-token \
  -id edge-1 -ttl 720h > unit.jwt

# In the edge cluster; remove the plaintext token after storing it
kubectl -n ghost-seeker create secret generic edge-auth \
  --from-file=unit-token=./unit.jwt --from-file=operator-token=./operator.token && rm unit.jwt

zarf package deploy dist/zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --confirm \
  --set-variables=EDGE_MAX_BYTES=10737418240,EDGE_MAX_ITEMS=50000
```

Setting `SHORE_REQUIRE_AUTH=false` is for lab clusters only; shore then logs a WARN at startup and
accepts any edge. ConsoleService always requires an operator token; local
binary runs may auto-generate one, while Compose and Zarf require an explicit credential.

To rotate before expiry, mint a new token, replace the `edge-auth` Secret, and
restart the edge StatefulSet:

```sh
kubectl -n ghost-seeker rollout restart statefulset/edge
```

## Operator authentication

The opaque `opauth` bearer token means “may talk to the console”: it is the
transport gate for every ConsoleService RPC and `/blob/` request. The operator
app reads it from `-operator-token` or `-operator-token-file`. Shore and edge
use the shared token as their ConsoleService transport gate. Policy edits record
the configured `-operator-id` (default `operator-ui`) as their issuer.
Command-layer edits still need a trusted policy signature.

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

## Production profile and prod gate

Production builds use the Go build tag `prod` (`make build-prod`,
`deploy/Dockerfile` by default via `ARG GO_TAGS=prod`, and therefore the Zarf
package images). Demo wiring lives in `*_demo.go` files (`//go:build !prod`)
with stubs in `*_prod.go` (`//go:build prod`), so `dev`/`main` keep the demo and
`prod` binaries do not contain it:

- `ghost-seeker` runs only in live mode (`-shore`); without it the binary exits
  with an error. There is no simulation generator, backfill, `-capture-every`,
  `-sim-link-bps`, `-auto-cdr`, `-dataset` replay or `-negatives-rate`.
- No `POST /api/demo/reset` (`server.WithDemoReset`), no `-demo-link-control`
  (`POST /api/shore/link`), no `-demo-accept-any-token`, no
  `-dev-token-file` auto-generation and no `-dev-trust-peers`: operators sign in
  with a real token (`GS_OPERATOR_TOKEN` / `-operator-token-file`) or PIV.
- The edge has no `-infer mock`, `-scenario`, `-frames` or `-period` replay;
  shore has no `-infer mock`.
- The embedded demo imagery (`web/static/imagery`, including the empty-ocean
  frames) and its catalog (`internal/catalog/imagery.json`) are not embedded.
- `/api/config` reports `features: {profile, simulation, demoReset, dataset,
  demoLinkControl}`; the console hides or removes demo-only controls unless the
  server reports them `true`.

Demo compose files (`deploy/compose/*.yml`) pass `GO_TAGS=""` and keep the demo
build.

`make prod-gate` (`scripts/prod-gate.sh`) builds every command with
`-tags prod`, runs `go vet`/`go test -tags prod ./...` and every
`TestProdContract*` test, scans the prod binaries (`go tool nm`, embedded
strings and `-h` output) for demo symbols, routes and flags, runs
`web/prod-features.test.cjs`, checks the Docker/Zarf defaults, and checks that
the `/api/v1` output API is documented in `docs/api/openapi.yaml`. The
`prod-gate` GitHub workflow runs it on pull requests to `prod` and `dev`.

**Repository admin action required:** in GitHub branch protection for `prod`,
mark the `prod-gate` status check as required (and require pull requests).
Bots and agents cannot change branch protection, so until an admin does this
the gate is advisory on GitHub. See `AGENTS.md`.
