# Deployment networking

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
checker preserves these placeholders while inspecting the unrendered package;
`check.sh` substitutes the documented defaults before kubeconform validates it.
At the site, test allowed service/DNS/routed flows and denied unrelated peers,
namespaces, destinations and ports on an enforcing CNI before fielding.
