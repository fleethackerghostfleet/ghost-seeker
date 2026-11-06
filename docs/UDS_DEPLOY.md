# Fleet administrator: split UDS deployment

This runbook installs **SHORE** (shore + operator) and **SHIP** (edge + real
inference) onto **existing UDS Core 1.16.0** clusters. Application bundles do
not install or reconfigure Core. Deploy shore first, then each ship. The two
bundles may share a cluster in separate namespaces, but still use the shore
TLS gateway; direct cross-namespace h2c is not part of the split contract.

Production images use `-tags prod`: no simulation, replay, mock analyzer,
generated demo auth, or demo imagery. Use live sensor input only. Read
[FIELDING](FIELDING.md), [SECURITY](SECURITY.md), the
[bundle contract](../bundles/prod-uds/README.md), and
[model notices](../deploy/models/THIRD_PARTY_NOTICES.md) before fielding.

## 1. Prerequisites and change record

- Linux amd64 Kubernetes nodes for these packages; release manifests currently
  declare `architecture: amd64`. Do not assume arm64 artifacts exist.
- UDS Core **1.16.0**, healthy Pepr/operator, Istio tenant gateway, STRICT mTLS,
  Zarf registry/agent and storage provisioner on **both** clusters.
- Administrator workstation: **uds-cli v0.41.0**, **Zarf v0.89.0**, compatible
  `kubectl`, Bash, `curl` with HTTP/2, `openssl`, Python 3 and `sha256sum`.
  Source validation additionally needs Docker, Make, Go 1.26.10+, and
  **k3d v5.8.3**. Install pinned official binaries per [VERIFY](VERIFY.md).
- Cluster-admin/provisioning rights, approved secret-management channel,
  namespace ownership and a maintenance window. Explicitly select kubecontexts;
  do not rely on whichever cluster happens to be current.
- A default dynamic StorageClass or explicit `*_STORAGE_CLASS` values supporting
  ReadWriteOnce volumes and the workloads' non-root UIDs/fsGroup. Default
  capacities: ship edge data **10Gi**, frames **10Gi**; shore data **5Gi**,
  operator review log **1Gi**. Budget image/registry storage, model emptyDir,
  SQLite growth, snapshots and ingest backlog separately. Set retention limits;
  `EDGE_MAX_BYTES=0` / `EDGE_MAX_ITEMS=0` mean unlimited.
- Ship inference requests **500m CPU / 1Gi RAM**, limits **4 CPU / 4Gi RAM**.
  Edge requests **100m / 128Mi**, limits **1 CPU / 1Gi**. Shore and operator
  each request **100m / 128Mi**, limit **1 CPU / 512Mi**. These exclude Core,
  proxies and model initialization. Reserve headroom for peak inference at its
  configured limit; do not size a fleet node from requests alone. A local
  validation host with 8 CPU / 32Gi RAM is a starting point, not a throughput
  guarantee. Measure ingest latency, OOMs and throttling with the real cameras.
- Site DNS: `<EDGE_GATEWAY_HOST>.<ship DOMAIN>` resolves to ship's tenant
  gateway; `<SHORE_GATEWAY_HOST>.<shore DOMAIN>` and
  `<OPERATOR_GATEWAY_HOST>.<shore DOMAIN>` to shore's. Ship DNS must resolve
  shore's FQDN. `SHORE_URL`, public-host allowlists and gateway hosts must agree.
- Routed ship-to-shore TCP connectivity on the configured gateway port with
  **HTTP/2 end to end and intact gRPC trailers**, including every L7 proxy.
  Approve narrow destination CIDRs/ports; `192.0.2.0/32` is documentation-only,
  not a functioning egress policy. Include the actual gateway/NAT destination.

Record release URL, hashes, image lock, Core version, cluster IDs, site configs
(without secret values), storage/reclaim policy and previous release before
starting. Confirm tools and target contexts:

```bash
uds version
zarf version
kubectl version --client
curl --version                       # Features must include HTTP2
export SHORE_CONTEXT='<shore-context>' SHIP_CONTEXT='<ship-context>'
export SHORE_NS=ghost-seeker-shore SHIP_NS=ghost-seeker-ship
kubectl --context "$SHORE_CONTEXT" get nodes,storageclasses
kubectl --context "$SHIP_CONTEXT" get nodes,storageclasses
kubectl --context "$SHORE_CONTEXT" get pods -n pepr-system
kubectl --context "$SHIP_CONTEXT" get pods -n istio-system
```

### Gateway certificates: Core owns TLS

The **application never generates certificates**. Obtain site-approved
certificates valid for all gateway FQDNs; use Core's separately managed
configuration to install them. Never use the development wildcard certificate
for fleet service. Leave `EDGE_TLS_SECRET` and `SHORE_TLS_SECRET` empty: the
split packages reject application listener TLS behind STRICT mTLS.

Core 1.16.0's `istio-tenant-gateway` / `uds-istio-config` chart accepts
`tls.credentialName` for an existing TLS Secret in **istio-tenant-gateway**, or
`tls.cert` / `tls.key` as base64 PEM supplied securely. Have the Core
administrator expose these Helm values in the **Core** bundle, not the app:

