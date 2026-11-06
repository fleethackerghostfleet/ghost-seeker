# Security gates and release evidence

`security.yml` supplements (and does not replace or weaken) `prod-gate`.
PRs to dev/main/prod run secrets, Go SAST/SCA, Python SAST/SCA, dependency
licenses and manifest checks. Only prod PRs, prod pushes, Monday schedules
and manual dispatches build and scan the four production images. No extra
dev push build is added. Scheduled workflows execute the default branch;
dispatch can select a ref. Configure required checks in branch protection
separately; adding this workflow does not itself enforce merge protection.

## Local reproduction

On Linux amd64 with Go from go.mod, Python 3.11, git-lfs, curl and Docker:

```sh
export PATH="$HOME/.local/bin:$(go env GOPATH)/bin:$PATH"
git lfs pull --include web/static/imagery/empty-ocean.zip --exclude=""
for group in secrets go python manifests images; do
  bash scripts/security-install.sh "$group"
done
make security                    # source gates (fails closed)
make security-secrets GITLEAKS_LOG_OPTS="origin/dev..HEAD" # PR range
make images GO_IMAGE=mirror.gcr.io/library/golang:1.26.10-alpine PY_IMAGE=mirror.gcr.io/library/python:3.11-slim
make sbom                        # scans local images, no build/network registry fallback
make scan                        # scans all 4 even when one fails
python3 -m unittest discover -s scripts -p 'test_security.py'
actionlint .github/workflows/security.yml
make test
make prod-gate
```

The installer pins scanner releases, not vulnerability databases. Results
can change without a source change. It installs isolated scanner and shipped
dependency environments under `.venv/`. Individual targets are
`security-secrets`, `security-go`, `security-python`, `security-licenses`, and
`security-config`. `BANDIT`, `PIP_AUDIT`, `PIP_LICENSES`, `SHIPPED_PYTHON`, and
`SECURITY_PYTHON` can point to existing tools/environments. Tools must be
installed; missing tools or scan/parse errors are not successful gates.

## Policies, scope and exceptions

* **Secrets:** gitleaks scans every PR commit in base..head (not only the
  final tree), full Git history otherwise. Reports are redacted. The reviewed
  `gitleaks-baseline.json` contains 338 exact historical false positives (see
  triage below); there are no path or rule exclusions. The baseline applies
  to both modes, but a match in any new commit still fails. Any real
  credentials must be revoked, even if removed later. Never auto-refresh the
  baseline in CI; changes require review and a documented disposition.
* **Go:** gosec scans both default and prod build profiles; any new HIGH
  fails. `gosec-baseline.json` freezes 29 default / 24 prod existing HIGH
  findings at origin/dev 2267d7e using file, rule and code-context SHA256,
  ignoring line-number movement, not entire rules or directories. Duplicate
  findings beyond the baseline count fail too. These are a debt baseline,
  **not risk acceptance**: G115 integer narrowing (13 each profile, F-17),
  G404 deterministic simulation/scoring PRNGs (15 default / 11 prod), and
  G101 on the demo token hint (1 default, false positive string). The prod
  profile still compiles analysis packages even when they are not linked in
  deployed binaries; prod-gate independently checks that separation. Owners
  must review individual findings, fix unsafe conversions, and remove
  fingerprints as findings are resolved. Do not auto-regenerate the baseline
  in CI or add fingerprints merely to make a PR green. govulncheck fails on
  reachable vulnerabilities in both build profiles.
* **Python:** Bandit scans `edgeml/src`, failing HIGH; all other findings
  remain in the JSON metrics. pip-audit fails on any advisory in the resolved
  dependencies of the default sidecar: `edgeml` core plus `onnxruntime`,
  exactly the default Dockerfile install. The freeze is retained as evidence.
  Optional evaluation/GPU/detect/tracking extras and model weights are not
  shipped by `make images` and are not approved by this check. Enabling
  `WITH_TRACKING` requires extending this inventory first. Versions are
  currently resolved, not locked: a hashed deployment lockfile is follow-up
  work; the image SBOM/scan remains the authority for the actual build.
* **Licenses:** Go production command dependency closure (`GOFLAGS=-tags=prod`)
  and the isolated shipped Python environment are checked. AGPL and GPL
  variants (including dual-license expressions containing them) and unknown
  metadata fail closed. LGPL is not prohibited by this specific policy;
  this is not legal approval. **GPL/AGPL allowlist: empty.** First-party
  module/`edgeml` are excluded from dependency reports, not declared licensed:
  the repository still needs an owner-selected LICENSE. Tooling/build-only
  packages pip/setuptools/wheel are excluded by pip-licenses defaults. Model
  licenses, vendored native wheel components and assets need separate legal
  review; an Ultralytics/AGPL model cannot be approved by passing this gate.
* **IaC:** the existing Zarf v0.89 native renderer and validator emit all 8
  listener-TLS/CA profiles before Trivy scans them. Raw Go-template manifests
  are not valid YAML and must not be scanned as if they were plain YAML.
  HIGH/CRITICAL findings fail; there are no IaC suppressions.
  The `GS_DEV_TOKEN_FILE=none` disable sentinel is set directly in shore/edge
  container environment entries, not stored in ConfigMaps. This preserves
  the demo-auth-disable behavior without triggering a ConfigMap secret-name
  heuristic or suppressing detection of actual ConfigMap secrets.
