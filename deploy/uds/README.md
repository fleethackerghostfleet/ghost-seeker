# Ghost Seeker on UDS Core

Target: **UDS Core 1.14.3**, UDS CLI **0.41.0** (embedded Zarf 0.89.0),
amd64. Core's default mesh is **Istio ambient**. The namespace and Package
explicitly select ambient; no privileged Istio init-container exemption is
needed. This bundle deploys the application **on an existing Core installation**;
it does not install or replace a production cluster's controls.

The default sidecar uses `--tracker auto`: it returns real stateless detections
when BoxMOT is absent, and retains ByteTrack when the tracking dependencies are
installed. Explicit BoxMOT tracker requests fail during startup if dependencies
are missing. The Zarf command remains model-manifest verified, with no mock
fallback. The separate tracking image remains AGPL-3.0 license-blocked as
documented in PIPELINE.md; do not silently substitute it.
The image also includes the canonical night-light catalog/configuration needed
by Analyze, without depending on the demo's host catalog mount.

## Local deployment

Install Docker, UDS CLI 0.41.0 and k3d 5.8.3, plus the repository's documented
Zarf/check tooling. The local Core bundle creates the `uds` k3d cluster and
binds host ports 80/443; do not run it against a shared or production cluster.

```sh
make uds-core-dev  # pinned official k3d-slim-dev bundle; includes Pepr, Istio and Keycloak
kubectl config current-context  # verify k3d-uds before provisioning
kubectl apply -f deploy/zarf/manifests/namespace.yaml
```

