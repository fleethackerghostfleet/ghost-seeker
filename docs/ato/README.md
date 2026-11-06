# ATO preparation package

**Draft, not an authorization or a compliance determination.** This package maps
repository capabilities and gaps into material a program can tailor for RMF.
The proposed Moderate baseline is a planning reference, not an assigned impact
level. Program, Authorizing Official (AO), classification, impact level, ship
platform, system owner, assessor, dates and organization-defined parameters are
**TBD** until the responsible organization supplies them.

## Index

| Document | Purpose | Evidence versus template |
| --- | --- | --- |
| [SSP](SSP.md) | System description, inventory and responsibilities | Source-backed description plus unfilled program SSP fields |
| [Boundary and data flow](BOUNDARY_AND_DATA_FLOW.md) | Trust zones, interconnections and TLS boundaries | Current wiring; split ship/shore model explicitly proposed |
| [PPSM](PPSM.md) | Application ports, protocols and services | Manifest-derived worksheet, not an approved PPSM registration |
| [Control matrix](CONTROL_MATRIX.md) | Rev. 5 Moderate application-family responsibility mapping | Implementation references; tailoring/assessment/inheritance remain TBD |
| [POA&M](POAM.md) | Open findings, evidence gaps and closure criteria | Source-backed backlog; owners, deadlines and risk acceptance TBD |
| [Third-party and model inventory](THIRD_PARTY_AND_MODEL_INVENTORY.md) | Dependencies, assets and license limitations | Repository records and named generated inventories, not legal clearance |
| [AI and human review](AI_HUMAN_IN_THE_LOOP.md) | Decision support, review and promotion governance | Existing workflow distinguished from required operational approvals |

## Evidence rules

Implementation references are relative links into this repository. They show
code/configuration exists, not that it is enabled, correctly operated or assessed
in a fielded system. Descriptions were prepared from the security-findings stack
at parent revision `0fa798617f2fbb2c97aa3e22b445dff48c081be9`; subsequent source changes require
revalidation. Do not infer classification from the historical test report's
UNCLASSIFIED synthetic-prototype scope.

Generated evidence is **not embedded or automatically current**:

* `make sbom` produces `dist/security/{edge,shore,operator,edgeml-sidecar}.{cdx,spdx}.json`.
* `make security` and `make scan` produce source/dependency/configuration and image
  reports; see [pipeline policy](../security/PIPELINE.md) for tools, baselines and scope.
* `make test`, `make prod-gate` and the broader clean-commit `make verify` have
  different scopes; see [verification instructions](../VERIFY.md).
* Earlier local UDS and security test observations are engineering evidence,
  not an assessor-signed SAR, a continuous authorization, or proof of hosted
  prod-push signing. Attach the actual reports to the program evidence repository.

For each promoted release record the source commit, platform/architecture,
image digests, model manifest and approval, SBOMs, scanner/database versions,
test results (including failures/skips), signatures/attestations and reviewer.
Storage location, retention, custodian and independent assessor: **TBD**. Never
include bearer tokens, join keys, private keys or operational imagery here.

## Completion and approval

The program must categorize the actual information/system, select and tailor the
baseline, establish the authorization boundary and interconnection agreements,
validate platform inheritance, complete an official ASD STIG checklist, close or
accept POA&M items and obtain an AO decision. Other baseline families (including
AT, CP, IR, MA, MP, PE, PL and PS) remain program/platform responsibilities to
assess; their absence from the application matrix is **not** a not-applicable
determination. No ATO, Iron Bank acceptance, FIPS validation or UDS inheritance
approval is asserted by these documents.
