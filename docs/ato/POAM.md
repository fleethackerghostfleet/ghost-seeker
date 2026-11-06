# Plan of Action and Milestones — draft

This is an engineering backlog, **not an AO-approved POA&M or risk acceptance**.
For every item, accountable owner, resources, milestone dates, scheduled
completion, risk acceptance authority and evidence-repository location are
**TBD**. Severity marked TBD needs program assessment; historical CAT labels
are the original report author's estimates, not DISA determinations. Closure
requires current configuration-specific evidence and independent review.

## Current open work and release decisions

| ID | Issue / source | Severity | Status and required closure evidence |
| --- | --- | --- | --- |
| ATO-01 | EMCON SILENT still transmits a heartbeat each tick; [SECURITY §7](../SECURITY.md#7-deep-scan-before--after) | Low (source) | Open; decide required silence semantics, implement/test or explicitly accept operational traffic |
| ATO-02 | Attribute-only suppress can defeat command-locked priority rule; same source | Low (source) | Open; specify lock semantics and regression-test enforcement |
| ATO-03 | `test_rpc_http_override_and_unexpected_engine_exception` expects raw exception text; actual sanitized response; [protocol tests](../../edgeml/tests/test_sidecar_protocol.py) / [server](../../edgeml/src/edgeml/sidecar/server.py) | TBD; test/contract mismatch | Open; reconcile intended secure contract and test through approved change; do not restore information leakage to get green |
| ATO-04 | Negative Content-Length test expects 413; actual 400; same protocol tests | TBD; test/contract mismatch | Open; document intended malformed-length status and fix discrepancy. Last Task 4 suite: 2 failed, 614 passed, 3 skipped; not an all-green suite |
| ATO-05 | FIPS-validated cryptography evidence absent; [TLS helper](../../internal/httpx/tls.go), [JWT](../../internal/unitauth/auth.go), [Ed25519](../../internal/policy/signing.go) | TBD; authorization blocker if required | Open; identify approved modules/certificates, operating modes and TLS/signature policy across Go, mesh/gateway and platform; verify actual builds. TLS/hash use is not FIPS validation |
| ATO-06 | No repository LICENSE; [pipeline license policy](../security/PIPELINE.md) | TBD; legal/release decision | Open; owner selects license and legal approves first-party distribution/ownership; dependency gate does not resolve it |
| ATO-07 | Ultralytics YOLO code/weights and derivatives require licensing decision; same policy and [inventory](THIRD_PARTY_AND_MODEL_INVENTORY.md) | TBD; legal/release decision | Open; evaluate AGPL/commercial terms and selected exported weights, obtain written authorization or replace. ONNX export does not clear model rights |
| ATO-08 | Optional BoxMOT tracking inventory carries AGPL-3.0; same sources | TBD; legal/release blocker for variant | Open/blocked for fielding; default stateless fallback does not require this package. Approve rights or replace; no GPL/AGPL gate exception exists |
| ATO-09 | Internal-only/unknown-rights datasets; [registry](../../datasets/registry.yaml) | TBD; legal/data-governance decision | Open; exclude `ir_maritime_vessels` from shipped models (`internal_dev_only`); establish licenses/lineage for all training and evaluation assets; registry metadata alone is not legal clearance |
| ATO-10 | Keycloak client registration is not app SSO; [UDS guidance](../../deploy/uds/README.md) | TBD | Open; OIDC callback, issuer/audience/signature validation, session/CSRF and claims-to-role integration plus adversarial tests; disabled by default |
| ATO-11 | Enabled OIDC and cross-cluster paths untested operationally; [UDS contract](../../deploy/uds/README.md) | TBD | Open; test real identity integration and second-cluster HTTPS sync, NAT/CIDRs, trust/expiry/failure cases and boundary policy. Local same-cluster frame delivery does not close this |
| ATO-12 | Official Application Security and Development STIG checklist not completed; [historical review](../security/STIG_PENTEST_REPORT.md) | TBD | Open; select official benchmark/version and applicability, complete official checklist with current evidence and qualified assessor review |
| ATO-13 | No operational categorization, approved boundary, platform inheritance or AO decision; [SSP](SSP.md) | TBD | Open; supply categorization, ODPs, inheritance packages, agreements, assessment/SAR and authorization |
| ATO-14 | Audit identity, protection/centralization/retention and shared-token accountability; F-11 / [human review limitations](../HUMAN_REVIEW.md) | Historical CAT II; current severity TBD | Open/hybrid capability only; validate PIV identity where enabled, protect append logs, define retention/backup/failure response and SIEM correlation; reviewer display names are not authenticated identities |
| ATO-15 | Runtime storage/backup/retention and encryption; [fielding caps](../FIELDING.md), [shore limits](../../shore/store/limits.go) | TBD | Open; size PVCs including DB/WAL/reviews and replacement copies, configure edge caps, encrypt storage, test backup/restore and deletion. Quotas alone are not lifecycle management |
| ATO-16 | Release signing / admission enforcement / production platform evidence; [pipeline](../security/PIPELINE.md) | TBD | Open; capture actual prod OIDC signing/attestation and verification evidence, enforce approved digests at site, inventory host/native components and validate Iron Bank substitutions; local gates alone do not establish cATO |

## Historical STIG residual reconciliation

[STIG_PENTEST_REPORT.md](../security/STIG_PENTEST_REPORT.md) describes an older
assessment with selective status updates. Do not copy its original “no auth” or
“no crypto” statements as facts about current code. The following reconciliation
preserves residuals without falsely asserting they have been independently retested.

| ID / report references | Original severity / current status | Remaining action and current evidence |
| --- | --- | --- |
| STIG-R1 — F-01, F-03, F-04 | CAT I / CAT II historically; implementation present, reassessment open | Current [opauth](../../internal/opauth), [unit auth](../../internal/unitauth/auth.go), [HTTP guards](../../internal/httpx/httpx.go) and [SECURITY](../SECURITY.md) provide auth/Host/CORS controls. Validate selected token/PIV topology, account/role operations and boundary TLS; do not treat old report closure as current certification |
| STIG-R2 — F-05, F-06, F-07, F-08, F-15 | CAT II / CAT III historically; partial/current hardening, reassessment open | [shore](../../cmd/shore/main.go), [edge](../../cmd/edge/main.go), [SSE helper](../../internal/httpx/stream.go) and [sidecar server](../../edgeml/src/edgeml/sidecar/server.py) now have guards. Sidecar is still unauthenticated at app layer; packaged bind-all relies on network controls. Legacy [inference stub](../../inference/stub/server.py) is separate and not an approved production substitute. Retest applicable listeners and review unused stub disposition |
| STIG-R3 — F-09, F-10 | CAT II historically; remediated shipped scope, monitoring open | Toolchain/locks and scanner gates exist. Refresh actual release reports; development/eval/GPU environments and non-reachable module advisories remain separately managed |
| STIG-R4 — F-11, F-12 | CAT II historically; partial/review open | ATO-14 covers audit operations. Review error handling on all shore/edge RPCs; [sidecar server](../../edgeml/src/edgeml/sidecar/server.py) sanitization is not evidence for every service |
| STIG-R5 — F-16 | CAT III; partial remediation | [runtime integrity](../../edgeml/src/edgeml/sidecar/integrity.py) verifies approved local manifests; [evaluation/export utilities](../../edgeml/tools) still need pinned-download/provenance review. Demo TOFU is not production provenance |
| STIG-R6 — F-17 | CAT III; review open | Review unchecked conversions; [gosec baseline](../security/gosec-baseline.json) preserves debt, not a safety justification; test bounds before closing |
| STIG-R7 — F-18 related residuals, report §7 | CAT III / informational; open review | Review static UI exposure, directory/file permissions and `style-src 'unsafe-inline'`; [HTTP helpers](../../internal/httpx/httpx.go) and deployed filesystem/CSP need reassessment |
| STIG-R8 — F-19 | CAT III historically; implemented, operational verification pending | Numeric UIDs in [Go Dockerfile](../../deploy/Dockerfile), [sidecar Dockerfile](../../edgeml/Dockerfile) and manifests; verify actual substituted/platform images and volume permissions |
| STIG-R9 — residual DoS and policy TTL behavior in SECURITY §7 | Low (source); open review | Night-light cost grows with bright spots/pixels; large directive rescoring loops and EMCON policy expiry semantics need workload/operational analysis. No claim these are closed by Task 4 pixel/quota/SSE fixes |
| STIG-R10 — manifest hash follow-up in SECURITY §7 | Severity TBD; source follow-up, closure unverified | Review the documented requirement to take the expected chunk hash from the owning edge's manifest, not the submitted chunk; reconcile current [store validation](../../shore/store/store.go) and tests before accepting integrity claims |
| STIG-R11 — development auto-login proxy limitation in SECURITY §7 | Low (source); non-production boundary limitation | Development auto-login must never be proxied into an operational enclave. Verify production build tags/explicit credentials at site; do not reinterpret demo convenience as an inherited authentication control |

## Closure record template

For each ID capture: accountable owner **TBD**; planned milestones/dates **TBD**;
resources **TBD**; implemented change/release **TBD**; independent test and
negative-case evidence **TBD**; residual likelihood/impact **TBD**; acceptance or
closure authority/signature/date **TBD**. Resolved code issues should retain their
source/retest history; unresolved tests and licensing decisions must not be
silently removed from release evidence.
