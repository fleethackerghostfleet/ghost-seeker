# Authorization boundary and data flows

The boundary is **proposed**, not AO-approved. Classification and all program
network addresses are TBD. Labels describe current application protocols, not
an assertion that every deployment has TLS or a verified inheritance agreement.

## Current same-cluster UDS application wiring

```mermaid
flowchart LR
  user["Operator browser - external workstation"]
  sensor["Live sensor / ingest client - integration TBD"]
  subgraph platform["UDS Core 1.14.3 platform - proposed inherited zone"]
    gw["Tenant gateway - HTTPS termination"]
    mesh["Ambient mesh - transport identity and mTLS"]
    subgraph app["ghost-seeker namespace - candidate application boundary"]
      op["Operator :8090 - session/token"]
      shore["Shore :8080 - unit JWT / operator bearer"]
      edge["Edge :8081 - operator bearer ingest"]
      ml["Sidecar :9090 - no app authentication"]
      data[("PVCs - DB / blobs / reviews")]
      models[("Read-only model manifest and weights")]
      secrets[("Application Secrets")]
    end
  end
  user -->|"HTTPS TCP 443; app session or bearer"| gw
  gw -->|"HTTP backend 8090 over platform mesh path"| op
  mesh -. "protects enrolled workload hops; verify platform policy" .-> app
  op -->|"TCP 8080; server-side operator bearer"| shore
  edge -->|"TCP 8080; unit JWT; Exchange responses contain downlinks"| shore
  edge -->|"TCP 9090; Connect JSON; network/mesh trust only"| ml
  shore -. "optional SHORE_INFER_URL; TCP 9090; no app auth" .-> ml
  sensor -. "TCP 8081; authenticated push; site route NOT supplied by Package" .-> edge
  edge --> data
  shore --> data
  op --> data
  models --> ml
  secrets -. "token/key configuration" .-> edge
  secrets -. "signing and operator credentials" .-> shore
  secrets -. "operator credential" .-> op
```

The storage node above groups **separate** edge/shore/operator PVCs; it is not a
shared database. Default Zarf operator arguments set `-edge=` and `-link-listen=`:
there is no default operator-to-edge bridge or development link shaper. The
edge Service is not exposed by the UDS Package. External sensor push needs a
reviewed route and network rule; its dashed arrow is a required integration,
not existing gateway ingress. See [PPSM](PPSM.md) for optional versus permitted flows.

## Proposed split ship / shore model

```mermaid
flowchart LR
  subgraph ship["Shipboard k3s - platform, version and boundary TBD"]
    live["Live camera / ingest adapter TBD"]
    edge["Edge :8081; local DB/blob PVC"]
    ml["Local real-model inference :9090"]
    live -->|"operator-authenticated push or configured live source"| edge
    edge -->|"local HTTP Connect; protect with ship platform controls"| ml
  end
  link["Ship-shore bearer / routing / NAT / availability TBD"]
  subgraph shoresite["Shore UDS Core - candidate platform inheritance"]
    gw["Optional shore tenant HTTPS endpoint :443"]
    shore["Shore :8080; data PVC; signed policy issuer"]
    op["Operator :8090"]
    gw -->|"HTTP backend; mesh hop; app auth retained"| shore
    op -->|"operator bearer; TCP 8080"| shore
  end
  edge -->|"outbound HTTPS to approved SHORE_URL; unit JWT"| link
  link -->|"CIDR/port narrowly authorized; commonly TCP 443"| gw
  shore -. "ACKs, policy and pull requests in edge-initiated Exchange response" .-> edge
```

This is a deployment planning diagram, **not a tested second-cluster
interconnection**. Configure `UDS_CROSS_CLUSTER_SHORE`, `SHORE_EGRESS_CIDR`,
`SHORE_EGRESS_PORT`, `SHORE_URL` and receiving `UDS_EXPOSE_SHORE`; address DNS,
NAT source ranges, certificate trust and both platforms' controls. The package
does not enroll an arbitrary shipboard k3s cluster into the shore mesh or extend
its mTLS across the WAN. No unsolicited inbound shore-to-edge listener is needed
for downlinks. Agreements, endpoints and transport approval remain TBD.

## Authentication and TLS termination

| Segment | Authentication / trust | Encryption and termination |
| --- | --- | --- |
| Browser to tenant gateway | App token login/session remains required; no OIDC header integration | HTTPS terminates at gateway; certificate/trust configuration supplied by platform |
| Gateway to operator | Existing application session/bearer checks, not implicit gateway login | App listener is HTTP :8090 by default; ambient mTLS is a distinct platform hop, not browser-to-app TLS |
| Operator to shore | Bridge injects server-side operator bearer; browser credentials stripped | Default HTTP :8080; mesh protection in enrolled UDS; optional shore TLS terminates in shore |
| Edge to shore sync | HS256 JWT role/unit subject/expiry and item ownership | Default local HTTP :8080; opt-in TLS 1.3 listener and CA trust; remote gateway HTTPS ends at gateway unless site config adds listener TLS |
| Edge/optional shore to sidecar | No application bearer, user or unit authentication | Plain HTTP :9090; rely on explicit network rules/ambient mesh or approved ship-local isolation |
| Sensor/client to edge ingest | Operator bearer; not unit JWT | HTTP :8081 by default; optional TLS 1.3 at edge, or reviewed site ingress |
| Optional direct PIV browser to operator | Client certificate chain, mapped user/role and CRL checks | TLS terminates in operator app; ordinary TLS termination at tenant gateway loses the direct client certificate handshake |

PIV is optional, not the packaged default. Gateway TLS forwarding headers are
not a PIV/OIDC adapter; passthrough or a separately reviewed identity design is
needed. Certificate provisioning, approved crypto/FIPS modules, all platform
mTLS assertions and operational TLS configuration require assessment.

## Supply-chain and administrative trust zones

Build/test tools retrieve sources/dependencies/bases and produce images, SBOMs
and reports. The security workflow can publish, keylessly sign and attest only
on guarded prod pushes. Sites provision Secrets and read-only verified model
bundles separately. These are administrative/promotion flows, not runtime
internet permissions for the application. Optional LLM, research, cloud datasets,
camera-network and on-prem OCSP connections require separate approval and rules;
the Package does not allow them merely because a portable policy variable exists.

Evidence: [UDS Package](../../deploy/zarf/manifests/uds-package.yaml),
[UDS deployment contract](../../deploy/uds/README.md),
[portable network policies](../../deploy/zarf/manifests/network-default-deny.yaml),
[TLS helper](../../internal/httpx/tls.go), [operator PIV](../../internal/opauth/certs.go),
[unit auth](../../internal/unitauth/auth.go), [pipeline](../security/PIPELINE.md).
