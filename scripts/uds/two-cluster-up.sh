#!/usr/bin/env bash
set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
LOG_DIR=${LOG_DIR:-"$ROOT/docs/reports/uds-validation/logs"}
BUNDLE=${BUNDLE:-ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-slim-dev:1.16.0}
SHORE_CLUSTER=${SHORE_CLUSTER:-uds-shore}
SHIP_CLUSTER=${SHIP_CLUSTER:-uds-ship}
mkdir -p "$LOG_DIR"

for tool in k3d uds kubectl docker python3; do
  command -v "$tool" >/dev/null
done
if (( $(</proc/sys/fs/inotify/max_user_instances) < 1024 )); then
  echo 'Two clusters need more inotify instances. Run: sudo sysctl -w fs.inotify.max_user_instances=1024 fs.inotify.max_user_watches=1048576' >&2
  exit 1
fi
[[ "$SHORE_CLUSTER" != "$SHIP_CLUSTER" ]]

deploy() {
  uds deploy "$BUNDLE" --confirm --no-progress --no-color --no-log-file "$@" \
    2>&1 | python3 -u -c '
import re,sys
for line in sys.stdin:
    if re.search(r"password|token|secret|credential|signing.key|private.key|[│┃]|^\s*\|", line, re.I):
        continue
    sys.stdout.write(line)
' | tee -a "$LOG_DIR/$name-deploy.log"
}

up() {
  local name=$1 api=$2 http=$3 https=$4
  local context="k3d-$name"
  if k3d cluster list -o json | python3 -c \
    'import json,sys; sys.exit(not any(c["name"] == sys.argv[1] for c in json.load(sys.stdin)))' "$name"; then
    k3d kubeconfig merge "$name" --kubeconfig-switch-context >/dev/null
    kubectl --context "$context" get namespace pepr-system istio-system
    if [[ "${RESUME_CORE:-0}" == 1 ]]; then
      deploy --packages core-base
      deploy --packages core-identity-authorization
    fi
    kubectl --context "$context" get secret -n zarf zarf-package-core-base zarf-package-core-identity-authorization >/dev/null
    echo "Reusing $name (no destructive bundle bootstrap)."
  else
    for port in "$api" "$http" "$https"; do
      case "$port" in 8105|8106|8109) echo "Protected port: $port" >&2; return 1;; esac
    done
    deploy \
      --set "uds-k3d-dev.CLUSTER_NAME=$name" \
      --set "uds-k3d-dev.K3D_API_PORT=$api" \
      --set "uds-k3d-dev.K3D_HTTP_PORT=$http" \
      --set "uds-k3d-dev.K3D_HTTPS_PORT=$https"
  fi
  kubectl --context "$context" wait --for=condition=Ready pods -A \
    --field-selector=status.phase!=Succeeded --timeout=180s
  kubectl --context "$context" get pods -A -o wide | tee "$LOG_DIR/$name-pods.txt"
  kubectl --context "$context" get services -A -o wide | tee "$LOG_DIR/$name-services.txt"
}

case "${TARGET:-both}" in shore|ship|both) ;; *) echo 'TARGET must be shore, ship or both' >&2; exit 1;; esac
if [[ "${TARGET:-both}" != ship ]]; then
  up "$SHORE_CLUSTER" "${SHORE_API_PORT:-6551}" "${SHORE_HTTP_PORT:-9080}" "${SHORE_HTTPS_PORT:-9443}"
fi
if [[ "${TARGET:-both}" != shore ]]; then
  up "$SHIP_CLUSTER" "${SHIP_API_PORT:-6552}" "${SHIP_HTTP_PORT:-10080}" "${SHIP_HTTPS_PORT:-10443}"
fi
k3d cluster list | tee "$LOG_DIR/clusters.txt"
docker network inspect "k3d-$SHORE_CLUSTER" "k3d-$SHIP_CLUSTER" \
  --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}} gateway={{.Gateway}}{{end}}' \
  | tee "$LOG_DIR/networks.txt"
