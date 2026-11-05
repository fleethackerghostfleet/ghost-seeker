#!/usr/bin/env python3
"""Download the pinned, official IBM Granite artifact; verify before activation."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import urllib.request

REPOSITORY = "ibm-granite/granite-4.0-350m-GGUF"
REVISION = "b8208a86a58427e1739265318028eb5895b74bf2"
FILENAME = "granite-4.0-350m-Q4_K_M.gguf"
SHA256 = "771c588a49607f274a2bba3185733607ebe6f74b996ab90e2d6bee0d98bcec52"
SIZE = 236985760
LLAMA_CPP_REVISION = "45b6604b20f17b7d7638042306c0cd1b6af5999c"


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as src:
        for chunk in iter(lambda: src.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def fetch(directory):
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / FILENAME
    if target.exists() and target.stat().st_size == SIZE and digest(target) == SHA256:
        return target
    temporary = target.with_suffix(".gguf.part")
    url = f"https://huggingface.co/{REPOSITORY}/resolve/{REVISION}/{FILENAME}"
    try:
        with urllib.request.urlopen(url, timeout=120) as src, temporary.open("wb") as dst:
            for chunk in iter(lambda: src.read(1024 * 1024), b""):
                dst.write(chunk)
        if temporary.stat().st_size != SIZE or digest(temporary) != SHA256:
            raise ValueError("IBM artifact size/SHA-256 mismatch; refusing to activate model")
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path, help="Local model directory (not committed to Git)")
    args = parser.parse_args()
    path = fetch(args.directory)
    print(json.dumps({"path": str(path), "repository": REPOSITORY, "revision": REVISION,
                      "sha256": SHA256, "bytes": SIZE, "license": "Apache-2.0",
                      "llamaCppRevision": LLAMA_CPP_REVISION}, indent=2))


if __name__ == "__main__":
    main()
