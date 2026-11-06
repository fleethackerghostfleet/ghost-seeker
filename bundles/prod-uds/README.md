# Production UDS configuration contract

Two **application-only** bundles target existing UDS Core **1.16.0** clusters:
`ship/` (edge + inference) and `shore/` (shore + operator). They never install,
replace or reconfigure Core. Build with Zarf **v0.89.0**, bundle with uds-cli
**v0.41.0**. `tasks.yaml` contains only production tasks. The old combined
non-UDS package remains available under `deploy/zarf/`; local generated auth is
isolated in `tasks-dev.yaml` (`uds run dev --file tasks-dev.yaml`).

Run `make pin-images` before packaging. It builds `-tags prod` Go images, the
tracking-enabled inference image, and the checksum-verified model image, then
records one fresh SHA/timestamp/random build tag in `images.lock.json` and all
package/manifests. Never rebuild/push over those tags; configure registry
immutability when publishing. `make images` alone uses a fresh tag but does not
change deployment pins. `make zarf-ship`, `make zarf-shore`, then the
`create-ship-bundle` / `create-shore-bundle` UDS tasks build the deliverables.

## Release build

Install the pinned tools in `docs/VERIFY.md` (Go 1.26.10, Zarf v0.89.0,
uds-cli v0.41.0), Docker with buildx, Python 3.11 and PyYAML. From a clean
commit run `make release VERSION=0.1.0-rc1` (or `uds run release` with VERSION
exported). `ARCH=amd64` is the default. `ALLOW_DIRTY=1` is an explicit local
testing escape hatch; its manifest is marked dirty and is not a release from
the recorded commit. Do not edit sources while a build is running.

`VERSION` is the checked-in baseline for standalone packaging; `make
package-check` rejects drift between it, split package metadata, bundle
metadata and bundle package refs. The release VERSION argument overrides all
four definitions together in an isolated source copy under
`dist/release-work-<VERSION>`; source image pins are not modified. Existing
output/work directories are refused, never silently overwritten. A failed
build leaves these directories for diagnosis; archive/remove them before a
retry. Fresh image tags intentionally contain SHA/time/randomness: this is a
repeatable build procedure with recorded inputs, not a claim of byte-identical
images (upstream base tags and Python dependency resolution are not immutable).

The existing `make pin-images` path builds prod-tagged Go images, tracking-enabled
inference (`EDGEML_TRACKING=1`), and checksum-verified models. Outputs in
`dist/release/<VERSION>/` include both Zarf packages, both UDS bundles,
`sbom/{ship,shore}/` exported Zarf SBOMs, the model `THIRD_PARTY_NOTICES.md`,
`images.lock.json`, `release.json` (git SHA, architecture, refs, Docker image
IDs and repository digests when present), and `SHA256SUMS`.
Docker image IDs are recorded separately from repository digests: their meaning
depends on the Docker storage backend. Model licensing remains
**UNRESOLVED**: YOLO11n AGPL-3.0 and MobileCLIP Apple terms; notices and OCI
labels are unchanged. No model redistribution approval is implied.

Verify locally with `cd dist/release/<VERSION> && sha256sum -c SHA256SUMS`.
Artifacts above 2,000,000,000 bytes also get 1 GiB `.part0000`, `.part0001`, ...
files, each checksummed. Upload those parts rather than the oversized original
to GitHub (2 GiB asset limit). Reassemble with
`cat <artifact>.part* > <artifact>` in lexical order, then verify the original
checksum. `SHA256SUMS` covers every file except itself; when downloading parts
only, reassemble before checking the complete checksum list.

For arm64 use `make release VERSION=<new-version> ARCH=arm64` on native arm64
or a buildx builder with QEMU already configured. Every image is built with
`DOCKER_DEFAULT_PLATFORM=linux/<ARCH>` and inspected for the requested arch.
This host's builder advertises only amd64 variants, not arm64. No system-wide
QEMU/binfmt changes are made by the release script. Emulated tracking-enabled
PyTorch installs can take hours and are not built by default; arm64 release
execution remains unverified. Prefer native arm64 and verify dependencies and
model behavior there before fielding.

## Authentication is administrator-owned

Create the configured namespace and Secrets **before** deploying. No production
package or bundle generates tokens, signing keys or certificates, and no secret
values belong in the checked-in examples. Set `*_AUTH_SECRET` and the associated
`*_KEY` variables to reference existing keys (missing keys fail closed):

| Package | Secret default | Required keys (configurable) |
| --- | --- | --- |
| ship | `edge-auth` | `unit-token`, `operator-token` |
| shore | `shore-auth` | `signing-key`, `operator-token` |
| shore | `operator-auth` | `operator-token` |

Provision an expiring HS256 **unit** JWT whose subject matches `EDGE_ID`, signed
with shore's signing key. Shore and operator must share the same operator token;
edge may use a separate operator token. Use the site's approved secret manager
or Kubernetes Secret provisioning process. Roll workloads after rotating Secret
values consumed through environment variables. `SHORE_REQUIRE_AUTH` is fixed
true in the production shore package.

## Gateway TLS belongs to UDS Core

