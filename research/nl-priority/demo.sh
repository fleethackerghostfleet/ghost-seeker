#!/bin/sh
# Opt-in integration with the existing demo stack; never merges/promotes code.
set -eu
LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$LAB_DIR/../.." && pwd)
export NL_MODELS_DIR=${NL_MODELS_DIR:-"$LAB_DIR/models"}
compose() { docker compose -f "$REPO_DIR/deploy/compose/demo.compose.yml" -f "$REPO_DIR/deploy/compose/demo.nl.compose.yml" "$@"; }
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo 'Docker with Compose is required; see research/nl-priority/DEMO_INTEGRATION.md for native development.' >&2
  exit 1
fi
case "${1:-up}" in
  up)
    mkdir -p "$NL_MODELS_DIR"
    compose --profile nl-setup run --rm granite-fetch
    compose up -d --build --wait
    echo 'Demo with local Granite: http://localhost:8090/#reprioritize'
    ;;
  offline) compose up -d --no-build --pull never --wait ;;
  down) compose down ;;
  logs) compose logs -f ui nl-compiler granite ;;
  config) compose config ;;
  *) echo 'Usage: demo.sh [up|offline|down|logs|config]' >&2; exit 2 ;;
esac
