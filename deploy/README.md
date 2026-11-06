# Deployment networking and transport

The Zarf package in `deploy/zarf` uses the dedicated `ghost-seeker` namespace.
Its required namespace component installs default-deny **ingress and egress**
for every pod, including extra workloads. Each optional component packages its
own workload-specific allows; an edge-only install does not depend on the
operator, shore or inference component to install its egress permissions.

These policies require a NetworkPolicy-enforcing CNI. They have static schema,
selector, packaging and traffic-matrix tests, not a live-cluster enforcement test.
Additional policies are additive and can widen access. Authentication and TLS
remain mandatory; allowing a TCP port does not restrict HTTP paths.

## Internal flows and DNS

| Source pod label | Destination pod label | TCP port |
| --- | --- | ---: |
| `app: edge` | `app: shore` | 8080 |
| `app: edge` | `app: edgeml-sidecar` | 9090 |
| `app: shore` | `app: edgeml-sidecar` | 9090 |
| `app: operator` | `app: shore` | 8080 |
| `app: operator` | `app: edge` | 8081 |

Both ingress and egress allow these same-namespace flows. The `operator`
Deployment runs the production `ghost-seeker` process; its packaged `-edge=`
disables edge polling, but the permission supports enabling it. Shore text
embedding is optional (`SHORE_INFER_URL`); edge requires `INFER_URL`.

Every packaged workload can reach DNS over **UDP and TCP 53** only when the
destination matches both namespace `kubernetes.io/metadata.name: kube-system`
and pod `k8s-app: kube-dns`. These selectors are in the **same peer**, not separate
OR peers. This covers conventionally labeled CoreDNS as well as kube-dns. A site
using NodeLocal DNS or other labels must add a reviewed, narrow DNS policy and
verify resolver reachability before enabling workloads. Extra pods do not
automatically receive DNS or service access.

## External egress variables

Each row provides two independently configurable Zarf variables: the full
`*_EGRESS_CIDR` name shown and its corresponding `*_EGRESS_PORT` name. All CIDRs
default to the documentation-only `192.0.2.0/32`; they are **not usable deployment
destinations**. External access therefore fails closed until configured. Use an
approved narrow destination network or gateway `/32` (IPv6 CIDRs also work),
never `0.0.0.0/0` or `::/0`. Each port is TCP and must be an integer in 1..65535.

| CIDR variable | Source workloads | Port variable / default | Application flow |
| --- | --- | --- | --- |
| `SHORE_EGRESS_CIDR` | edge, operator | `SHORE_EGRESS_PORT` / 8080 | Off-cluster `SHORE_URL`, or customized operator `-shore`; sync, console/blob/API |
| `EDGE_EGRESS_CIDR` | operator | `EDGE_EGRESS_PORT` / 8081 | Customized operator `-edge` remote status/console |
| `INFERENCE_EGRESS_CIDR` | edge, shore | `INFERENCE_EGRESS_PORT` / 9090 | Off-cluster `INFER_URL` / `SHORE_INFER_URL` |
| `CAMERA_EGRESS_CIDR` | edge | `CAMERA_EGRESS_PORT` / 80 | Optional `GS_MJPEG_URL` HTTP(S) camera pull |
| `LLM_EGRESS_CIDR` | operator | `LLM_EGRESS_PORT` / 8080 | Optional `GS_LLM_URL` / `-llm-url` OpenAI-compatible endpoint or approved gateway |
| `RESEARCH_EGRESS_CIDR` | operator | `RESEARCH_EGRESS_PORT` / 8097 | Optional `GS_RESEARCH_COMPILER_URL` local service origin |
| `OCSP_EGRESS_CIDR` | operator | `OCSP_EGRESS_PORT` / 80 | Optional `GS_PIV_OCSP_URL` responder for PIV authentication |
| `MODEL_EGRESS_CIDR` | inference | `MODEL_EGRESS_PORT` / 443 | Approved model mirror only in customized deployments; not needed by packaged inference |

Derivation: `cmd/edge/main.go` constructs the shore sync and inference clients,
and optionally calls `ingest.PullMJPEG`. `cmd/shore/main.go` only constructs an
optional text-embedding client: it does **not** call an LLM/Vertex endpoint.
`cmd/ghost-seeker/main.go` owns the operator's shore/edge bridge, optional
`command.NewLLMClient`, research compiler and PIV OCSP client. Its LLM client
expects an OpenAI-compatible API; direct Vertex SDK/OAuth/metadata calls are not
part of this package. If a deployment uses an approved Vertex gateway, configure
its destination CIDR and actual port (typically 443), not unrestricted Google IPs.