On **each cluster**, Core's `DOMAIN` must match the application's `DOMAIN`.
`SHORE_GATEWAY_HOST`, `OPERATOR_GATEWAY_HOST` and `EDGE_GATEWAY_HOST` are tenant
gateway subdomains, not full URLs. Provide a certificate valid for the gateway
FQDNs. Application listener TLS stays off behind UDS STRICT mTLS sidecars.

UDS Core 1.16's `istio-tenant-gateway` component / `uds-istio-config` chart accepts
`tls.credentialName` (existing TLS Secret in **istio-tenant-gateway**) or
`tls.cert` and `tls.key` (base64-encoded PEM, supplied securely). On the separately
managed **Core bundle**, wire these Helm values using bundle overrides, e.g.:

```yaml
# Fragment of the administrator's Core bundle, NOT an application bundle.
packages:
  - name: core
    # Keep the site's pinned repository/ref and signature verification here.
    overrides:
      istio-tenant-gateway:
        uds-istio-config:
          variables:
            - name: TENANT_TLS_SECRET
              path: tls.credentialName
              description: Existing TLS Secret in istio-tenant-gateway
```

Set that Core bundle's `variables.core.TENANT_TLS_SECRET` and
`variables.core.DOMAIN` in its site `uds-config.yaml`. Alternatively expose
`tls.cert` / `tls.key` as **sensitive: true** bundle variables and provide them
through the site's secret injection mechanism; never check them in. Do not use
Core's development wildcard certificate for fleet deployments. See the pinned
[Core chart values](https://github.com/defenseunicorns/uds-core/blob/v1.16.0/src/istio/charts/uds-istio-config/values.yaml).

Ship's `SHORE_URL` defaults to `https://shore.uds.dev:443`; set it explicitly
when changing the shore domain/host. TLS hostname verification remains enabled.
For a private CA, create `SHORE_CA_SECRET` in the **ship namespace**, containing
`ca.crt`; empty uses system roots. The client negotiates HTTP/2 (ALPN); all
proxies/load balancers on the route must preserve HTTP/2 and gRPC trailers.
Shore's Service retains `appProtocol: http2`. Split packages require
`SHORE_TRANSPORT=external` even on a shared cluster: use the shore TLS gateway
and approved gateway egress CIDR/port. Direct cross-namespace h2c is not exposed
by these packages. The legacy combined package retains its local h2c path.

## Site variables and boundaries

Zarf deployment variables go under `variables.ghost-seeker-ship` or
`variables.ghost-seeker-shore` in `uds-config.yaml` (or `--set`), **not** bundle
Helm overrides. Copy the respective example, replace documentation-only CIDRs
with approved site networks, and use `UDS_CONFIG` with the deploy task.

| Variables | Scope / default |
| --- | --- |
| `NAMESPACE`, `DOMAIN`, `*_GATEWAY_HOST`, `*_PUBLIC_HOSTS` | Separate ship/shore namespaces; match Core and Host checks |
| `*_AUTH_SECRET`, `*_OPERATOR_TOKEN_KEY`, `EDGE_UNIT_TOKEN_KEY`, `SHORE_SIGNING_KEY_KEY` | Existing Secret names/keys; no generated values |
| `SHORE_URL`, `SHORE_TRANSPORT`, `SHORE_CA_SECRET` | External HTTPS:443 by default; optional private trust roots |
| `EDGE_STORAGE{,_CLASS}`, `FRAMES_STORAGE{,_CLASS}` | ship: 10Gi each, cluster default class |
| `SHORE_STORAGE{,_CLASS}`, `OPERATOR_STORAGE{,_CLASS}` | shore: 5Gi / 1Gi, cluster default class |
| `INFERENCE_{CPU,MEMORY}_{REQUEST,LIMIT}` | ship: 500m / 1Gi requests, 4 CPU / 4Gi limits |
| `SHORE_EGRESS_{CIDR,PORT}` | ship: documentation-only /32 and 443; approve shore gateway IP range (use post-DNAT destination/port for an internal gateway) |
| `CAMERA_EGRESS_{CIDR,PORT}` | ship: documentation-only /32 and 80; optional camera pull |
| `{LLM,RESEARCH,OCSP,INFERENCE}_EGRESS_{CIDR,PORT}` | shore: narrow documentation-only /32; optional services |

All containers, including model init, have CPU/memory requests and limits.
StorageClass empty means **omit** the field and use the cluster default (not
the special empty-string class). Frames always use PVCs on UDS, never hostPath.

Package `allow` selectors are scoped with `remoteNamespace: NAMESPACE`, and
each in-mesh flow has an ingress/egress pair. UDS generates default-deny and DNS
policies. No `Anywhere`, wildcard namespaces, or default-route CIDRs are used.
Only the tenant gateway admits ingress; shore exposure is limited to `/api/v1/`
and the SyncService prefix. Restrict external **gateway source CIDRs** at the
cluster load balancer/firewall in the Core/site configuration: an application
Package cannot recover a remote ship's original source address after gateway
termination. Do not add direct pod ingress as a substitute. Approved camera
push clients use the authenticated ship gateway `/api/v1/ingest/frames` route.

The two bundles can also coexist on one test cluster because their namespaces
and Package identities differ. This is not proof of a two-cluster fleet rollout;
site DNS, routed CIDRs, PKI, capacity and secret distribution remain admin-owned.