* **Images:** Syft emits CycloneDX JSON and SPDX JSON per image. Trivy scans
  OS and language packages, failing fixable HIGH/CRITICAL. Unfixed advisories
  do not block this policy and still need POA&M review. Scans never pull an
  unrelated registry image when a local image is missing. Reports/SBOMs are
  retained as Actions artifacts (14 days source; 30 days images); archive
  release evidence to the program's long-term evidence store before expiry.
  The Python runtime contains neither pip, setuptools nor wheel in its
  application venv or base interpreter. They are removed after building and
  installing dependencies; runtime package installation is not supported.

## Signing and assessor evidence

Only a **push to prod** publishes scanned images to
`ghcr.io/<owner>/<repo>/<image>:<commit>`. The separate signing job requires
all source gates and the image gate to succeed, has `id-token: write`, and
signs the immutable digests with cosign keyless OIDC/Fulcio/Rekor. Both SBOM
formats are attested against those same digests. PRs (including forks),
schedules and dispatches never publish or sign and never receive OIDC
permission. GHCR package creation/write access must be enabled for the
repository's GITHUB_TOKEN. These are public transparency-log identities;
do not use this public service for classified artifacts.

Verify signatures/attestations using the exact workflow identity
`https://github.com/<owner>/<repo>/.github/workflows/security.yml@refs/heads/prod`
and issuer `https://token.actions.githubusercontent.com`. Configure admission
policy separately; this workflow does not enforce cluster verification,
pin Zarf image digests, certify Iron Bank bases, or claim an ATO/cATO.

| Evidence | ATO / DevSecOps use (program tailoring required) |
|---|---|
| Redacted gitleaks report and PR/run identity | Secret handling, SA-11 / IA-5 |
| gosec/Bandit reports, reviewed baseline | SAST, SA-11 / SI-10; open items to POA&M |
| govulncheck logs, pip-audit JSON, Trivy JSON | SCA/vulnerability monitoring, RA-5 / SI-2 |
| Dependency license inventories | Supply-chain and distribution review, SA-4 / SR family |
| Rendered manifests and Trivy config report | Configuration evidence, CM-6 / CM-7 |
| Per-image CycloneDX/SPDX, digests and SBOM attestations | Component inventory, CM-8 / SR-4 |
| Cosign signatures, OIDC identity and transparency entries | Release integrity, SI-7 / SR-11 |

Retain run URL, commit, scanner versions, database timestamp, reports and
review disposition together. An AO must accept residual risks and inherited
controls; passing automation is not authorization.

### Initial findings (2026-11-06)

The original 16 KSV-0109 detections were the explicit demo-auth-disable
sentinel, not credentials; moving the sentinel to container env resolves
them without an exception. Image scanning originally found HIGH
CVE-2026-23949 (`jaraco.context` 5.3.0) and CVE-2026-24049 (`wheel` 0.45.1)
in setuptools' vendored build tooling in both the Python base interpreter
and application venv. Removing build tooling from both runtime locations
removes those vulnerable copies rather than ignoring their CVEs.

Full-history gitleaks triage grouped **all 338 detections**, all from the
`generic-api-key` rule, by path and historical commit:

| Historical path | Count | Commit | Disposition |
|---|---:|---|---|
| `edgeml/reports/nightlights/gemini_comparison.json` | 333 | `ae0277ad7b` | SHA256 payload cache identifiers, not API credentials |
| `cmd/mint-unit-token/main_test.go` | 1 | `95af9f9c09` | Explicit test-only signing-key fixture |
| `edge/link/connect_test.go` | 1 | `c5273b71d2` | Same test-only signing-key fixture |
| `internal/unitauth/auth_test.go` | 1 | `c5273b71d2` | Same test-only signing-key fixture |
| `shore/sync/sync_test.go` | 1 | `04a0e1685b` | Same test-only signing-key fixture |
| `cmd/policy-keygen/behavior_test.go` | 1 | `a9e5dad12f` | `-id=operator-2026` CLI argument, not key material |

Triage read each test finding at its historical revision and verified all
four signing fixtures are the same deliberately named test string, used
only to exercise token/authentication behavior. The policy-keygen test
generates fresh key material under `t.TempDir()`; its flagged argument is
only the key's public identifier.

For the eval report, all 333 flagged lines were parsed and checked as
64-character hexadecimal `key` fields on evaluation examples; first,
middle and last contexts were inspected. The generator at the **same
historical commit**, `edgeml/scripts/nightlights_gemini.py:compare`, assigns
`hashlib.sha256((ENDPOINT + json.dumps(payload, sort_keys=True)).encode()).hexdigest()`
to `key` and retains that field in report examples while excluding the
payload. Authentication is obtained separately with gcloud; it is not the
job's cache key. This distinguishes cache hashes from an assumed credential
based merely on their shape. No finding looked like a real credential.

The committed baseline is the redacted gitleaks report, not an allowlist of
these paths or rules. With gitleaks v8.30.1 and `--redact`, baseline matching
still checks rule, file, exact historical commit, source coordinates,
description, entropy and commit metadata; it does not compare secret text.
Thus no credential values need be committed. The same string introduced
in a new commit is not exempt. Baseline self-scanning is excluded by
gitleaks' baseline handling, not by a repository-wide path suppression.
