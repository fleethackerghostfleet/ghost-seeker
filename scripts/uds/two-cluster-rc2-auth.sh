#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077
: "${STATE:?}" "${SHORE_CONTEXT:?}" "${SHIP_CONTEXT:?}" "${SHORE_NS:?}" "${SHIP_NS:?}"
[[ ! -e "$STATE/auth" ]] || { echo 'Fresh rc2 auth directory required' >&2; exit 1; }
mkdir "$STATE/auth"
kubectl --context "$SHORE_CONTEXT" create namespace "$SHORE_NS"
kubectl --context "$SHIP_CONTEXT" create namespace "$SHIP_NS"
SHORE_OPERATOR_TOKEN=$(openssl rand -hex 32)
EDGE_OPERATOR_TOKEN=$(openssl rand -hex 32)
export SHORE_SIGNING_KEY=$(openssl rand -hex 32)
export EDGE_ID=${EDGE_ID:-ship-001}
export UNIT_TTL_SECONDS=86400
UNIT_TOKEN=$(python3 - <<'PY'
import base64, hashlib, hmac, json, os, time
def enc(value):
    return base64.urlsafe_b64encode(value).rstrip(b'=')
now = int(time.time())
header = enc(b'{"alg":"HS256","typ":"JWT"}')
claims = enc(json.dumps({"sub": os.environ["EDGE_ID"], "role": "unit",
                        "iat": now, "exp": now + int(os.environ["UNIT_TTL_SECONDS"])}).encode())
payload = header + b'.' + claims
print((payload + b'.' + enc(hmac.new(os.environ['SHORE_SIGNING_KEY'].encode(),
                                   payload, hashlib.sha256).digest())).decode())
PY
)
printf %s "$SHORE_OPERATOR_TOKEN" > "$STATE/auth/operator-token"
printf %s "$EDGE_OPERATOR_TOKEN" > "$STATE/auth/edge-operator-token"
printf %s "$SHORE_SIGNING_KEY" > "$STATE/auth/signing-key"
printf %s "$UNIT_TOKEN" > "$STATE/auth/unit-token"
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" create secret generic shore-auth \
  --from-file="signing-key=$STATE/auth/signing-key" --from-file="operator-token=$STATE/auth/operator-token"
kubectl --context "$SHORE_CONTEXT" -n "$SHORE_NS" create secret generic operator-auth \
  --from-file="operator-token=$STATE/auth/operator-token"
kubectl --context "$SHIP_CONTEXT" -n "$SHIP_NS" create secret generic edge-auth \
  --from-file="unit-token=$STATE/auth/unit-token" --from-file="operator-token=$STATE/auth/edge-operator-token"
unset SHORE_SIGNING_KEY UNIT_TOKEN SHORE_OPERATOR_TOKEN EDGE_OPERATOR_TOKEN
