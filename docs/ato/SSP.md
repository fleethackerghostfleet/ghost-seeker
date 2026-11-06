# System Security Plan — draft skeleton

## 1. Identification and categorization

| Field | Value |
| --- | --- |
| Repository system name | Ghost Seeker |
| Program / authorization identifier / version to authorize | TBD |
| System owner / information owner / ISSM / ISSO / AO / assessor | TBD |
| Classification, handling caveats and releasability | TBD |
| Confidentiality / integrity / availability impact; DoD impact level | TBD / TBD / TBD; TBD |
| Operational mission, deployment sites and ship platform | TBD |
| Authorization boundary, connected-system owners and agreement IDs | Proposed below; approval TBD |
| Assessment dates, lifecycle stage and authorization expiration | TBD |

Moderate is used only to organize the [control worksheet](CONTROL_MATRIX.md).
Neither a historical prototype classification nor a working deployment establishes
the program's categorization.

## 2. Purpose and system description

The application ingests live image frames at an edge, runs local inference,
prioritizes observations and schedules stored forms across a constrained
ship-to-shore link. Shore stores received items and serves an authenticated
versioned output API and console. The operator application proxies shore
operations to a browser. See [FIELDING](../FIELDING.md), the
[API specification](../api/openapi.yaml), [ingester](../../edge/ingest/ingest.go),
[scheduler](../../edge/scheduler) and [shore API](../../shore/api).

ML outputs are decision-support observations and priority recommendations, not
engagement decisions. The repository does not establish weapons integration or
engagement authority. Actual mission use, safety case and prohibited uses: TBD.
See [human-in-the-loop scope](AI_HUMAN_IN_THE_LOOP.md).

## 3. Users, roles and administration

| Principal | Repository capability | Operational responsibility / gap |
| --- | --- | --- |
| Edge unit | HS256 unit JWT; subject bound to edge ID; shore checks item ownership | Provisioning, distribution, expiry/rotation and revocation procedures TBD |
| Operator bearer holder | Shared console/API transport credential, browser session login | Not individual identity or a complete per-person RBAC/account lifecycle |
| PIV user | Optional direct operator-app TLS client certificate verification; mapped operator/viewer roles | PKI, CRLs, approved products and direct TLS deployment required; not wired by default UDS route |
| Command policy signer | Trusted Ed25519 policy signature required for higher-echelon changes | Signer authority, custody and approval process TBD; shore's signer cannot authorize above-ship changes |
| Reviewer | Review/correction actions; optional restricted join-link sessions in documented review workflow | Self-reported names are labels, not verified identities; operational review-mode approval TBD |
| Platform/deployment administrator | Installs images, Secrets, PVCs, network/gateway configuration | Kubernetes/host IAM, separation of duties and access reviews TBD |

Evidence: [security model](../SECURITY.md), [unit auth](../../internal/unitauth/auth.go),
[operator auth](../../internal/opauth), [policy handler](../../shore/console/console.go),
[review workflow](../HUMAN_REVIEW.md). Keycloak client registration is optional
and **does not implement application SSO**.

## 4. Proposed boundary and deployment models

**Application boundary candidate:** edge and shore Go services, Python inference
sidecar, operator Go/web application, their persistent SQLite/blob/review data,
approved model bundle, configuration and application Secrets. Final boundary and
classification of each asset: TBD. Browser workstations, sensors, communication
links, PKI, registries, build infrastructure, Kubernetes nodes/control plane,
UDS Core and research/cloud data services are external dependencies unless the
program explicitly includes them. See [diagrams](BOUNDARY_AND_DATA_FLOW.md).

* **Repository UDS model:** [UDS bundle](../../deploy/uds/uds-bundle.yaml) deploys
  the application onto existing UDS Core **1.14.3**, using ambient mesh and a
  namespace Package. The local Core target is k3d-slim-dev, not an approved
  production shore platform. The default application bundle includes edge,
  inference, shore and operator in one namespace/cluster.
