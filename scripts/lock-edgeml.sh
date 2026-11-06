#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../edgeml"
UV=${UV:-uv}
if [ "$("$UV" --version)" != "uv 0.9.5" ]; then
  echo 'lock regeneration requires uv==0.9.5' >&2
  exit 1
fi
common=(--python-version 3.11 --python-platform x86_64-manylinux_2_28 --generate-hashes --only-binary :all: --custom-compile-command 'make lock-edgeml')
"$UV" pip compile pyproject.toml requirements.in "${common[@]}" -o requirements.lock
"$UV" pip compile requirements-build.in "${common[@]}" -o requirements-build.lock
"$UV" pip compile requirements-headless.in "${common[@]}" --no-deps -o requirements-headless.lock
"$UV" pip compile pyproject.toml requirements-tracking.in --extra tracking --python-version 3.11 --python-platform x86_64-manylinux_2_28 --generate-hashes --custom-compile-command 'make lock-edgeml' --torch-backend cpu --emit-index-url --index-strategy unsafe-best-match --upgrade -o requirements-tracking.lock
