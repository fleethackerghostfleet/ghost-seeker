#!/usr/bin/env bash
set -euo pipefail
export PATH="$HOME/sdk/go/bin:$HOME/.local/bin:$PATH"
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
STATE=${STATE:-"$HOME/.local/share/uds-two-cluster"}
LOG_DIR=${LOG_DIR:-"$ROOT/docs/reports/uds-validation/logs"}
TAG=${TAG:-"prod-$(git -C "$ROOT" rev-parse --short=12 HEAD)-$(date -u +%Y%m%d%H%M%S)-$(python3 -c 'import secrets; print(secrets.token_hex(4))')"}
SHIP_CONTEXT=${SHIP_CONTEXT:-k3d-uds-ship}
SHORE_CONTEXT=${SHORE_CONTEXT:-k3d-uds-shore}
SHIP_NAMESPACE=${SHIP_NAMESPACE:-ghost-seeker-ship}
SHORE_NAMESPACE=${SHORE_NAMESPACE:-ghost-seeker-shore}
LINK_IP=${LINK_IP:-172.19.0.1}
mkdir -p "$STATE/build" "$LOG_DIR"
export ROOT STATE TAG SHIP_CONTEXT SHORE_CONTEXT SHIP_NAMESPACE SHORE_NAMESPACE LINK_IP
STAGE="$STATE/build/$TAG"
export STAGE
if [[ "${SKIP_BUILD:-0}" != 1 ]]; then
  if [[ ! -s "$STATE/build/models/files/yolo11n-640/model.onnx" ]]; then
    sh "$ROOT/deploy/models/fetch.sh" "$STATE/build/models"
  fi
  make -C "$ROOT" images MODELS="$STATE/build/models/files" IMAGE_TAG="$TAG" \
    GO_IMAGE="${GO_IMAGE:-mirror.gcr.io/library/golang:1.26.10-alpine}" \
    PY_IMAGE="${PY_IMAGE:-mirror.gcr.io/library/python:3.11-slim}" \
    BUSYBOX_IMAGE="${BUSYBOX_IMAGE:-mirror.gcr.io/library/busybox:1.37.0}" \
    EDGEML_TRACKING=1 2>&1 | tee "$LOG_DIR/images-$TAG.log"
fi
printf '%s\n' "$TAG" > "$STATE/built-tag"
if [[ "${BUILD_ONLY:-0}" == 1 ]]; then exit 0; fi

if [[ "${CLEAN_APP_DATA:-0}" == 1 ]]; then
  for role in shore ship; do
    if [[ "$role" == shore ]]; then context=$SHORE_CONTEXT; namespace=$SHORE_NAMESPACE;
    else context=$SHIP_CONTEXT; namespace=$SHIP_NAMESPACE; fi
    kubectl config use-context "$context" >/dev/null
    zarf package remove "ghost-seeker-$role" --confirm --no-color \
      2>&1 | tee "$LOG_DIR/remove-$TAG-$role.log"
    kubectl --context "$context" delete namespace "$namespace" --wait=true --timeout=180s \
      2>&1 | tee "$LOG_DIR/clean-$TAG-$role.log"
  done
fi
python3 "$ROOT/scripts/uds/two-cluster-auth.py"
kubectl --context "$SHORE_CONTEXT" apply -f "$ROOT/scripts/uds/gateway-access-log.yaml"
python3 - <<'PY'
import json,os,re,shutil
from pathlib import Path
root, stage = Path(os.environ['ROOT']), Path(os.environ['STAGE'])
if not stage.exists():
    shutil.copytree(root/'deploy/zarf', stage/'deploy/zarf')
    shutil.copytree(root/'bundles/prod-uds', stage/'bundles/prod-uds')
    for path in (stage/'deploy/zarf').rglob('*.yaml'):
        text = re.sub(r'(ghost-seeker/(?:shore|edge|operator|edgeml-sidecar|edgeml-models)):([\w.-]+)',
                      lambda m: m[1]+':'+os.environ['TAG'], path.read_text())
        path.write_text(text)
    lock = stage/'deploy/zarf/images.lock.json'
    lock.write_text(json.dumps({name:f'ghost-seeker/{name}:'+os.environ['TAG']
                               for name in json.loads(lock.read_text())}, indent=2)+'\n')
    (stage/'dist').mkdir()
