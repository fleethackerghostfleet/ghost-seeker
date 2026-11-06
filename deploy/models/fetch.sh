#!/bin/sh
set -eu
dest=$1
checksums=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/RELEASE_SHA256SUMS
mkdir -p "$dest"
dest=$(CDPATH= cd -- "$dest" && pwd)
if ! (cd "$dest" && sha256sum -c "$checksums"); then
  rm -f "$dest/models-D.tar.gz"
  curl -fsSL --retry 2 https://github.com/fleethackerghostfleet/ghost-seeker/releases/download/demo-data-v1/models-D.tar.gz -o "$dest/models-D.tar.gz" || {
    rm -f "$dest/models-D.tar.gz"
    gh release download demo-data-v1 -R fleethackerghostfleet/ghost-seeker -p models-D.tar.gz -D "$dest" --clobber
  }
  (cd "$dest" && sha256sum -c "$checksums")
fi
mkdir -p "$dest/files"
tar -xzf "$dest/models-D.tar.gz" -C "$dest/files" --strip-components=1
