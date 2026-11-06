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
make images                      # digest-pinned base defaults (including mirrors)
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
  remain in the JSON metrics. pip-audit fails on any advisory in
  `edgeml/requirements.lock`, with hash enforcement. The license environment
  is installed from that same lock. This is the default CPU sidecar closure
  (core, ONNX Runtime and ONNX for external-data integrity validation).
  Optional evaluation/GPU/detect extras and model licenses are not approved
  by this gate. Tracking has a separate hashed lock; see the restrictions below.
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
`ghcr.io/<owner>/<repo>/<image>:<commit>`. The image job only saves the
scanned images as a short-lived artifact; the separate publish-and-sign job
requires all source gates and the image gate to succeed before it pushes
anything, has `id-token: write`, and signs the immutable digests with cosign keyless OIDC/Fulcio/Rekor. Both SBOM
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

## Pinned Python image dependencies

`make lock-edgeml` requires `uv==0.9.5` (install it in a tooling venv).
`UV=/path/to/uv make lock-edgeml` selects that executable. The script compiles
`pyproject.toml` plus the runtime inputs for Python 3.11 / Linux amd64
(manylinux 2.28), with hashes for every resolved transitive dependency.
Review lock diffs on updates; other Python versions/architectures need their
own reviewed resolution and smoke test. Development dependency ranges remain
in `pyproject.toml`. Existing lock versions are retained unless constraints
change; use uv's `--upgrade` when deliberately refreshing the full default inventory.
The tracking compile refreshes its full inventory and compares available
versions across PyPI/the CPU index (`unsafe-best-match`); review those updates
and their hashes rather than trusting the CPU index as the source for unrelated packages.

* `requirements.lock`: default sidecar. pip uses `--require-hashes` and binary
  wheels only; it cannot silently resolve a new dependency.
* `requirements-tracking.lock`: full optional `WITH_TRACKING=1` inventory,
  including pinned CPU Torch/Torchvision versions and PyTorch-index wheel
  hashes. No unhashed index fallback is used. `filterpy` is source-only: its
  sdist is hashed and built without build isolation using the hashed
  `requirements-build.lock` toolchain and installed core scientific deps.
* `requirements-headless.lock`: pinned headless OpenCV replacement for
  BoxMOT's GUI OpenCV dependency, retaining the original slim-image behavior.
  The replacement is also hashed, not an unconstrained post-install fixup.
* `requirements-build.lock`: pip/setuptools/wheel used to build the local
  package without dependency resolution or isolated build downloads. These
  tools are removed from the runtime, including the base interpreter.

The optional tracking image is **not approved for fielding by the default
license gate**: BoxMOT carries AGPL licensing and its distribution includes
additional model-download utilities. Enabling it requires a separate legal
decision and scanning its complete locked inventory/image. No GPL/AGPL
allowlist entry or vulnerability exclusion was added. To audit that inventory:

```sh
pip-audit -r edgeml/requirements-tracking.lock --require-hashes --disable-pip
docker build -f edgeml/Dockerfile --build-arg WITH_TRACKING=1 -t edgeml-tracking .
```

Build the sidecar from the repository root: its Dockerfile-specific context
allowlist includes only Python sources/locks and the two canonical night-light
catalog files. `python3 scripts/test_sidecar_image.py [image]` verifies packaged
night-light analysis with networking disabled, a read-only root and no host mounts.

The lock fixes Python resolution, not bit-for-bit OS builds: Debian packages
(currently `libgomp1`), timestamps and upstream build backends still require
a program-controlled package mirror/snapshot for full reproducibility.

## Model provenance and runtime integrity (F-16)

No model is downloaded or baked into the shipped image. The real sidecar
loads local ONNX/OpenVINO exports; offline HF settings are retained. Before
constructing any engine/provider, `--model-manifest` (or
`EDGEML_MODEL_MANIFEST`) is mandatory whenever external model/policy files
are used. Model-free `--mock` and Tomer `NoObjects` modes need no manifest.
The Zarf inference manifest points to `/models/manifest.json` alongside the
operator-provisioned, read-only model bundle; its emptyDir remains a placeholder.

The approved manifest is JSON, relative to its own directory:

**Demo exception to provenance approval, not verification:** `make demo-models`
also creates `manifest.json` for the downloaded/cached YOLO and MobileCLIP
directories, including calibration and auxiliary files. This demo-only helper
trusts the local bytes on first use and records their SHA-256 as the revision;
it does not attest upstream provenance. Existing manifests are checked, not
silently refreshed when files change. Review an intentional demo bundle update
before explicitly deleting its manifest and rerunning `make demo-models`.
Demo Compose (including the language override) and `make demo-sidecar` pass
this manifest with the models mounted read-only in Compose. Never use the
demo helper to approve production artifacts: Zarf still requires an
independently reviewed manifest. No unverified-model bypass was introduced.

```json
{
  "schema_version": 1,
  "artifacts": [
    {
      "path": "yolo11n-640/model.onnx",
      "sha256": "<64 lowercase hex characters from the approved artifact>",
      "source": "<upstream repository URL or controlled export artifact URI>",
      "revision": "<40 lowercase hex upstream commit SHA>"
    }
  ]
}
```