for name in ('edge', 'shore', 'operator', 'edge-inference'):
    manifest = (stage/'deploy/zarf/manifests'/f'{name}.yaml').read_text()
    if '###ZARF_REGISTRY###/' not in manifest or 'private-registry' not in manifest:
        raise ValueError('Stage must follow docs/UDS_DEPLOY.md registry/pull-secret contract: '+name)
PY
for role in shore ship; do
  package="$STAGE/dist/zarf-package-ghost-seeker-$role-amd64-0.1.0.tar.zst"
  bundle="$STAGE/dist/uds-bundle-ghost-seeker-$role-amd64-0.1.0.tar.zst"
  if [[ ! -f "$package" ]]; then
    zarf package create "$STAGE/deploy/zarf/$role" --confirm --output "$STAGE/dist" --no-color \
      2>&1 | tee "$LOG_DIR/package-$TAG-$role.log"
  fi
  if [[ ! -f "$bundle" ]]; then
    uds create "$STAGE/bundles/prod-uds/$role" --confirm --output "$STAGE/dist" \
      --no-color --no-progress --no-log-file 2>&1 | tee "$LOG_DIR/bundle-$TAG-$role.log"
  fi
  if [[ "$role" == shore ]]; then
    context=$SHORE_CONTEXT
    vars=(--set "ghost-seeker-shore.NAMESPACE=$SHORE_NAMESPACE")
  else
    context=$SHIP_CONTEXT
    vars=(--set "ghost-seeker-ship.NAMESPACE=$SHIP_NAMESPACE" \
      --set ghost-seeker-ship.FRAMES_SOURCE=pvc \
      --set ghost-seeker-ship.SHORE_URL=https://shore.uds.dev:443 \
      --set "ghost-seeker-ship.SHORE_EGRESS_CIDR=$LINK_IP/32" \
      --set ghost-seeker-ship.SHORE_EGRESS_PORT=443)
  fi
  kubectl config use-context "$context" >/dev/null
  UDS_CONFIG="$STAGE/bundles/prod-uds/$role/uds-config.yaml" \
    uds deploy "$bundle" --confirm --no-color --no-progress --no-log-file "${vars[@]}" \
    2>&1 | tee "$LOG_DIR/app-$TAG-$role-deploy.log"
  if [[ "$role" == ship ]]; then
    kubectl --context "$context" patch statefulset edge -n "$SHIP_NAMESPACE" --type merge \
      -p "{\"spec\":{\"template\":{\"spec\":{\"hostAliases\":[{\"ip\":\"$LINK_IP\",\"hostnames\":[\"shore.uds.dev\"]}]}}}}"
    kubectl --context "$context" -n "$SHIP_NAMESPACE" rollout status statefulset/edge --timeout=300s
    kubectl --context "$context" -n "$SHIP_NAMESPACE" rollout status deployment/edgeml-sidecar --timeout=600s
    namespace=$SHIP_NAMESPACE
  else
    kubectl --context "$context" -n "$SHORE_NAMESPACE" rollout status statefulset/shore --timeout=300s
    kubectl --context "$context" -n "$SHORE_NAMESPACE" rollout status deployment/operator --timeout=300s
    namespace=$SHORE_NAMESPACE
  fi
  kubectl --context "$context" -n "$namespace" wait --for=jsonpath='{.status.phase}'=Ready \
    "packages.uds.dev/ghost-seeker-$role" --timeout=180s
done
sha256sum "$STAGE"/dist/*.tar.zst > "$LOG_DIR/artifact-SHA256SUMS-$TAG.txt"
git -C "$ROOT" rev-parse HEAD > "$LOG_DIR/revision-$TAG.txt"
printf '%s\n' "$TAG" > "$STATE/current-tag"
