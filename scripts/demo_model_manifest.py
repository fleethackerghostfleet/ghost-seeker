"""Demo-only trust-on-first-use inventory; never use this to approve production models."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import tempfile


REQUIRED = ("yolo11n-640/model.onnx", "yolo11n-640/meta.json", "mobileclip-s1/model.onnx",
            "mobileclip-s1/meta.json", "mobileclip-s1/text_cache.json")


def write_manifest(directory):
    root = Path(directory).expanduser().resolve(strict=True)
    for name in REQUIRED:
        path = root / name
        if not path.is_file() or not path.stat().st_size:
            raise ValueError(f"missing or empty demo model file: {name}; run make demo-models")
    artifacts = []
    for name in ("yolo11n-640", "mobileclip-s1"):
        folder = root / name
        for path in [folder, *sorted(folder.rglob("*"))]:
            if path.is_symlink():
                raise ValueError(f"demo model symlink not allowed: {path}")
            if not path.is_file():
                continue
            digest = hashlib.sha256()
            with path.open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(chunk)
            value = digest.hexdigest()
            artifacts.append({"path": path.relative_to(root).as_posix(), "sha256": value,
                              "source": "demo-models:trust-on-first-use", "revision": f"sha256:{value}"})
    data = {"schema_version": 1, "artifacts": artifacts}
    manifest = root / "manifest.json"
    if manifest.exists():
        if manifest.is_symlink() or json.loads(manifest.read_text()) != data:
            raise ValueError("demo bundle changed since first use; review it before explicitly removing manifest.json and rerunning make demo-models")
        return manifest
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", dir=root, prefix=".demo-manifest-", delete=False) as stream:
            temporary = Path(stream.name)
            json.dump(data, stream, indent=2)
            stream.write("\n")
        temporary.chmod(0o644)
        os.replace(temporary, manifest)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory")
    args = parser.parse_args()
    print("WARNING: DEMO ONLY: trusting local model bytes on first use; not production provenance", flush=True)
    print(f"demo model manifest: {write_manifest(args.directory)}")


if __name__ == "__main__":
    main()
