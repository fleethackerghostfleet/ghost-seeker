# Verification without GitHub Actions

Hosted CI is manual-only (`workflow_dispatch`) to avoid automatic Actions usage.
You do not need another organization, a GitHub plan upgrade, or a runner
registration to run these checks. Local compute/Devin usage still has its own cost.

## Prepare a Linux verification host

Use a trusted checkout on a host with Go 1.26.10+, Node 22+, Python 3.11,
Make, Git, a C compiler (Go race detector), and a running Docker Engine that your
user can access. Docker access is effectively root access; do not expose its
socket or register this host to run untrusted PRs automatically.

Install the same auxiliary versions as the manual CI workflow:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -e 'edgeml[test]'
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.18.1
export PATH="$HOME/.local/bin:$(go env GOPATH)/bin:$PATH"
mkdir -p "$HOME/.local/bin"
curl -fsSL https://github.com/bufbuild/buf/releases/download/v1.47.2/buf-Linux-x86_64 -o "$HOME/.local/bin/buf"
curl -fsSL https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-linux-amd64.tar.gz | tar -xz -C "$HOME/.local/bin" kubeconform
curl -fsSL https://github.com/zarf-dev/zarf/releases/download/v0.89.0/zarf_v0.89.0_Linux_amd64 -o "$HOME/.local/bin/zarf"
chmod +x "$HOME/.local/bin/buf" "$HOME/.local/bin/zarf"
```

Download commands above are for Linux amd64. Other architectures need the
corresponding official release artifacts. Downloads, Go modules, Python packages,
Docker bases and Kubernetes schemas require network access on the first run.
There are no cloud-account credentials or GitHub write tokens required by the verifier.

## Run against a clean commit

```sh
. .venv/bin/activate
make verify
# Or: make verify VERIFY_PYTHON=.venv/bin/python
```

The command runs verifier regression tests, Go vet/tests/race/build/module
verification, pinned govulncheck, web tests, Python tests, protobuf lint/regeneration
and generated-code freshness, strict Kubernetes/Zarf checks, and all four Docker
image builds. Existing make variables such as `GO_IMAGE` and `PY_IMAGE` can select
approved base-image mirrors. Review the image choices alongside the report.

It writes `dist/verify/<commit>-<unique-run>/report.json` and per-check logs.
The report names the full Git commit and UTC start/end times. A failed command,
missing tool, timed-out check, unavailable Kubernetes schema, source mutation,
or uncommitted generated file means **nonzero exit and a failed report**, not a
successful skip. All checks run even when an earlier command fails, so the report
shows the remaining failures too. Package validation uses `STRICT_CHECKS=1`;
ordinary `make package-check` retains its existing offline-friendly behavior.
Some Python tests intentionally skip optional heavyweight backends; their counts
remain visible in the Python log, just as in the hosted workflow.

Do not edit or switch branches while verification runs. A report is valid only
for its recorded clean commit and tested environment, not later commits. Reports
are local evidence, not a signed attestation or an automatic GitHub status check.
Share the report and relevant logs with the reviewer before merging.

## Merge and hosted-CI policy

This change does not alter branch protection, required checks, permissions, or
manufacture passing GitHub statuses. If a required Actions check blocks a merge,
an owner must approve an equivalent external check and the protection update;
do not bypass it. Automatic verification is not provided by this local command:
the author/reviewer must explicitly run it for each proposed merge commit.

An owner can still manually run **CI** in GitHub's Actions tab, which uses hosted
allowance/paid usage. Once billing is restored, re-enable `pull_request` and
`push` to `main` in `.github/workflows/ci.yml` for automatic checks. An optional
long-term replacement is a dedicated isolated self-hosted runner; that still
uses the Actions service but not GitHub-hosted runner compute.
