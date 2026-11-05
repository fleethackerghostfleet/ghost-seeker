#!/usr/bin/env sh
# Fetch one demo-data archive (clips or models) from the ghost-seeker GitHub
# release and unpack it. Tries, in order: plain HTTPS (works once the repo is
# public), `gh release download` (private repo: `brew install gh && gh auth login`),
# then the lab GCS bucket via gcloud (original source; needs a lab account).
#
#   deploy/demo-data.sh <asset> <dest-dir> <strip-components> [gcloud-fallback-cmd...]
set -eu
asset=$1; dest=$2; strip=$3; shift 3
repo=${GS_DEMO_DATA_REPO:-fleethackerghostfleet/ghost-seeker}
tag=${GS_DEMO_DATA_TAG:-demo-data-v1}
url="https://github.com/$repo/releases/download/$tag/$asset"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
got=""
if curl -fsSL --retry 2 -o "$tmp/$asset" "$url" 2>/dev/null && [ -s "$tmp/$asset" ]; then
  got="https"
elif command -v gh >/dev/null 2>&1 && gh release download "$tag" -R "$repo" -p "$asset" -D "$tmp" 2>"$tmp/gh.err"; then
  got="gh"
elif [ $# -gt 0 ] && command -v gcloud >/dev/null 2>&1; then
  echo "demo-data: release asset $asset unavailable (no gh auth?); falling back to gcloud" >&2
  mkdir -p "$dest"; "$@"; exit 0
else
  echo "error: cannot fetch $asset from $url" >&2
  echo "  private repo: install GitHub CLI and sign in once -> brew install gh && gh auth login" >&2
  [ -s "$tmp/gh.err" ] && sed 's/^/  gh: /' "$tmp/gh.err" >&2
  exit 1
fi
if curl -fsSL -o "$tmp/SHA256SUMS" "https://github.com/$repo/releases/download/$tag/SHA256SUMS" 2>/dev/null \
   || { command -v gh >/dev/null 2>&1 && gh release download "$tag" -R "$repo" -p SHA256SUMS -D "$tmp" 2>/dev/null; }; then
  want=$(awk -v a="$asset" '$2==a{print $1}' "$tmp/SHA256SUMS")
  if command -v sha256sum >/dev/null 2>&1; then have=$(sha256sum "$tmp/$asset" | cut -d' ' -f1); else have=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1); fi
  [ -z "$want" ] || [ "$want" = "$have" ] || { echo "error: $asset checksum mismatch ($have != $want)" >&2; exit 1; }
fi
mkdir -p "$dest"
tar -xzf "$tmp/$asset" -C "$dest" --strip-components="$strip"
echo "demo-data: $asset -> $dest (via $got)"
