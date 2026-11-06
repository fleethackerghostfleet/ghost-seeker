#!/bin/sh
set -eu
dest=$1
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
mkdir -p "$dest"
dest=$(CDPATH= cd -- "$dest" && pwd)
download() {
  tag=$1 name=$2 output=$3
  curl -fsSL --retry 2 "https://github.com/fleethackerghostfleet/ghost-seeker/releases/download/$tag/$name" -o "$output" || {
    rm -f "$output"
    gh release download "$tag" -R fleethackerghostfleet/ghost-seeker -p "$name" -O "$output"
  }
}
if ! (cd "$dest" && sha256sum -c "$here/RELEASE_SHA256SUMS"); then
  download demo-data-v1 models-D.tar.gz "$dest/models-D.tar.gz"
  (cd "$dest" && sha256sum -c "$here/RELEASE_SHA256SUMS")
fi
mkdir -p "$dest/files"
tar -xzf "$dest/models-D.tar.gz" -C "$dest/files" --strip-components=1
# Set FETCH_TEXT=0 to skip optional text downloads on image-only deployments.
if [ "${FETCH_TEXT:-1}" = 1 ]; then
  mkdir -p "$dest/files/mobileclip-s1"
  if ! (cd "$dest/files" && sha256sum -c "$here/TEXT_SHA256SUMS"); then
    download models-text-v1 text_model.onnx "$dest/files/mobileclip-s1/text_model.onnx"
    download models-text-v1 tokenizer.json "$dest/files/mobileclip-s1/tokenizer.json"
    (cd "$dest/files" && sha256sum -c "$here/TEXT_SHA256SUMS")
  fi
fi