`edge-inference.yaml` uses a local `/models/.../model.onnx`; `edgeml/Dockerfile`
sets Hugging Face/Transformers offline flags. Provision models via a reviewed
mount/PVC, replacing the placeholder emptyDir, before starting inference. The
model-mirror allow is a configurable escape hatch for explicitly reviewed
customizations, not permission to download arbitrary models. Image pulls,
PVC provisioning and node clock synchronization are node/cluster operations,
not outbound calls from these application pods; inventory them separately.

Setting network variables does **not** configure application URLs, enable
optional features, add secrets, or provision routing. The optional camera,
LLM/research and PIV settings require separate manifest/env/argument changes.
The research compiler also validates origins in application code; an egress
allow does not bypass that restriction. File-drop camera ingestion needs no
camera egress. HTTP(S) redirects, proxies, dynamic DNS/CDN addresses and TLS
gateways may require additional destinations or different ports. Inventory
every actual destination seen by the CNI; add reviewed narrow site policies
for disjoint networks, simultaneous shore/inference endpoints on different
ports, or multiple DNS addresses rather than widening the CIDR to all traffic.
The shared shore/inference variable pairs assume the clients use the same
approved routed destination range and port; split them with site policies if not.

For example, on a ship cluster with an approved HTTPS shore gateway (replace
the documentation addresses with real site values):

```sh
zarf package deploy zarf-package-ghost-seeker-amd64-0.1.0.tar.zst \
  --set-variables=SHORE_URL=https://shore.example:443,SHORE_PUBLIC_HOSTS=shore.example,SHORE_EGRESS_CIDR=203.0.113.20/32,SHORE_EGRESS_PORT=443
```