Create the auth and policy Secrets using [FIELDING.md](../../docs/FIELDING.md#enable-in-kuberneteszarf).
Use owner-only local key files; never put credentials in bundle variables or
commit them. Shore and edge must have matching public policy keys. The policy
signing private key never goes to edge. Missing credentials intentionally
prevent startup; this package does not generate test credentials automatically.

The bundle selects shore, operator, edge and edge-inference. Before deploying,
provision an existing model PVC and populate it with an approved bundle:
`yolo11n-640/model.onnx`, its `meta.json`, the `mobileclip-s1` directory,
all auxiliary files, and root `manifest.json`. Set `MODELS_PVC` to that claim.
The sidecar mounts it read-only and verifies the manifest before loading any
model. Use a site-approved storage class and a separately approved promotion
process for production. `make demo-models` provides a **demo-only TOFU** bundle
for local integration testing, not production provenance approval. A temporary
rootless upload pod can mount the claim read-write; remove it after upload.
On Zarf-managed clusters that pod's image must also be delivered through Zarf;
the agent rewrites even public image references to the private registry. A
packaged `ghost-seeker/edgeml-sidecar:0.1.0` image can run a Python upload helper
instead of its normal entrypoint once its image has been pushed during deploy.
Without a populated model claim the inference component deliberately fails
closed. Frames are optional: `FRAMES_PVC` empty uses an emptyDir; setting it
mounts an existing sensor claim read-only, without any hostPath exemption.

```sh
make images
make package-check
make uds-bundle
make uds-deploy-dev UDS_DEPLOY_ARGS='--set MODELS_PVC=ghost-seeker-models'
kubectl -n ghost-seeker get pods,packages
curl -k --resolve ghost-seeker.uds.dev:443:127.0.0.1 https://ghost-seeker.uds.dev/
```

`-k` is only for the local Core development certificate; production uses a
trusted gateway certificate. Log in using the provisioned operator bearer token
(do not print it in shell output). The application retains its token-to-session
cookie/PIV authorization; the ingress gateway is not an authentication bypass.

Zarf variables are available through `uds deploy --set NAME=value` (or a local
`uds-config.yaml` `variables.ghost-seeker` map). For example:

```sh
make uds-deploy-dev UDS_DEPLOY_ARGS='--set MODELS_PVC=approved-models --set OPERATOR_HOST=console --set UDS_DOMAIN=example.mil'
```

`UDS_DOMAIN` must match the Core cluster's configured tenant domain; it does not
reconfigure Core. `OPERATOR_HOST`, `SHORE_HOST` and `EDGE_HOST` are **subdomain
labels**, not full hostnames: Core appends its domain. Shore/edge Host allowlists
retain internal service names and add their gateway FQDNs. Their CORS allowlist
adds only `https://OPERATOR_HOST.UDS_DOMAIN`; no wildcard origin is permitted.
The operator proxies browser requests to shore, so no cross-origin browser API
permission is needed for normal console use. Gateway TLS terminates before the
mesh's mTLS hop; listener TLS/CA variables remain available for site end-to-end
TLS deployments.

## Network contract

The Package generates both ingress and egress rules for exactly these flows:

| Source | Destination | TCP port | Condition |
| --- | --- | --- | --- |
| edge | shore | 8080 | local sync |
| edge | edgeml-sidecar | 9090 | local inference |
| operator | shore | 8080 | server-side console bridge |
| shore | edgeml-sidecar | 9090 | `SHORE_INFER_URL` nonempty (local text embedding) |
| edge | `SHORE_EGRESS_CIDR` | `SHORE_EGRESS_PORT` | `UDS_CROSS_CLUSTER_SHORE=true` |

Core provides DNS/mesh control-plane rules; there is no broad `IntraNamespace`,
`Anywhere`, cloud metadata or Kubernetes API permission. Existing portable
NetworkPolicies remain as defense-in-depth; Kubernetes allow policies are
additive, not an intersection. Core also generates mesh AuthorizationPolicies
from the Package. Optional external camera, LLM, research, OCSP or remote
inference integrations need separately reviewed explicit Package rules; their
portable CIDR variables alone are not sufficient on UDS.

For cross-cluster sync set `UDS_CROSS_CLUSTER_SHORE=true`, a narrow real
`SHORE_EGRESS_CIDR`, `SHORE_EGRESS_PORT` (typically 443), and `SHORE_URL` to the
remote authenticated HTTPS endpoint. The validator rejects unrestricted CIDRs
and the documentation-only default. In the receiving cluster set
`UDS_EXPOSE_SHORE=true` to expose `SHORE_HOST.UDS_DOMAIN` on the tenant gateway.
This exposes the shore listener (sync, API and static UI), **not only one RPC**;
unit JWT and operator bearer authentication remain mandatory. Site ingress
restrictions and certificate trust must be configured independently. The edge
console is not exposed by this Package.

## Optional SSO and monitoring

`UDS_SSO_CLIENT_ENABLED=false` by default. Enabling it only registers an OIDC
client in Keycloak; it does **not** enable Authservice or sign users into Ghost
Seeker. `cmd/ghost-seeker` accepts token/session-cookie or direct PIV TLS
authentication, not OIDC identity headers. There is no `/oidc/callback` handler
yet. Production OIDC requires authorization-code/PKCE callback handling, token
signature/issuer/audience validation, session/CSRF handling, and an explicit
claims-to-roles mapping. A trusted-header adapter would also need protection
against header spoofing and a reviewed gateway-to-app trust boundary. Do not
enable Authservice selectors or trust arbitrary forwarded identity headers as a
substitute. Direct PIV TLS verification cannot be moved behind TLS termination
without a separately reviewed design.

No ServiceMonitor is created: the shipped Go services/sidecar have health and
JSON diagnostic endpoints, but no Prometheus scrape endpoint. Core logging and
runtime controls are inherited; metrics instrumentation remains future work.

The operator keeps its append-only review audit log on the `operator-data` PVC
(`OPERATOR_STORAGE=1Gi`), while retaining a read-only root filesystem. Its single
replica uses Recreate updates to avoid RWO multi-attach during an upgrade.

## Schema and evidence

`make package-check` renders all listener TLS combinations, checks UDS networking,
Secret mounts, numeric non-root UIDs, drop ALL and no hostPath/escalation, then
runs strict kubeconform with the actual Core Package schema. The schema is
downloaded from pinned commit `ee479114762badb92eaf9b851779db55a76b0060` and
SHA-256 checked before extraction; no unknown-CRD skip is used.

Official references (Core 1.14.3):

- [Package CRD](https://github.com/defenseunicorns/uds-core/blob/v1.14.3/src/pepr/uds-cluster-crds/templates/packages.uds.dev.yaml)
- [Package API](https://github.com/defenseunicorns/uds-core/blob/v1.14.3/docs/reference/operator-and-crds/packages-v1alpha1-cr.md)
- [Local slim bundle](https://github.com/defenseunicorns/uds-core/tree/v1.14.3/bundles/k3d-slim-dev)

Before fielding, retain pod readiness, Package Ready status, gateway HTTP results,
authenticated sync evidence and Pepr admission logs alongside the SBOM/signing
evidence described in [PIPELINE.md](../../docs/security/PIPELINE.md). Local
integration is not an authorization to operate.