```yaml
# Fragment of the site's Core bundle; retain its pinned refs/verification.
packages:
  - name: core-base
    overrides:
      istio-tenant-gateway:
        uds-istio-config:
          variables:
            - name: TENANT_TLS_SECRET
              path: tls.credentialName
```

Set `variables.core-base.TENANT_TLS_SECRET` and `variables.core-base.DOMAIN` in
that Core bundle's config (use its actual package name if different). Provision
the named certificate Secret through the site's approved mechanism. Repeat on
ship and shore; match each application's `DOMAIN` to its own Core domain. See
the pinned [Core chart values](https://github.com/defenseunicorns/uds-core/blob/v1.16.0/src/istio/charts/uds-istio-config/values.yaml).

If shore uses a private CA, put **public CA certificates only**, PEM encoded,
in a ship-namespace Secret with key `ca.crt`, and set ship `SHORE_CA_SECRET`:

```bash
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" create secret generic shore-ca \
  --from-file=ca.crt='<approved-ca-bundle.pem>'
```

Create the namespace first (section 3). The optional shore-side CA Secret is
only needed for HTTPS operator upstreams. Do not disable certificate or hostname
verification to work around a trust error.

## 2. Site configuration and complete variable reference

Use separate working directories, each containing an administrator-owned
`uds-config.yaml`. Start from
[shore](../bundles/prod-uds/shore/uds-config.yaml) and
[ship](../bundles/prod-uds/ship/uds-config.yaml). Variables belong under
`variables.ghost-seeker-shore` or `variables.ghost-seeker-ship`, **not** Helm
overrides. These files carry references to Secrets, never token/key values.

```yaml
# shore-site/uds-config.yaml
options:
  architecture: amd64
variables:
  ghost-seeker-shore:
    NAMESPACE: ghost-seeker-shore
    DOMAIN: shore.example.mil
    SHORE_GATEWAY_HOST: shore
    OPERATOR_GATEWAY_HOST: ghost-seeker
    SHORE_STORAGE_CLASS: "<shore-storage-class>"
    OPERATOR_STORAGE_CLASS: "<shore-storage-class>"
```

```yaml
# ship-site/uds-config.yaml
options:
  architecture: amd64
variables:
  ghost-seeker-ship:
    NAMESPACE: ghost-seeker-ship
    DOMAIN: ship.example.mil
    EDGE_ID: ship-001
    EDGE_GATEWAY_HOST: edge
    FRAMES_SOURCE: pvc
    SHORE_TRANSPORT: external
    SHORE_URL: https://shore.shore.example.mil:443
    SHORE_EGRESS_CIDR: "<approved-shore-gateway-ip>/32"
    SHORE_EGRESS_PORT: "443"
    SHORE_CA_SECRET: shore-ca # omit/use empty with system-trusted roots
    EDGE_STORAGE_CLASS: "<ship-storage-class>"
    FRAMES_STORAGE_CLASS: "<ship-storage-class>"
```

Replace all angle-bracket placeholders. Defaults below are **package defaults**,
not necessarily usable site values. “Required?” means the operational
requirement; every declared variable currently has a default. A retained
compatibility variable does not enable unsupported listener TLS. Public-host
configuration automatically appends the configured gateway FQDN; add any other
approved DNS aliases explicitly, including service names for custom namespaces.

| Name | Package | Default | Required? | Meaning |
| --- | --- | --- | --- | --- |
| `NAMESPACE` | ship | `ghost-seeker-ship` | Yes, default usable | Pre-create namespace and auth Secrets. |
| `NAMESPACE` | shore | `ghost-seeker-shore` | Yes, default usable | Pre-create namespace and auth Secrets. |
| `DOMAIN` | both | `uds.dev` | Yes, site value | Match the corresponding Core DOMAIN. |
| `EDGE_GATEWAY_HOST` | ship | `edge` | Yes | Tenant subdomain, not URL/FQDN. |
| `SHORE_GATEWAY_HOST` | shore | `shore` | Yes | Shore API/sync tenant subdomain. |
| `OPERATOR_GATEWAY_HOST` | shore | `ghost-seeker` | Yes | Operator tenant subdomain. |
| `EDGE_ID` | ship | `edge-1` | Yes, unique per ship | Must equal unit JWT subject. |
| `EDGE_PUBLIC_HOSTS` | ship | `edge,edge.ghost-seeker-ship.svc.cluster.local,edge.uds.dev` | Yes | Comma-separated accepted Host aliases; gateway FQDN is appended. |
| `SHORE_PUBLIC_HOSTS` | shore | `shore,shore.ghost-seeker-shore,shore.ghost-seeker-shore.svc,shore.ghost-seeker-shore.svc.cluster.local` | Yes | Accepted aliases; gateway FQDN is appended. |
| `EDGE_AUTH_SECRET` | ship | `edge-auth` | Yes, pre-existing | Secret with unit and edge operator credentials. |
| `EDGE_UNIT_TOKEN_KEY` | ship | `unit-token` | Yes | Key in EDGE_AUTH_SECRET containing expiring unit JWT. |
| `EDGE_OPERATOR_TOKEN_KEY` | ship | `operator-token` | Yes | Edge operator bearer key in EDGE_AUTH_SECRET. |
| `SHORE_AUTH_SECRET` | shore | `shore-auth` | Yes, pre-existing | Shore signing and operator credential Secret. |
| `SHORE_SIGNING_KEY_KEY` | shore | `signing-key` | Yes | HS256 key in SHORE_AUTH_SECRET. |
| `SHORE_OPERATOR_TOKEN_KEY` | shore | `operator-token` | Yes | Operator bearer key in SHORE_AUTH_SECRET. |
| `OPERATOR_AUTH_SECRET` | shore | `operator-auth` | Yes, pre-existing | Operator bridge credential Secret. |
| `OPERATOR_OPERATOR_TOKEN_KEY` | shore | `operator-token` | Yes | Must contain same value as shore's operator token. |
| `SHORE_REQUIRE_AUTH` | shore | `true` | Fixed true | Sync unit authentication; false is rejected. |
| `SHORE_TRANSPORT` | ship | `external` | Fixed external | Split transport always uses shore gateway TLS. |
| `SHORE_URL` | ship | `https://shore.uds.dev:443` | Yes, site value | External verified-TLS shore URL, including actual port. |
| `OPERATOR_SHORE_URL` | shore | `http://shore:8080` | Yes | In-namespace shore upstream behind mesh mTLS; keep default. |
| `SHORE_CA_SECRET` | both | empty | Private CA only | Existing Secret with PEM `ca.crt`; empty uses system roots. Ship: shore gateway trust. Shore: operator HTTPS upstream trust if configured. |
| `EDGE_TLS_SECRET` | both | empty | Must stay empty | Legacy listener-TLS compatibility variable; nonempty is rejected by UDS split packages. |
| `SHORE_TLS_SECRET` | both | empty | Must stay empty | Legacy listener-TLS compatibility variable; not a Core gateway certificate setting. |
| `EDGE_STORAGE` | ship | `10Gi` | Yes | Edge data PVC capacity (SQLite + blobs). |
| `EDGE_STORAGE_CLASS` | ship | empty | If no default SC | Edge data StorageClass; empty uses cluster default. |
| `FRAMES_SOURCE` | ship | `pvc` | Must use pvc | hostPath option is not admissible under UDS Pepr. |
| `FRAMES_HOSTPATH` | ship | `/var/lib/ghost-seeker/frames` | No, unused on UDS | Legacy hostPath location; ignored with pvc. |
| `FRAMES_STORAGE` | ship | `10Gi` | Yes | Frames PVC capacity; mounted read-only at `/frames`. |
| `FRAMES_STORAGE_CLASS` | ship | empty | If no default SC | Frames StorageClass. |
| `SHORE_STORAGE` | shore | `5Gi` | Yes | Shore data PVC capacity (SQLite + blobs). |
| `SHORE_STORAGE_CLASS` | shore | empty | If no default SC | Shore data StorageClass. |
| `OPERATOR_STORAGE` | shore | `1Gi` | Yes | Operator review-log PVC capacity. |
| `OPERATOR_STORAGE_CLASS` | shore | empty | If no default SC | Operator review-log StorageClass. |
| `EDGE_MAX_BYTES` | ship | `0` | Set site retention | Retained form payload-byte cap; zero unlimited. |
| `EDGE_MAX_ITEMS` | ship | `0` | Set site retention | Retained item cap; zero unlimited. |
| `INFER_URL` | ship | `http://edgeml-sidecar:9090` | Yes | Real inference endpoint; default stays inside ship namespace. |
| `SHORE_INFER_URL` | shore | empty | Optional | Remote text embedding endpoint; empty disables text search. Approve its egress. |
| `MODEL_DETECTOR` | ship | `yolo11n-640/model.onnx` | Yes | Approved detector path relative to model image `/models`. |
| `MODEL_EMBED` | ship | `mobileclip-s1` | Yes | Relative embedding directory, or `none` to disable embeddings. |
| `MODEL_SHA256` | ship | `41f9db8eaaa64a34803f1a145468d80a66910ad63572ddf24b9c9c95d6fb4875` | Yes, nonempty | SHA256 of the model image's SHA256SUMS file; init also verifies individual files. Match approved release. |
| `INFERENCE_CPU_REQUEST` | ship | `500m` | Yes | Scheduler CPU reservation. |
| `INFERENCE_CPU_LIMIT` | ship | `4` | Yes | CPU ceiling; must cover workload and be >= request. |
| `INFERENCE_MEMORY_REQUEST` | ship | `1Gi` | Yes | Scheduler RAM reservation. |
| `INFERENCE_MEMORY_LIMIT` | ship | `4Gi` | Yes | RAM ceiling; >= request; insufficient RAM causes OOM. |
| `SHORE_EGRESS_CIDR` | ship | `192.0.2.0/32` | Yes, replace | Narrow approved routed gateway destination; not allow-all. |
| `SHORE_EGRESS_PORT` | ship | `443` | Yes | Actual routed shore gateway TCP port. |
| `CAMERA_EGRESS_CIDR` | ship | `192.0.2.0/32` | If pulling camera | Approved optional MJPEG camera destination. Does not configure a camera URL. |
| `CAMERA_EGRESS_PORT` | ship | `80` | If pulling camera | Camera destination TCP port. |
| `INFERENCE_EGRESS_CIDR` | shore | `192.0.2.0/32` | If SHORE_INFER_URL set | Approved external inference destination. |
| `INFERENCE_EGRESS_PORT` | shore | `9090` | If SHORE_INFER_URL set | External inference TCP port. |
| `LLM_EGRESS_CIDR` | shore | `192.0.2.0/32` | Optional endpoint only | Operator's approved LLM/Vertex gateway destination. |
| `LLM_EGRESS_PORT` | shore | `8080` | Optional endpoint only | LLM destination TCP port. |
| `RESEARCH_EGRESS_CIDR` | shore | `192.0.2.0/32` | Optional endpoint only | Operator's approved research compiler destination. |
| `RESEARCH_EGRESS_PORT` | shore | `8097` | Optional endpoint only | Research destination TCP port. |
| `OCSP_EGRESS_CIDR` | shore | `192.0.2.0/32` | Optional endpoint only | Operator's approved OCSP responder destination. Does not enable PIV. |
| `OCSP_EGRESS_PORT` | shore | `80` | Optional endpoint only | OCSP destination TCP port. |

There are 33 ship and 28 shore declarations (shared rows above count for both).
Optional egress variables authorize destinations only; they do **not** enable
or configure `GS_MJPEG_URL`, `GS_LLM_URL`, `GS_RESEARCH_COMPILER_URL`, or PIV
clients. Those runtime capabilities need a separately reviewed manifest change;
they are not additional split-bundle variables.

## 3. Provision authentication before deployment

The administrator owns all credentials. Never run `tasks-dev.yaml` or
`dev_auth.py` on a fleet cluster. Shore enforces HS256 **unit** JWTs; operator
API bearer tokens are a different credential. Each ship JWT must have
`sub == EDGE_ID`, `role: unit`, a future `exp`, and a signature made with shore's
exact signing-key bytes. Use site-approved expiry/rotation policy and synchronized
clocks. The symmetric shore signing key can mint credentials for every ship;
do **not** copy it to ship. Shore and operator must share one operator token;
edge can use a separate token.

These Bash examples use placeholder prompts or captured random values, never
echo credentials or place them in command-line arguments. Disable shell tracing,
terminal recording and CI command echoing. Use an approved secret manager for
durable escrow; Kubernetes Secrets require encryption-at-rest and restricted
RBAC. Do not save the environment, log Secret YAML, or commit credentials.

```bash
set +x
umask 077
kubectl --context "$SHORE_CONTEXT" create namespace "$SHORE_NS"
kubectl --context "$SHIP_CONTEXT" create namespace "$SHIP_NS"
# For an existing namespace, verify ownership rather than deleting/recreating it.

# Generate 256-bit values without displaying them; alternatively read approved
# values with: read -rs -p '<operator token from secret manager>: ' SHORE_OPERATOR_TOKEN
SHORE_OPERATOR_TOKEN=$(openssl rand -hex 32)
EDGE_OPERATOR_TOKEN=$(openssl rand -hex 32)
export SHORE_SIGNING_KEY=$(openssl rand -hex 32)
export EDGE_ID=ship-001                  # must match ship-site/uds-config.yaml
export UNIT_TTL_SECONDS=86400            # example only; rotate before expiry
UNIT_TOKEN=$(python3 - <<'PY'
import base64, hashlib, hmac, json, os, time
def enc(value):
    return base64.urlsafe_b64encode(value).rstrip(b'=')
now = int(time.time())
header = enc(b'{"alg":"HS256","typ":"JWT"}')
claims = enc(json.dumps({"sub": os.environ["EDGE_ID"], "role": "unit",
                        "iat": now, "exp": now + int(os.environ["UNIT_TTL_SECONDS"])}).encode())
payload = header + b'.' + claims
print((payload + b'.' + enc(hmac.new(os.environ['SHORE_SIGNING_KEY'].encode(),
                                   payload, hashlib.sha256).digest())).decode())
PY
)

kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" create secret generic shore-auth \
  --from-file=signing-key=<(printf %s "$SHORE_SIGNING_KEY") \
  --from-file=operator-token=<(printf %s "$SHORE_OPERATOR_TOKEN")
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" create secret generic operator-auth \
  --from-file=operator-token=<(printf %s "$SHORE_OPERATOR_TOKEN")
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" create secret generic edge-auth \
  --from-file=unit-token=<(printf %s "$UNIT_TOKEN") \
  --from-file=operator-token=<(printf %s "$EDGE_OPERATOR_TOKEN")
unset SHORE_SIGNING_KEY UNIT_TOKEN
```

For existing credentials, substitute secret-manager values using silent
`read -rs` prompts for `<shore-signing-key>`, `<shore/operator-token>`,
`<edge-unit-JWT>` and `<edge-operator-token>` instead of generating replacements.
Use matching Secret names/keys if changing `*_AUTH_SECRET` / `*_KEY` variables.
Missing Secrets/keys fail closed. Rotate through the approved provisioning
process, then restart workloads because environment Secret references do not
hot-reload. Coordinate shore/operator token rotation and sign all replacement
ship JWTs before changing the shared HS256 key (there is no overlap keyring).

## 4. Acquire verified release artifacts and deploy

The release download base URL is
`https://github.com/fleethackerghostfleet/ghost-seeker/releases/download/uds-v0.1.0-rc2`;
`0.1.0-rc2` is the **package metadata version**, matching tag `uds-v0.1.0-rc2`.
Confirm these names against the published assets and checksum manifest before
downloading; do not substitute an earlier release. Naming follows
[tasks.yaml](../tasks.yaml):

- `uds-bundle-ghost-seeker-shore-amd64-0.1.0-rc2.tar.zst`
- `uds-bundle-ghost-seeker-ship-amd64-0.1.0-rc2.tar.zst`
- `zarf-package-ghost-seeker-shore-amd64-0.1.0-rc2.tar.zst`
- `zarf-package-ghost-seeker-ship-amd64-0.1.0-rc2.tar.zst`
- `SHA256SUMS`, `release.json`, `images.lock.json`, `THIRD_PARTY_NOTICES.md`,
  and SBOM JSON files listed in `SHA256SUMS` (plus provenance/signatures if published).

Use a fresh release directory; download and verify **all** files listed by the
release checksum manifest before deploying. Abort on any missing/mismatched
entry. SHA256SUMS detects corruption, not an untrusted publisher: obtain the
manifest through the approved authenticated channel and verify published
signatures/provenance according to site policy. Do not use `--insecure` or skip
signature validation.

```bash
export RELEASE_URL='https://github.com/fleethackerghostfleet/ghost-seeker/releases/download/uds-v0.1.0-rc2' VERSION='0.1.0-rc2' ARCH=amd64
# In an empty release working directory:
curl -fSL "$RELEASE_URL/SHA256SUMS" -o SHA256SUMS
for role in shore ship; do
  curl -fSL "$RELEASE_URL/uds-bundle-ghost-seeker-$role-$ARCH-$VERSION.tar.zst" \
    -o "uds-bundle-ghost-seeker-$role-$ARCH-$VERSION.tar.zst"
  curl -fSL "$RELEASE_URL/zarf-package-ghost-seeker-$role-$ARCH-$VERSION.tar.zst" \
    -o "zarf-package-ghost-seeker-$role-$ARCH-$VERSION.tar.zst"
done
# Fetch any additional files listed by this release's SHA256SUMS, then:
sha256sum --check SHA256SUMS
```

Each bundle contains its Zarf package/images; the standalone packages are for
inspection and package-level operations, not an extra installation step.

**Pinned CLI correction:** the frequently shown syntax
`uds deploy <bundle> --config uds-config.yaml` is **not supported by v0.41.0**
(`unknown flag: --config`). The tested equivalent is the `UDS_CONFIG`
environment variable; this is also what `tasks.yaml` uses. Use absolute config
paths to avoid accidentally loading a different working directory's config.

```bash
export SHORE_CONFIG='<absolute-path>/shore-site/uds-config.yaml'
export SHIP_CONFIG='<absolute-path>/ship-site/uds-config.yaml'
kubectl config use-context "$SHORE_CONTEXT"
UDS_CONFIG="$SHORE_CONFIG" uds deploy \
  "uds-bundle-ghost-seeker-shore-$ARCH-$VERSION.tar.zst" --confirm
# Complete shore readiness/API checks below before proceeding.
kubectl config use-context "$SHIP_CONTEXT"
UDS_CONFIG="$SHIP_CONFIG" uds deploy \
  "uds-bundle-ghost-seeker-ship-$ARCH-$VERSION.tar.zst" --confirm
```

Do not deploy the old combined `ghost-seeker` package alongside these packages.
It is not the split fleet installation. Do not deploy `k3d-core-slim-dev` onto
an existing fleet cluster: its development cluster package can destroy/recreate
the target k3d cluster.

## 5. Verify before accepting the deployment

### Kubernetes and UDS

```bash
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" rollout status statefulset/shore --timeout=300s
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" rollout status deployment/operator --timeout=300s
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" rollout status statefulset/edge --timeout=300s
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" rollout status deployment/edgeml-sidecar --timeout=600s
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" get pods,pvc
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" get pods,pvc
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" get packages.uds.dev ghost-seeker-shore -o yaml
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" get packages.uds.dev ghost-seeker-ship -o yaml
```

All pods (including `istio-proxy`) must be Ready, PVCs Bound and Package CRs
`status.phase: Ready`. Inspect Package status/errors, not just successful Helm
installation. Zarf's manifest release bookkeeping lives in `default`; the app
resources and Package CRs live in the configured application namespaces.

### TLS, authentication and live flow

Set site URLs and trust. `CURL_CA_BUNDLE` may point to an approved public CA
bundle for a private PKI; never use `curl -k` in acceptance checks. Keep bearer
headers off process arguments by passing them through curl's stdin config.

```bash
export SHORE_BASE=https://shore.shore.example.mil
export EDGE_BASE=https://edge.ship.example.mil
export OPERATOR_BASE=https://ghost-seeker.shore.example.mil
# If needed: export CURL_CA_BUNDLE='<approved-ca-bundle.pem>'
shore_curl() { curl --config <(printf 'header = "Authorization: Bearer %s"\n' "$SHORE_OPERATOR_TOKEN") "$@"; }
edge_curl() { curl --config <(printf 'header = "Authorization: Bearer %s"\n' "$EDGE_OPERATOR_TOKEN") "$@"; }

curl --http2 -sS -o /dev/null -w '%{http_code}\n' "$SHORE_BASE/api/v1/health"
# Expect 401, NOT 200, 302 to SSO, or 403 Host rejection.
shore_curl --http2 -fsS -o /dev/null -w '%{http_code}\n' "$SHORE_BASE/api/v1/health"
# Expect 200. Also check the authenticated collection:
shore_curl --http2 -fsS "$SHORE_BASE/api/v1/items?limit=1"
curl --http2 -sS -o /dev/null -w '%{http_version}\n' "$SHORE_BASE/api/v1/health"
# Expect 2; this verifies only this hop, not every ship-to-shore proxy.
```

Use actual `/api/v1/health` and `/api/v1/items` routes, not the bare `/api/v1`
prefix. The gateway intentionally exposes `/api/v1/` and SyncService routes,
not shore's unversioned console/admin routes. Operator UI is a separate host;
it must show a login form without credentials, then accept the operator token.

In terminal A, start SSE **before** sending the live JPEG; leave it idle for
at least 40 seconds (past the former 10-second deadline), then send a capture:

```bash
shore_curl --http2 -fNsS --max-time 90 "$SHORE_BASE/api/v1/stream"
```

In terminal B (with the same credential environment/functions), POST a fresh
JPEG from an approved live camera, not a synthetic/replayed deployment feed:

```bash
edge_curl --http2 -fsS -o ingest-result.json -w '%{http_code}\n' \
  -H 'Content-Type: image/jpeg' --data-binary '@<live-camera-capture.jpg>' \
  "$EDGE_BASE/api/v1/ingest/frames?source=acceptance-camera"
# Expect 202: stored/scored or duplicate-folded locally, NOT proof of shore delivery.
cat ingest-result.json
shore_curl --http2 -fsS "$SHORE_BASE/api/v1/items?limit=100" -o shore-items.json
```

Match the response's item/sighting ID and source to shore's items and the
`/api/v1/stream` event. Poll within the approved link-delay budget. Duplicate
folding and relative shore priority changes can produce updates rather than a
new card. SSE emits only on changes; silence while idle is not failure. Curl
exit 28 is expected only at the intentionally specified `--max-time`, not an
early EOF. Reconnect with the last event ID; on 410, resync from `/items` and
the health stream cursor as specified in [OpenAPI](api/openapi.yaml).

Inspect sync counters on the ship and application logs; snapshot before/after
the ingest and require successful sync progress with no growing failure count:

```bash
# Separate terminal; select a free port, not 8105/8106/8109.
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" port-forward service/edge 18181:8081
# In the credential terminal (port-forward bypasses the gateway, diagnostics only):
edge_curl -fsS -H 'Content-Type: application/json' --data '{}' \
  http://localhost:18181/ghostseeker.v1.ConsoleService/GetStatus
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" logs statefulset/edge -c edge --tail=100
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" logs statefulset/shore -c shore --tail=100
```

Compare `bytesSent`, `itemsManifested`, `itemsComplete`,
`heartbeat.unmanifestedItems` / `queuedBytes`, and `link.consecutiveFailures`.
ProtoJSON may omit zero-valued fields; absent counters mean zero. This is the
authenticated ConsoleService RPC, **not** a `/api/v1` addition. Raw scheduler
`Exchanges` / `Failures` are internal and are not exposed as a public metrics
endpoint in this release.

Do not treat a shore access-log HTTP 200 as sync acceptance: gRPC can fail after
HTTP headers. Store the counter deltas, ingest status, matched item ID, SSE
result and release hashes in the change record, with credentials redacted.

## 6. Upgrade, rollback and credential rotation

1. Keep the current and previous verified bundles, configs and hashes offline.
   Check release/schema compatibility and maintenance requirements. Freeze
   ingestion for an application-consistent SQLite + blobs backup, or use a
   validated CSI/application backup procedure; include operator review logs,
   edge backlog and secret-manager recovery records. Never copy only an active
   SQLite database while ignoring its WAL/blobs.
2. Verify the new release SHA256SUMS first. Preserve namespaces, Secret names,
   `EDGE_ID`, storage class and PVC identity unless deliberately migrating them.
   StorageClass changes are not an in-place volume migration; expansion needs
   provisioner support and cannot be assumed reversible.
3. Deploy new shore, complete verification, then canary ship before the fleet.
   Use the same section 4 commands with the new artifact/version and reviewed
   site config. Expect a maintenance interruption: these workloads are single
   replicas, not a zero-downtime HA deployment.
4. Repeat auth/live/SSE/counter checks. Roll Secrets explicitly when needed:

```bash
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" rollout restart statefulset/shore deployment/operator
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" rollout restart statefulset/edge
```

**Rollback:** uds-cli v0.41.0 / Zarf v0.89.0 expose no package rollback
subcommand. Do not invent `uds rollback` / `zarf package rollback`. Redeploy the
previous **verified** bundles with their matching config, shore then ship,
using section 4 commands with the previous `VERSION`. Confirm old binaries can
read current data before doing so; if a migration is incompatible, restore the
complete pre-upgrade data backup through the approved recovery procedure.
Helm-level rollback is not a substitute for restoring the bundle's images,
variables and package state. Redeployment normally retains StatefulSet PVCs;
it does **not** roll back their contents or undo received data. Check PVC UIDs
and data continuity after rollback.

## 7. Air-gapped operation and removal

Transfer verified bundles, SHA256SUMS, approved trust/provenance, pinned tools,
site configs and the previous release on approved media. Bundles include Go
apps, tracking-enabled inference and checksum-verified models: no model/pip
download should be needed at deployment/runtime. Provision Core, its registry,
DNS, time, storage and gateway PKI separately **before** disconnecting. Stage
credentials via the approved offline secret workflow, not in the bundle. Test
deployment with external network disconnected; disabling external network is
not the same as eliminating the required routed shore/camera connections.
Optional research/LLM/OCSP/inference endpoints need approved internal services
and narrow egress. Offline PIV revocation operations remain in SECURITY.md.

Uninstall is a destructive maintenance action, not rollback. Stop new captures,
drain or explicitly preserve the ship backlog, and take/verify recovery backups
before proceeding. Inventory PVCs/PVs and reclaim policy on both clusters:

```bash
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" get pvc -o wide
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" get pvc -o wide
kubectl --context "$SHIP_CONTEXT" get pv
kubectl --context "$SHORE_CONTEXT" get pv
```

StatefulSet `data-edge-0`, `frames-edge-0`, `data-shore-0` normally survive
**StatefulSet-only** removal by Kubernetes retention defaults. This does not
make package removal safe: **the packages also own a Namespace manifest;
removing its Helm release can delete the namespace and every remaining PVC
and Secret in it.** `operator-data` is additionally an explicit chart-managed
PVC and can be deleted with its release. Treat every claim as at risk. A
`Delete` PV reclaim policy can destroy the storage when its PVC disappears;
arrange approved snapshots/Retain policy and a tested restore before
uninstalling. Back up Secrets separately. If retention in place is required,
establish and test an administrator-approved Helm resource-retention procedure
for the Namespace and chart-managed PVC **before** package removal; merely
leaving StatefulSet claim retention at its default is insufficient.

```bash
# DESTRUCTIVE: execute only after the backup/retention procedure above.
kubectl config use-context "$SHIP_CONTEXT"
uds zarf package remove ghost-seeker-ship --confirm
kubectl config use-context "$SHORE_CONTEXT"
uds zarf package remove ghost-seeker-shore --confirm
```

Re-inventory any retained volumes and test recovery. Removing Package CRs
removes their generated routing/policies. Do not additionally delete namespaces,
PVCs, PVs or Secrets as routine cleanup. Namespace deletion removes retained PVCs too.
Data destruction requires separate explicit authorization and retention-policy
approval; remove only the named resources after backup/retention acceptance.
Never uninstall Core as part of removing Ghost Seeker.

## 8. Troubleshooting

| Symptom | Checks / corrective action |
| --- | --- |
| Shore logs 200, edge counts sync failures or `missing grpc-status` | Any **HTTP/1.1-only L7 proxy** on ship→shore drops gRPC trailers. Require HTTP/2 end to end: client h2c for internal legacy transport, ALPN h2 for external TLS, and shore Service `appProtocol: http2`. Check every load balancer/gateway/upstream hop, not just curl's negotiated version. Do not mask failures or disable auth. |
| Pepr denies `hostPath`; edge StatefulSet has no pods | Set `FRAMES_SOURCE=pvc` (split default), choose a usable StorageClass, redeploy. Do not relax Pepr or use `FRAMES_HOSTPATH` on UDS. Push live JPEGs through the ingest API; the frames mount is read-only to edge. |
| Pod-to-pod timeout though gateway works | Package `expose` admits the gateway only. Verify both **Egress and Ingress allow pairs** for edge↔inference and operator↔shore, correct selectors/namespaces/ports, Package Ready and generated NetworkPolicies. Do not add allow-all. Split shore traffic uses gateway egress CIDR/port, not cross-namespace h2c. |
| `ImagePullBackOff` for app images or Istio init despite bundled images | UDS-managed namespaces set `zarf.dev/agent=ignore`: do not depend on the Zarf admission agent to rewrite images or add credentials. Workload manifests must use the built-in `###ZARF_REGISTRY###` prefix and explicit `imagePullSecrets: [{name: private-registry}]`. Verify that Secret exists without printing its contents. Rebuild/redeploy corrected bundles; do not remove the UDS namespace label or disable admission policies. |
| Wrong binary despite successful deploy | Compare image references and pod `imageID` to the release lock. `IfNotPresent` plus reused tags leaves stale k3d node images. Build a fresh immutable tag/digest and repackage; do not overwrite an existing release tag or rely on deleting pods to refresh it. |
| Ingest/edgeml returns 503 while `/healthz` is healthy | Check model init checksum logs, memory/CPU, real `/ghostseeker.v1.InferenceService/Analyze` errors, boxmot tracking (`WITH_TRACKING=1`), and installed COLREGS JSON catalog. Health alone does not execute Analyze. The image must include packaged `edgeml.nightlights` JSON resources; validate with a real-model JPEG, not mock mode. Local validation confirmed both COLREGS JSON files installed in the built image and Analyze returned HTTP 200 without catalog errors; no catalog packaging fix was needed. |
| Idle SSE ends around 10 seconds / no next event | Confirm release contains the idle-stream write-deadline fix; disable proxy response buffering and set a sufficiently long idle timeout on **all** hops. Observe >40s idle followed by a real event. No traffic while idle is normal; unexpected EOF is not. Respect cursor/410 resync semantics. |
| `CreateContainerConfigError`, 401 or expired JWT | Verify Secret names and key **names** without printing values; JWT role/unit, subject/EDGE_ID, expiration, clock and signing key agreement. Missing keys fail closed. Restart env-consuming workloads after rotation. Shore/operator tokens must match. |
| TLS unknown authority / SAN mismatch | Match DNS, gateway SANs, Core DOMAIN and SHORE_URL. Install approved public CA bundle via SHORE_CA_SECRET. Do not set listener TLS or bypass verification. |
| 403 Host / gateway 404 | Check `*_PUBLIC_HOSTS`, auto-appended gateway FQDN, configured Core/application DOMAIN and route prefixes. `/api/v1/health` is exposed; bare `/api/v1` and shore `/api/shore/*` are not gateway acceptance endpoints. |
| PVC Pending / permission denied / inference OOM | Inspect `describe pod`, `describe pvc`, storage provisioner, fsGroup permissions, node allocatable resources and events. Tune `INFERENCE_*` with measured load; do not disable resource/security controls. |
| Unknown `--config` flag | Use `UDS_CONFIG=/absolute/path/uds-config.yaml uds deploy <bundle> --confirm` with uds-cli v0.41.0. |

Safe diagnostic starting points (review logs for sensitive material before sharing):

```bash
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" get events --sort-by=.lastTimestamp
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" describe pod -l app=edge
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" logs deployment/edgeml-sidecar -c models
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" logs deployment/edgeml-sidecar -c edgeml-sidecar --tail=100
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" get service shore -o jsonpath='{.spec.ports[0].appProtocol}'
```

## 9. OPEN decisions (not resolved by this runbook)

- **Model licensing/provenance:** YOLO11n stock COCO weights: AGPL-3.0 versus
  Enterprise; MobileCLIP-S1: Apple research terms. See THIRD_PARTY_NOTICES;
  packaging is not fleet legal approval. Do not substitute or retrain here.
- **Hard-coded “FOR TRAINING AND DEMONSTRATION ONLY” banner:** release/product
  authority must decide its fleet treatment. This runbook does not remove it.
- **PIV pass-through versus UDS SSO:** current split package uses operator
  tokens behind gateway TLS. Application PIV needs client-certificate TLS
  semantics; forwarded identity headers are not equivalent. No automatic UDS
  SSO integration is claimed. Decide and security-review the architecture.
- **Branch protection:** owner must decide required reviewers/checks and
  enforcement for prod-uds; do not bypass gates. Code-owner review applies to
  gate, `.github/`, AGENTS.md and `*_prod.go` changes.

## 10. Local validation (not fleet certification)

Use an isolated disposable uds-k3d cluster with unique name/API/HTTP/HTTPS
ports. Never reuse an existing cluster or ports 8105, 8106, 8109. Core's slim
development bundle is a laboratory bootstrap, not a production prerequisite
installer. Keep generated laboratory credentials separate from fleet material.

Example **laboratory-only** Core config (verify the name/ports are unused
before running; bootstrap deletes any k3d cluster with this name):

```yaml
# local-core.yaml, NOT an application/fleet config
variables:
  uds-k3d-dev:
    CLUSTER_NAME: uds-runbook
    K3D_API_PORT: "18650"
    K3D_HTTP_PORT: "18280"
    K3D_HTTPS_PORT: "18443"
```

```bash
k3d cluster list
UDS_CONFIG="$PWD/local-core.yaml" uds deploy \
  ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-slim-dev:1.16.0 --confirm
export SHORE_CONTEXT=k3d-uds-runbook SHIP_CONTEXT=k3d-uds-runbook
```

From a clean source checkout, production build/package commands are:

```bash
make pin-images
# Docker Hub 429: use GO_IMAGE=mirror.gcr.io/library/golang:1.26.10-alpine
# and PY_IMAGE=mirror.gcr.io/library/python:3.11-slim on make pin-images.
make zarf-shore
make zarf-ship
uds run create-shore-bundle
uds run create-ship-bundle
```

`make pin-images` mutates the image lock/manifests; retain these local build
records, but do not commit a local validation image set in a docs-only PR.
Create a local SHA256SUMS, then follow sections 2–5 literally with the built
bundles. Document any development-only DNS/CA/network settings separately;
they must never become fleet defaults. Verification evidence and any limits
of the laboratory run belong in the PR/change record, not an implied fleet
certification.
