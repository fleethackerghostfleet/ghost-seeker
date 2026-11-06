#!/usr/bin/env python3
"""Prepare an allowlisted, verified model-image context; text assets are optional."""
import hashlib
import re
import shutil
import subprocess
import sys
from pathlib import Path


def prepare(models, sums, output):
    root = Path(models).resolve(strict=True)
    manifest = Path(sums).read_text()
    text_names = ["mobileclip-s1/text_model.onnx", "mobileclip-s1/tokenizer.json"]
    present = [(root / name).exists() for name in text_names]
    if any(present) and not all(present):
        raise ValueError("text assets must include both text_model.onnx and tokenizer.json")
    if all(present):
        # Always pin optional files too; never silently copy unverified weights.
        manifest += "\n" + Path(__file__).with_name("TEXT_SHA256SUMS").read_text()
    paths, normalized, seen = [], [], {}
    for line in manifest.splitlines():
        if not line:
            continue
        match = re.fullmatch(r"([0-9a-fA-F]{64}) [ *](.+)", line)
        if not match:
            raise ValueError("invalid SHA256SUMS entry")
        digest, name = match.groups()
        path = Path(name)
        if path.is_absolute() or ".." in path.parts or name == "SHA256SUMS" or not path.parts or "\\" in name:
            raise ValueError(f"unsafe model path: {name}")
        if name in seen:
            if seen[name] != digest.lower():
                raise ValueError(f"conflicting model checksum: {name}")
            continue
        seen[name] = digest.lower()
        source_root = Path(__file__).resolve().parents[2] / "internal" if path.parts[0] == "catalog" else root
        source = (source_root / path).resolve(strict=True)
        if not source.is_relative_to(source_root) or not source.is_file():
            raise ValueError(f"model must be a file within MODELS: {name}")
        if hashlib.sha256(source.read_bytes()).hexdigest() != digest.lower():
            raise ValueError(f"model checksum mismatch: {name}")
        paths.append((path, source))
        normalized.append(f"{digest.lower()}  {path.as_posix()}\n")
    if not paths:
        raise ValueError("MODEL_SUMS must list at least one model file")
    required_catalog = {Path("catalog/colregs_lights.json"), Path("catalog/colregs_night_config.json")}
    missing_catalog = required_catalog - {path for path, source in paths}
    if missing_catalog:
        raise ValueError("MODEL_SUMS missing required catalog entries: " + ", ".join(
            str(path) for path in sorted(missing_catalog)))
    out = Path(output)
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        raise ValueError("output context must be empty")
    for path, source in paths:
        destination = out / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        destination.chmod(0o644)
    manifest = "".join(normalized).encode()
    (out / "SHA256SUMS").write_bytes(manifest)
    subprocess.run(["sha256sum", "-c", "SHA256SUMS"], cwd=out, check=True)
    print("MODEL_SHA256=" + hashlib.sha256(manifest).hexdigest())


if __name__ == "__main__":
    try:
        if len(sys.argv) != 4:
            raise ValueError("usage: prepare.py MODELS MODEL_SUMS OUTDIR")
        prepare(*sys.argv[1:])
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        sys.exit(f"error: {exc}")
