#!/usr/bin/env bash
set -euo pipefail
mkdir -p "${HOME}/.local/bin"
export PATH="${HOME}/.local/bin:${PATH}"
case "${1:?go|python|images|secrets|manifests}" in
  go)
    go install github.com/securego/gosec/v2/cmd/gosec@v2.22.11
    go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
    go install github.com/google/go-licenses/v2@v2.0.1
    ;;
  python)
    python3 -m venv .venv/security
    .venv/security/bin/pip install bandit==1.8.6 pip-audit==2.9.0 pip-licenses==5.5.5
    python3 -m venv .venv/shipped
    .venv/shipped/bin/pip install ./edgeml onnxruntime
    ;;
  secrets)
    curl -fsSL https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz | tar -xz -C "${HOME}/.local/bin" gitleaks
    ;;
  images)
    curl -fsSL https://github.com/aquasecurity/trivy/releases/download/v0.76.2/trivy_0.76.2_Linux-64bit.tar.gz | tar -xz -C "${HOME}/.local/bin" trivy
    curl -fsSL https://github.com/anchore/syft/releases/download/v1.57.0/syft_1.57.0_linux_amd64.tar.gz | tar -xz -C "${HOME}/.local/bin" syft
    ;;
  manifests)
    curl -fsSL https://github.com/aquasecurity/trivy/releases/download/v0.76.2/trivy_0.76.2_Linux-64bit.tar.gz | tar -xz -C "${HOME}/.local/bin" trivy
    curl -fsSL https://github.com/zarf-dev/zarf/releases/download/v0.89.0/zarf_v0.89.0_Linux_amd64 -o "${HOME}/.local/bin/zarf"
    chmod +x "${HOME}/.local/bin/zarf"
    ;;
  *) exit 2 ;;
esac
