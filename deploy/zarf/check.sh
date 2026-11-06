#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
cd "$REPO_ROOT"

skip() {
  echo "skipped: $*"
  if [ "${STRICT_CHECKS:-0}" = "1" ]; then
    exit 1
  fi
}

PYTHON=${PYTHON:-python3}
if ! command -v "$PYTHON" >/dev/null 2>&1; then
  skip "$PYTHON not installed"
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
"$PYTHON" -m unittest discover -s "$SCRIPT_DIR" -p 'test_check_manifests.py'
"$PYTHON" "$SCRIPT_DIR/check_manifests.py" --output-dir "$tmp/manifests"

if command -v kubeconform >/dev/null 2>&1; then
  if kubeconform -strict -summary "$tmp"/manifests/*/*.yaml >"$tmp/kubeconform.log" 2>&1; then
    cat "$tmp/kubeconform.log"
  else
    if grep -Eiq 'Invalid:[[:space:]]*[1-9][0-9]*([,[:space:]]|$)' "$tmp/kubeconform.log" ||
      grep -Fq ' is invalid:' "$tmp/kubeconform.log"; then
      cat "$tmp/kubeconform.log"
      exit 1
    elif grep -Eiq 'Invalid:[[:space:]]*0([,[:space:]]|$)' "$tmp/kubeconform.log" &&
      grep -Eiq 'failed to fetch|failed to download|no such host|connection refused|TLS handshake timeout|i/o timeout|network is unreachable|temporary failure in name resolution' "$tmp/kubeconform.log"; then
      cat "$tmp/kubeconform.log"
      skip "kubeconform schemas could not be fetched (offline)"
    else
      cat "$tmp/kubeconform.log"
      exit 1
    fi
  fi
else
  skip "kubeconform not installed"
fi

if command -v zarf >/dev/null 2>&1; then
  zarf dev lint deploy/zarf
  if [ "${PACKAGE_CREATE:-0}" = "1" ]; then
    mkdir -p dist
    zarf package create deploy/zarf --confirm --output dist
  fi
else
  skip "zarf not installed"
fi
