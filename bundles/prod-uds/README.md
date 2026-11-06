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
Shore's Service retains `appProtocol: http2`. Only for a co-located shore in the
**same** STRICT mTLS mesh may `SHORE_TRANSPORT=in-mesh` select an explicit
`http://shore.<namespace>.svc.cluster.local:8080` endpoint with matching policy.

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
