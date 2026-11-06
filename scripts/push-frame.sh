#!/bin/sh
# Push one complete image; the token travels on stdin, never in curl's argv.
set +x
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: $0 SOURCE IMAGE [META.json] (EDGE_URL, GS_OPERATOR_TOKEN_FILE or GS_OPERATOR_TOKEN)" >&2
  exit 2
fi
source=$1
frame=$2
case "$source" in ''|*[!A-Za-z0-9_.:/-]*) echo 'invalid source ID' >&2; exit 2;; esac
[ "${#source}" -le 128 ] || { echo 'source ID too long' >&2; exit 2; }
[ -f "$frame" ] || { echo 'image file not found' >&2; exit 2; }
case "$frame" in
  *.[jJ][pP][gG]|*.[jJ][pP][eE][gG]) type=image/jpeg;;
  *.[pP][nN][gG]) type=image/png;;
  *) echo 'image must be JPEG or PNG' >&2; exit 2;;
esac
if [ -n "${GS_OPERATOR_TOKEN_FILE:-}" ]; then
  token=$(cat "$GS_OPERATOR_TOKEN_FILE")
else
  token=${GS_OPERATOR_TOKEN:-}
fi
[ -n "$token" ] || { echo 'set GS_OPERATOR_TOKEN_FILE or GS_OPERATOR_TOKEN' >&2; exit 2; }
case "$token" in *"$(printf '\r')"*|*'
'*) echo 'invalid bearer credential' >&2; exit 2;; esac
url=${EDGE_URL:-http://127.0.0.1:8081}
source=$(printf '%s' "$source" | sed 's/:/%3A/g;s|/|%2F|g')
url="${url%/}/api/v1/ingest/frames?source=$source"
if [ "$#" -eq 3 ]; then
  [ -f "$3" ] || { echo 'metadata file not found' >&2; exit 2; }
  printf 'Authorization: Bearer %s\n' "$token" |
    curl --silent --show-error --fail-with-body --header @- \
      --form "frame=@\"$frame\";type=$type" --form "meta=<\"$3\";type=application/json" \
      "$url"
else
  printf 'Authorization: Bearer %s\n' "$token" |
    curl --silent --show-error --fail-with-body --header @- \
      --header "Content-Type: $type" \
      --data-binary "@$frame" "$url"
fi
printf '\n'
