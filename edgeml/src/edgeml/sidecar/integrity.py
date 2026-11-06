"""Verify operator-approved, immutable model bundles before initializing any loader."""
from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path


def verify_models(paths: list[str | Path], manifest: str | Path | None) -> None:
    if not paths:
        return
    if not manifest:
        raise ValueError("--model-manifest is required when loading model artifacts")
    manifest = Path(manifest).expanduser().resolve(strict=True)
    root = manifest.parent
    data = json.loads(manifest.read_text())
    if data.get("schema_version") != 1 or not isinstance(data.get("artifacts"), list) or not data["artifacts"]:
        raise ValueError("model manifest must contain schema_version=1 and nonempty artifacts")
    approved = {}
    for entry in data["artifacts"]:
        relative = Path(entry["path"])
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError(f"unsafe model artifact path: {relative}")
        path = root / relative
        if path.is_symlink() or not path.resolve(strict=True).is_relative_to(root) or not path.is_file():
            raise ValueError(f"model artifact must be a regular file within the bundle: {relative}")
        path = path.resolve()
        if path in approved:
            raise ValueError(f"duplicate model artifact: {relative}")
        if not re.fullmatch(r"[0-9a-f]{64}", entry["sha256"]):
            raise ValueError(f"invalid SHA-256 for {relative}")
        if not re.fullmatch(r"[0-9a-f]{40}|sha256:[0-9a-f]{64}", entry["revision"]):
            raise ValueError(f"immutable source revision required for {relative}")
        if not isinstance(entry.get("source"), str) or not entry["source"]:
            raise ValueError(f"source provenance required for {relative}")
        with path.open("rb") as stream:
            digest = hashlib.sha256()
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
            actual = digest.hexdigest()
        if actual != entry["sha256"]:
            raise ValueError(f"SHA-256 mismatch for {relative}")
        approved[path] = actual
    for path in approved:
        if path.suffix == ".onnx":
            import onnx
            from onnx.external_data_helper import ExternalDataInfo, _get_all_tensors, uses_external_data

            model = onnx.load(path, load_external_data=False)
            for tensor in _get_all_tensors(model):
                if uses_external_data(tensor):
                    external = (path.parent / ExternalDataInfo(tensor).location).resolve()
                    if external not in approved:
                        raise ValueError(f"unapproved ONNX external data: {external}")
    for value in paths:
        path = Path(value).expanduser()
        if path.is_symlink():
            raise ValueError(f"model artifact cannot be a symlink: {path}")
        path = path.resolve(strict=True)
        if not (path.is_file() or path.is_dir()):
            raise ValueError(f"model artifact is not a file or directory: {path}")
        if not path.is_relative_to(root):
            raise ValueError(f"model artifact outside manifest bundle: {path}")
        directory = path if path.is_dir() else path.parent
        for artifact in directory.rglob("*"):
            if artifact == manifest:
                continue
            if artifact.is_symlink():
                raise ValueError(f"model artifact cannot be a symlink: {artifact}")
            if artifact.is_file() and artifact.resolve() not in approved:
                raise ValueError(f"unapproved model artifact: {artifact.relative_to(root)}")