* **Split operational model to finalize:** shore/operator on UDS Core and
  edge/inference on shipboard k3s. Ship hardware, k3s version/hardening, component
  selection, storage, sensor integration and fleet topology: **TBD**. This is
  not evidence of a validated shipboard installation. Cross-cluster CIDR/port
  egress and optional shore gateway exposure exist, but a second-cluster path
  has not been operationally verified.
* **Portable/lab models:** [Kubernetes/Zarf and Compose](../../deploy/README.md)
  exist. Development replay, mocks and relaxed demo authentication are not
  production controls. Production Go images use `-tags prod`; preserve
  [prod-gate](../../scripts/prod-gate.sh).

UDS Core is a candidate control provider, not automatically accepted inherited
authorization. Provider package, version-specific evidence, shared responsibilities,
platform accreditation and continuous monitoring agreements: TBD.

## 5. Interconnections and information

Ship edge initiates sync to shore: manifests, chunks, heartbeat/status and label
corrections; responses carry ACKs and authorized downlink/policy/pull information.
The browser uses operator sessions; the operator bridge uses server-side bearer
auth to shore. Edge calls local inference; optional shore text embedding calls
the sidecar. Authentication is not encryption: app HTTP is still the default;
gateway/mesh or explicitly configured listener TLS must protect the actual path.
See [PPSM](PPSM.md) and [TLS boundaries](BOUNDARY_AND_DATA_FLOW.md).

Data includes image forms (FACT/THUMB/CROP/FULL as defined in the
[item schema](../../proto/ghostseeker/v1/item.proto)), detections, embeddings,
sightings, timestamps/source/optional location, priorities, policies/directives,
reviewer labels/notes/corrections and security/operational logs. Credentials and
policy-signing private keys are sensitive security material. Actual PII/CUI,
classification, retention, dissemination, minimization and destruction rules
for imagery, location and reviewer information: **TBD**. Dataset catalogs and
research assets do not establish operational data rights or classification.

## 6. Hardware/software inventory and storage

| Component | Source inventory | Deployment record required |
| --- | --- | --- |
| Shore, edge, operator | [Go module](../../go.mod), [Go image recipe](../../deploy/Dockerfile) | Release commit/image digest and SBOM; Go 1.26.10 declared |
| Inference sidecar | [image recipe](../../edgeml/Dockerfile), [default lock](../../edgeml/requirements.lock), optional tracking lock | Python 3.11 image recipe; exact release/image SBOM and selected variant |
| Models, weights, datasets, browser libraries | [third-party inventory](THIRD_PARTY_AND_MODEL_INVENTORY.md) | Approved hashes, immutable sources, licensing decisions and model evaluation |
| UDS / Kubernetes / OS / crypto / container runtime | [UDS deployment guidance](../../deploy/uds/README.md) | Core 1.14.3 reference; actual platform SBOM, patches and provider evidence TBD |
| Sensors, ship compute/GPU, storage and network appliances; workstations | No operational hardware inventory supplied | Manufacturer, model, serial, firmware, location, owner, capacity, redundancy: TBD |

Generate CycloneDX/SPDX image inventories with `make sbom`; filenames and
release-evidence handling are in the [index](README.md). SBOMs do not inventory
all externally mounted models, ship equipment or platform services automatically.

Shore/edge use persistent data volumes; operator review log uses its own PVC;
models and optional frames are read-only mounts. Zarf contexts are non-root,
read-only-root, no privilege escalation, drop ALL, RuntimeDefault seccomp.
See [manifests](../../deploy/zarf/manifests). Storage encryption, backups,
restoration tests, audit retention, disk capacity and disposal are TBD platform
obligations. Blob quotas do not cap SQLite/WAL or replace storage management.

## 7. Control implementation, assessment and approvals

Use the [matrix](CONTROL_MATRIX.md) for responsibility/evidence and the
[POA&M](POAM.md) for unresolved work. Attach approved configuration, sanitized
test reports, platform inheritance documentation, interconnection agreements,
STIG checklist and independent assessment results. Business continuity, incident
response, staffing/training and supply-chain governance plans are TBD. System
owner review, assessor recommendation, AO decision and accepted residual risks:
**TBD**. This draft cannot authorize operation.
