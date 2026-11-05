#!/bin/sh
# Runtime services remain on an isolated Docker network after provisioning.
set -eu
LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export NL_MODELS_DIR=${NL_MODELS_DIR:-"$LAB_DIR/models"}
compose() { docker compose -f "$LAB_DIR/compose.yml" "$@"; }
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo 'Docker with Compose is required. Native development instructions are in research/nl-priority/README.md.' >&2
  exit 1
fi
setup() {
  mkdir -p "$NL_MODELS_DIR"
  compose --profile setup run --rm granite-fetch
  if [ ! -f "$NL_MODELS_DIR/mobileclip/manifest.json" ]; then
    compose --profile setup run --build --rm clip-export
  fi
}
case "${1:-up}" in
  up) setup; compose up -d --build --wait app; echo "Priority Lab: http://localhost:${NL_PORT:-8097}" ;;
  offline) compose up -d --no-build --pull never --wait app; echo "Priority Lab: http://localhost:${NL_PORT:-8097}" ;;
  setup) setup ;;
  down) compose down ;;
  logs) compose logs -f app vision granite ;;
  *) echo 'Usage: run.sh [up|offline|setup|down|logs]' >&2; exit 2 ;;
esac
