# Ports, Protocols and Services Management worksheet

**Not a registered/approved PPSM record.** Program, enclave, owners, endpoints,
classification, mission justification, boundary devices and registration IDs:
TBD. Application traffic below is TCP unless noted. No assigned PPSM approval
number or port-based security exemption is implied.

Primary sources: [UDS Package](../../deploy/zarf/manifests/uds-package.yaml),
[Zarf variables](../../deploy/zarf/zarf.yaml),
[shore](../../deploy/zarf/manifests/shore.yaml),
[edge](../../deploy/zarf/manifests/edge.yaml),
[operator](../../deploy/zarf/manifests/operator.yaml),
[inference](../../deploy/zarf/manifests/edge-inference.yaml) and
[portable policy contract](../FIELDING.md#zarf-network-isolation).

## Application and gateway flows

| ID | Initiator → listener | Destination port / protocol / service | Authentication | TLS / enabling condition |
| --- | --- | --- | --- | --- |
| P1 | Browser → tenant gateway → operator | 443 HTTPS externally; backend 8090 HTTP; UI/API/session/SSE | Application token/session; optional direct PIV is a different deployment | Default Package exposure `OPERATOR_HOST.UDS_DOMAIN`; gateway terminates TLS, platform mesh hop |
| P2 | Edge → shore | 8080 HTTP Connect SyncService; responses carry ACK/downlinks | Unit HS256 JWT, subject matches edge ID; shore item ownership | Allowed ingress/egress Package pair; optional shore listener TLS uses same port |
| P3 | Operator → shore | 8080 HTTP console RPC, authenticated API/blob proxy | Server-side operator bearer | Allowed Package pair; optional listener TLS + shore CA |
| P4 | Edge → sidecar | 9090 HTTP Connect JSON Analyze/Capabilities | No app auth; network/mesh isolation | Allowed Package pair; sidecar image does not provide an app TLS listener |
| P5 | Shore → sidecar | 9090 HTTP Connect EmbedText | No app auth; network/mesh isolation | Package pair only if `SHORE_INFER_URL` nonempty |
| P6 | Remote ship edge → approved shore endpoint | `SHORE_EGRESS_PORT`, typically 443 HTTPS | Unit JWT on sync; operator bearer required for shore reads/console | `UDS_CROSS_CLUSTER_SHORE=true`; narrow `SHORE_EGRESS_CIDR`; reviewed `SHORE_URL`; not cross-cluster tested |
| P7 | External client → optional shore tenant exposure | 443 HTTPS externally; backend 8080 HTTP | Unit JWT for sync; operator bearer for `/api/v1`, console and blobs | `UDS_EXPOSE_SHORE=true`; exposes entire listener, not a single path; platform/site ingress constraints required |
| P8 | Site sensor/client → edge | 8081 HTTP authenticated frame push / console / blobs | Operator bearer | Edge Service exists; **no default UDS gateway exposure or external sensor allow rule**; site-reviewed route required; optional edge TLS on same port |
| P9 | Operator → edge | 8081 HTTP console RPC | Operator bearer | Portable wiring can support it, but packaged operator `-edge=` disables it and UDS Package has no pair for it |

The Package selects apps within `ghost-seeker`; it does not broadly permit all
namespace traffic. Paired ingress/egress rules are one logical flow, not two
independent services. Optional arbitrary remote inference cannot be authorized
by the fixed same-namespace sidecar selector alone. Kubernetes NetworkPolicy
allow policies are additive, not an intersection; verify effective mesh and
network policy together at the actual boundary.

## Platform, probes and non-runtime traffic

The shipped services have no Prometheus scrape endpoint and the UDS Package has
no `monitor`/ServiceMonitor declaration. No metrics listener port is registered
here; health probes and SSE are not Prometheus monitoring.

| Service | Ports / scope | Responsibility / limitation |
| --- | --- | --- |
| Kubernetes readiness/liveness | Shore 8080 `/healthz`, edge 8081 `/healthz`, sidecar 9090 `/healthz`, operator 8090 `/`; HTTP/HTTPS as configured | Kubelet/platform probes, not operator-authorized data services; health/static/login surfaces are not all authenticated |
| DNS / ambient mesh / gateway control plane | Core-managed; exact deployed ports, protocols and endpoints TBD | Not enumerated as arbitrary application permissions; obtain platform PPSM/effective-policy inventory |
| Local Core dev gateway | Host 80/443 in pinned k3d dev bundle | Development only; port 80 redirect/behavior must be verified at site, not assumed approved |
| Optional on-prem OCSP, live camera, LLM/research service | Configured endpoints/ports TBD; off/not covered by default Package | Separate site interconnection/network approval required |
| Registry/dependency/model promotion | Build/admin HTTPS endpoints; actual endpoints TBD | Separate pipeline/admin zone; not runtime app egress approval |
| Kubernetes/node management, storage, PKI and time services | TBD from deployed platform | Host/control-plane rules not discoverable from app manifests; append provider inventory |
| Demo link shaper | TCP 18080 | Disabled by packaged operator `-link-listen=`; not an operational boundary service |

Optional enabling variables, real CIDRs after NAT, trusted CA roots, gateway
certificates, ship link characteristics, accountable service owners and expiry/
review dates must be recorded before registration. No public camera, research,
Kubernetes API or cloud metadata access is granted by the application Package.
