#!/bin/sh
# One-shot init for the Docker demo (deploy/compose/demo.compose.yml): create
# the local demo credentials once in the shared secrets volume, owned by the
# distroless `nonroot` uid the shore / ui / edge containers run as.
#   operator.token  shared operator bearer (what `make demo-token` prints natively)
#   unit.key        HS256 key the shore verifies edge JWTs with (`make demo-key`)
#   <edge-id>.jwt   the edge's unit token, re-minted on every `up`
# Existing token/key are kept, so restarting keeps the browser signed in;
# `make compose-demo-down` removes the volume for a fresh start.
set -eu
umask 077
dir=${SECRETS_DIR:-/secrets}
id=${EDGE_ID:-edge-1}
ttl=${UNIT_TOKEN_TTL:-24h}
uid=${RUN_AS_UID:-65532}
rand() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
mkdir -p "$dir"
cd "$dir"
if [ ! -s operator.token ]; then
  rand > .operator.token.tmp && mv .operator.token.tmp operator.token
  echo "demo-init: operator token written"
fi
if [ ! -s unit.key ]; then
  rand > .unit.key.tmp && mv .unit.key.tmp unit.key
  echo "demo-init: unit signing key written"
fi
mint-unit-token -id "$id" -ttl "$ttl" -signing-key-file unit.key > ".$id.jwt.tmp" && mv ".$id.jwt.tmp" "$id.jwt"
echo "demo-init: unit token for $id minted ($ttl)"
chown "$uid:$uid" operator.token unit.key "$id.jwt"
chmod 0400 operator.token unit.key "$id.jwt"
chmod 0500 "$dir"
chown "$uid:$uid" "$dir"