List **every** file in each selected model directory: weights, metadata,
OpenVINO BIN/XML, ONNX external tensors, embedding text caches, tokenizer
files and calibration JSON. Paths cannot escape the bundle; symlinks,
missing/unlisted files, mutable revisions and SHA-256 mismatches fail startup.
ONNX external tensor references must also resolve to hash-approved files.
Controlled exports can use `revision: "sha256:<64 hex artifact digest>"`
instead of a Git commit, with the upstream inputs recorded in the export's
reviewed provenance. Download Hugging Face sources in the controlled export
job using `revision=<full commit SHA>`, not a branch/tag, verify expected
artifact hashes there, then approve the exported bundle and manifest.

The manifest is an **operator trust input**, not a signature or proof that
an upstream publisher is trusted. Obtain expected hashes/revisions from a
reviewed source, store the approved manifest with release evidence, and mount
it and the models read-only before startup (do not compute/trust a new manifest
inside the running container). Do not mutate files after startup: lazy text
caches and weights rely on that read-only mount. Inference does not fetch
missing models. `botsort_reid` requires explicit local `reid_weights` in
`--trk`; default BoxMOT weight discovery is not allowed. The optional torch
text tower uses `text_model.pt` inside the verified embedding directory,
never an OpenCLIP pretrained tag; SigLIP also needs its local `tokenizer/`.
The default lock does not include Torch/OpenCLIP; requesting that optional
tower still requires a separately reviewed image with those dependencies.

Download-path review and boundary:

| Path | Use / disposition |
|---|---|
| `runtime/detectors.py`, `runtime/backends.py` | Sidecar local ONNX/OpenVINO loading; manifest verified before loading |
| `embed/clip_embedder.py` | Runtime ORT embedding/cache local; optional sidecar Torch receives verified local weights/tokenizer; dev/export default pretrained tags remain unapproved |
| `detectors/onnx_det.py` | Evaluation detector URL downloads (`urlretrieve`), not the sidecar runtime adapter |
| `detectors/hf_det.py`, `detectors/zoo.py` | Evaluation HF `from_pretrained`/`hf_hub_download` and Ultralytics `.pt` auto-downloads; not called by sidecar |
| `runtime/export.py`, export scripts | Research/export-time model acquisition; not executed by Docker build or sidecar; outputs need reviewed, hash-pinned provenance before promotion |
| `algorithm/ghost_priority/providers.py` | Optional Tomer local NanoDet via OpenCV; model/policy verified by sidecar startup; algorithm package is not copied into the default Python image |
| `algorithm/README.md` | Research download command already uses an OpenCV-zoo commit URL; promotion still requires SHA-256 in the manifest |
| `research/` | Research HTTP inference clients/services, not a source of models in shipped images |

F-16 is remediated at the shipped runtime boundary, **not closed for the
repository's evaluation/export utilities**. Their existing unpinned download
APIs must not be used directly as an approved supply-chain promotion process;
Bandit findings remain visible. Model licensing is a separate approval.

## Base digests, numeric users and Iron Bank substitution

`deploy/Dockerfile` pins the Go builder, distroless runtime and demo-init
Alpine base to multi-platform index digests; `edgeml/Dockerfile` pins Python
the same way. Readable tags are comments, not mutable defaults. The workflow
uses these defaults instead of overriding them with tags. Locks and current
image checks target Linux amd64; index digests retain platform selection but
do not constitute verification of other architectures.

Go runtime images use `USER 65532:65532`; the sidecar uses `10001:10001`.
Zarf shore/operator/edge pod contexts use UID/GID/fsGroup 65532, and
edge-inference uses 10001 consistently. No UID manifest migration is needed.

Registry1 access must already be configured through the program's approved
credential mechanism (`docker login registry1.dso.mil` outside build scripts).
Choose approved repositories and immutable digests; these placeholders are
not asserted to be actual Iron Bank catalog paths:

```sh
export IB_GO='registry1.dso.mil/<approved-go-repository>@sha256:<reviewed-digest>'
export IB_PY='registry1.dso.mil/<approved-python-repository>@sha256:<reviewed-digest>'
export IB_RUNTIME='registry1.dso.mil/<approved-static-runtime>@sha256:<reviewed-digest>'
make images GO_IMAGE="$IB_GO" PY_IMAGE="$IB_PY" RUNTIME_IMAGE="$IB_RUNTIME"
make sbom
make scan
```

The ARGs are also directly overridable with `docker build --build-arg`.
The Go builder must provide the `go.mod` toolchain and shell; the static
runtime must support Linux static binaries, CA trust and UID 65532. Both
Python stages use `PY_IMAGE` and currently require Python 3.11 plus Debian
`apt-get`, `groupadd`/`useradd` and `libgomp1`. A UBI/RHEL-based Iron Bank
Python image is **not** a drop-in ARG override: adapt and review the package
installation recipe for that distribution, then rerun locks, runtime smoke,
SBOM, license and CVE gates. ARG substitution alone does not certify Iron Bank
compliance, package provenance or air-gap readiness.
