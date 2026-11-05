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

if ! command -v python3 >/dev/null 2>&1; then
  skip "python3 not installed"
  exit 0
fi
python3 "$SCRIPT_DIR/check_manifests.py"

if command -v kubeconform >/dev/null 2>&1; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT HUP INT TERM
  for manifest in "$SCRIPT_DIR"/manifests/*.yaml; do
    sed \
      -e 's,###ZARF_VAR_SHORE_URL###,http://shore.ghost-seeker.svc.cluster.local:8080,g' \
      -e 's|###ZARF_VAR_SHORE_PUBLIC_HOSTS###|shore,shore.ghost-seeker,shore.ghost-seeker.svc,shore.ghost-seeker.svc.cluster.local|g' \
      -e 's|###ZARF_VAR_EDGE_PUBLIC_HOSTS###|edge,edge.ghost-seeker,edge.ghost-seeker.svc,edge.ghost-seeker.svc.cluster.local|g' \
      -e 's,###ZARF_VAR_EDGE_ID###,edge-1,g' \
      -e 's,###ZARF_VAR_SHORE_REQUIRE_AUTH###,false,g' \
      -e 's,###ZARF_VAR_EDGE_MAX_BYTES###,0,g' \
      -e 's,###ZARF_VAR_EDGE_MAX_ITEMS###,0,g' \
      -e 's,###ZARF_VAR_INFER_URL###,mock,g' \
      -e 's,###ZARF_VAR_SHORE_STORAGE###,5Gi,g' \
      -e 's,###ZARF_VAR_EDGE_STORAGE###,10Gi,g' \
      -e 's,###ZARF_VAR_FRAMES_HOSTPATH###,/var/lib/ghost-seeker/frames,g' \
      -e 's,###ZARF_VAR_FRAMES_DIR###,,g' \
      -e 's,###ZARF_VAR_SCENARIO###,,g' \
      "$manifest" > "$tmp/$(basename "$manifest")"
  done
  if kubeconform -strict -summary "$tmp" >"$tmp/kubeconform.log" 2>&1; then
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
