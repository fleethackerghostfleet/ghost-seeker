#!/usr/bin/env bash
# Production gate (`make prod-gate`). See AGENTS.md: it must pass before any
# PR to `prod`, and must never be skipped, weakened or bypassed. Fails if a
# `-tags prod` build still contains demo features.
set -euo pipefail
cd "$(dirname "$0")/.."
GO=${GO:-go}
NODE=${NODE:-node}
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
failures=0
step() { printf '\n== prod-gate: %s\n' "$*"; }
fail() { printf 'prod-gate: FAIL: %s\n' "$*" >&2; failures=$((failures + 1)); }

step "build all commands with -tags prod"
mkdir -p "$out/bin"
CGO_ENABLED=0 "$GO" build -tags prod -trimpath -o "$out/bin/" ./cmd/...
ls "$out/bin"

step "go vet -tags prod ./..."
"$GO" vet -tags prod ./...

step "go test -tags prod ./..."
"$GO" test -tags prod ./...

step "TestProdContract* (prod build)"
"$GO" test -tags prod -count=1 -v -run '^TestProdContract' ./... >"$out/contract.log" 2>&1 || { cat "$out/contract.log"; fail "TestProdContract* failed"; }
grep -E '^(--- (PASS|FAIL)|FAIL|ok ).*' "$out/contract.log" | grep -v 'no test files' || true
ran=$(grep -c '^--- PASS: TestProdContract' "$out/contract.log" || true)
[ "$ran" -gt 0 ] || fail "no TestProdContract* tests ran"
echo "$ran TestProdContract* tests passed"

step "prod binaries: no demo symbols"
# Symbols that only demo wiring links in (go tool nm needs unstripped binaries).
symbols='internal/server\.WithDemoReset|\(\*Server\)\.resetDemo|edge/inference\.NewMock|edge/inference\.\(\*Mock\)|internal/sensor\.NewGenerator|internal/sensor\.\(\*Generator\)|internal/dataset\.NewReplay|edge/ingest\.NewReplay|scenario\.Default|\(\*Sessions\)\.DemoAcceptAnyToken|\(\*Sessions\)\.TrustPeers|opauth\.loadOrCreate$|\(\*Bridge\)\.setLink|\(\*Bridge\)\.WithLinkOverride|main\.(runSimulation|runDatasetSimulation|fillBacklog|fillDatasetBacklog|runWatchOfficer|newSimulationUplink|simulationLinkRate)$'
for bin in "$out"/bin/*; do
  if ! syms=$("$GO" tool nm "$bin"); then fail "go tool nm failed on $(basename "$bin")"; continue; fi
  if hits=$(echo "$syms" | awk '{print $NF}' | grep -E "$symbols"); then
    fail "$(basename "$bin") links demo symbols: $(echo "$hits" | sort -u | tr '\n' ' ')"
  fi
done

step "prod binaries: no demo routes or embedded demo assets"
# Route patterns, not paths: the embedded console JS may name a gated route.
strings_re='POST /api/demo/reset|POST /api/shore/link|empty-ocean\.zip|imagery/library/|demo-accept-any-token|demo-link-control|sim-link-bps|dev-trust-peers|dev-token-file|DefaultDevFile'
for bin in "$out"/bin/*; do
  if hits=$(grep -aoE "$strings_re" "$bin" | sort -u); then
    fail "$(basename "$bin") contains demo strings: $(echo "$hits" | tr '\n' ' ')"
  fi
done

step "prod binaries: no demo flags in -h"
common='demo-reset|demo-link-control|demo-accept-any-token|dev-token-file|dev-trust-peers|sim-link-bps'
declare -A deployed=(
  [ghost-seeker]='backfill|capture-every|auto-cdr|dataset|dataset-dir|negatives-rate|splits|retain|policy-state|admin'
  [edge]='scenario|frames|period'
  [shore]=''
)
for bin in "$out"/bin/*; do
  name=$(basename "$bin")
  flags=$("$bin" -h 2>&1 | sed -nE 's/^  -([A-Za-z0-9-]+).*/\1/p' || true)
  [ -n "$flags" ] || { [ -z "${deployed[$name]+x}" ] && continue; fail "$name -h printed no flags"; continue; }
  forbidden="$common${deployed[$name]:+|${deployed[$name]}}"
  if hits=$(echo "$flags" | grep -xE "$forbidden"); then
    fail "$name exposes demo flags: $(echo "$hits" | tr '\n' ' ')"
  fi
  if [ -n "${deployed[$name]+x}" ] && "$bin" -h 2>&1 | grep -qi "or 'mock'"; then
    fail "$name -h still offers the mock analyzer"
  fi
done

step "ghost-seeker (prod) refuses to start without live mode"
if msg=$("$out/bin/ghost-seeker" -addr 127.0.0.1:0 2>&1); then
  fail "ghost-seeker started without -shore"
elif ! grep -q -- '-shore' <<<"$msg"; then
  fail "ghost-seeker exit message does not explain -shore: $msg"
else
  echo "ok: $msg"
fi

step "web: demo controls unreachable when features are false"
"$NODE" --test web/prod-features.test.cjs

step "deploy defaults build the prod profile"
grep -qE '^ARG GO_TAGS=prod$' deploy/Dockerfile || fail "deploy/Dockerfile must default to ARG GO_TAGS=prod"
grep -q -- '-tags "${GO_TAGS}"' deploy/Dockerfile || fail "deploy/Dockerfile go build must use -tags \"\${GO_TAGS}\""
if grep -nE 'SCENARIO|FRAMES_DIR|ZARF_VAR_INFER_URL###.*mock|default: mock|--mock' deploy/zarf/zarf.yaml deploy/zarf/manifests/*.yaml; then
  fail "deploy/zarf must not configure replay or the mock analyzer"
fi

step "output API contract"
if [ -f docs/api/openapi.yaml ]; then
  grep -q '/api/v1' docs/api/openapi.yaml || fail "docs/api/openapi.yaml does not document /api/v1"
else
  echo "WARNING: docs/api/openapi.yaml not present yet; the /api/v1 output API must be documented there before any PR to prod"
  [ "${PROD_GATE_TARGET:-}" != prod ] || fail "docs/api/openapi.yaml missing (required for PRs to prod)"
fi

if [ "$failures" -gt 0 ]; then
  printf '\nprod-gate: %d check(s) FAILED\n' "$failures" >&2
  exit 1
fi
printf '\nprod-gate: PASS\n'