On the **receiving** cluster, configure the separate `SHORE_INGRESS_CIDR` to the
ship gateway's source CIDR as observed after NAT. Ingress variables identify
sources; egress variables identify destinations. Service DNAT/SNAT and policy
evaluation order vary by CNI. Test the actual routed/Service-IP path and ports,
not just pod-to-pod traffic. These policies cannot allow by FQDN or validate TLS
names. Node traffic, kubelet probes and `kubectl port-forward` are not isolation
tests. See [FIELDING](../docs/FIELDING.md#zarf-network-isolation) and
[SECURITY](../docs/SECURITY.md#5a-zarf-network-isolation).

## Validation

Run `deploy/zarf/check.sh` for the Python checker/tests, kubeconform and Zarf lint,
then `make prod-gate`. Egress port placeholders are intentionally unquoted in the
manifests so Zarf renders integer ports, not Kubernetes named-port strings. The
checker preserves these placeholders for policy checks and uses Zarf's native
renderer to validate all eight listener-TLS/CA profiles with kubeconform.
At the site, test allowed service/DNS/routed flows and denied unrelated peers,
namespaces, destinations and ports on an enforcing CNI before fielding.

## Transport configuration

Shore and edge support opt-in server TLS 1.3. Neither `deploy/compose/` nor
`deploy/zarf/` enables it by default; their existing listener ports, URLs and
health probes remain HTTP. The optional Compose PIV operator profile is separate.

With existing unit/operator credentials provisioned (see
[Fielding](../docs/FIELDING.md#sync-authentication)), a direct deployment can use:

```sh
./bin/shore -listen :8080 -tls-cert /run/tls/shore.pem -tls-key /run/tls/shore.key
./bin/edge -shore https://shore.example:8080 -shore-ca /run/tls/ca-bundle.pem \
  -listen :8081 -tls-cert /run/tls/edge.pem -tls-key /run/tls/edge.key
./bin/ghost-seeker -shore https://shore.example:8080 -edge https://edge.example:8081 \
  -shore-ca /run/tls/ca-bundle.pem -link-listen ""
```

The operator command above configures **upstream** TLS only; use its existing
PIV/mTLS mode or an approved HTTPS proxy for the browser-facing listener.
On shore/edge, `GS_TLS_CERT` and `GS_TLS_KEY` are equivalent to the server flags.
On edge and the live operator, `GS_SHORE_CA` is equivalent to `-shore-ca`; the
operator uses that bundle for both consoles and its shore blob/REST/SSE proxy.
No CA bundle means system trust roots; supplied PEM roots are added to them.
An unpaired server cert/key, invalid key pair or unusable CA file fails startup.
No insecure verification switch is provided. Listener TLS does not replace unit
JWT authentication, operator credentials or policy-signature checks.

To enable this in Compose, supply read-only certificate/key/CA mounts through
approved secret handling, add the flags/env, change upstream URLs to `https://`,
and change HTTP health probes to HTTPS. For Zarf, use the switches below.
Confirm that the probes' TLS verification behavior meets site policy.
Cover the actual URL hostnames/IPs
in certificate SANs, including any Toxiproxy/operator TCP shaper host used by
the edge; the shaper passes TLS through without terminating it. Adjust network
policies, browser CA trust and TLS termination to the deployment. Restrict key
permissions, arrange renewal, and restart processes after replacing files.

Node consoles use same-origin browser requests; install the private CA in the
browser/OS trust store. The ship console's policy actions use its scheduler link
to shore, not a separate HTTP proxy. Inference sidecars, camera MJPEG and model
compiler connections do not use `-shore-ca`.

## Optional TLS in Zarf (v0.89.0)

The package uses Zarf v0.89's native manifest Go templating (`template: true`).
No shell wrapper, new image or duplicate workload component is needed; the
distroless images do not contain a shell. Empty Secret-name variables omit TLS
flags and volumes entirely, preserving the original plaintext render. The
shore and edge workloads are StatefulSets on this base, not Deployments; their
identity, storage, Service ports and selectors are unchanged.

| Variable | Default | Effect when non-empty |
|---|---|---|
| `SHORE_TLS_SECRET` | empty | Mount an existing `kubernetes.io/tls` Secret on shore, pass `-tls-cert`/`-tls-key`, and use HTTPS readiness/liveness probes |
| `EDGE_TLS_SECRET` | empty | The same listener configuration for edge, independently of shore |
| `SHORE_CA_SECRET` | empty | Mount an existing Secret containing `ca.crt` on edge and operator and pass `-shore-ca` to both |
| `SHORE_URL` | `http://shore.ghost-seeker.svc.cluster.local:8080` | Edge's upstream; this exact default automatically switches to HTTPS with `SHORE_TLS_SECRET` |
| `OPERATOR_SHORE_URL` | `http://shore:8080` | Operator's upstream; this exact default also switches to HTTPS with `SHORE_TLS_SECRET` |

**Custom URLs are preserved**, not rewritten: explicitly set `https://` when
targeting a TLS endpoint (including off-cluster shore). Using a private CA for
an off-cluster shore needs only the CA variable and explicit HTTPS URLs, not a
local listener Secret. Leave `SHORE_CA_SECRET` empty for system-trusted issuers.
The shared bundle can contain multiple roots; the operator also uses it for
edge if an edge upstream is configured. Its packaged `-edge=` remains disabled,
and its browser-facing listener remains HTTP. This is not operator PIV/mTLS.

Provision certificates via your approved PKI. Shore's SANs must cover both the
short `shore` hostname used by the operator and the FQDN used by edge (or change
the URLs/public-host allowlists to match your certificates). Edge's SANs must
cover its actual client/browser hostnames. Create the namespace and Secrets
**before** deploying the TLS-enabled components; certificate contents and private
keys are not packaged in Zarf:

```sh
kubectl create namespace ghost-seeker --dry-run=client -o yaml | kubectl apply -f -
kubectl -n ghost-seeker create secret tls shore-listener-tls \
  --cert=shore.crt --key=shore.key
kubectl -n ghost-seeker create secret tls edge-listener-tls \
  --cert=edge.crt --key=edge.key
kubectl -n ghost-seeker create secret generic shore-client-ca \
  --from-file=ca.crt=ca-bundle.pem

zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst --confirm \
  --set-variables SHORE_TLS_SECRET=shore-listener-tls \
  --set-variables EDGE_TLS_SECRET=edge-listener-tls \
  --set-variables SHORE_CA_SECRET=shore-client-ca
```

Names are namespace-local DNS labels (up to 63 characters); names, not key/CA
contents, are Zarf variables. TLS Secrets project `tls.crt` and `tls.key` under
`/run/listener-tls`; CA Secrets project `ca.crt` under `/run/shore-ca`. Both are
read-only mode `0440`, readable by the existing non-root pod's `fsGroup`. A
configured Secret or key is **required**: missing Secrets prevent startup;
invalid certs/keys/CA PEM fail application startup rather than falling back to
HTTP. An omitted CA mount means system roots, never skip-verification.

Listener probes retain `/healthz` and their named port, switching to `HTTPS`
with each listener's Secret. Kubernetes HTTPS probes **skip certificate
verification**; they test liveness/readiness, not trust or SAN correctness.
Application clients still verify chains and hostnames. Validate a successful
authenticated sync/operator request, a rejected untrusted client, probe health
and public-host allowlists in the actual cluster before fielding. Arrange
Secret renewal and explicitly restart shore/edge/operator to reload PEM files.
Removing listener Secret variables restores HTTP; update explicit upstream URLs
at the same time to avoid a mixed-protocol rollout.

`deploy/zarf/check.sh` requires Zarf v0.89+ and PyYAML for native rendering. It
runs the checker tests, validates all eight listener-TLS/CA on/off combinations
(including flags, Secret projections, probe schemes and upstream URLs), and
feeds those actual renders to kubeconform. It does not contact a cluster or
validate the contents/type of pre-provisioned Secrets; do not apply the raw,
unrendered Go-template manifests directly with kubectl.
