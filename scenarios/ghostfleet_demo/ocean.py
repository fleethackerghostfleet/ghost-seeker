#!/usr/bin/env python3
"""Reproduce the screened ocean watch from the existing LFS archive."""

import hashlib
import io
import json
from pathlib import Path
import sys
import zipfile

from PIL import Image


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]


def checked(data, expected, name):
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError(f"{name}: SHA-256 mismatch")
    return data


def crops(manifest, archive):
    checked(archive.read_bytes(), manifest["source_sha256"], str(archive))
    with zipfile.ZipFile(archive) as source:
        result = []
        for frame in manifest["frames"]:
            data = checked(source.read(frame["source"]), frame["source_sha256"], frame["source"])
            with Image.open(io.BytesIO(data)) as image:
                x, y, w, h = manifest["crop_xywh"]
                if x < 0 or y < 0 or w <= 0 or h <= 0 or x+w > image.width or y+h > image.height:
                    raise ValueError("crop outside source image")
                output = io.BytesIO()
                image.crop((x, y, x+w, y+h)).save(output, format="JPEG", **manifest["jpeg"])
            result.append(checked(output.getvalue(), frame["output_sha256"], frame["source"] + " crop"))
    if not result:
        raise ValueError("empty ocean manifest")
    return result


def extract(dest):
    manifest = json.loads((HERE / "ocean-manifest.json").read_text())
    archive = ROOT / manifest["source_zip"]
    try:
        images = crops(manifest, archive)
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        raise SystemExit(f"Ocean watch: {error}. Run git lfs pull --include='{manifest['source_zip']}' and use Pillow {manifest['pillow']}.") from error
    img1 = Path(dest) / "Open_ocean_watch" / "img1"
    img1.mkdir(parents=True, exist_ok=True)
    names = set()
    for i in range(manifest["replay_frames"]):
        name = f"{1 + i * manifest['name_stride']:06d}.jpg"
        names.add(name)
        (img1 / name).write_bytes(images[i % len(images)])
    for old in img1.glob("*.jpg"):
        if old.name not in names:
            old.unlink()
    print(f"Ocean watch: {len(images)} screened crops → {len(names)} replay frames in {img1}")


if __name__ == "__main__":
    extract(sys.argv[1] if len(sys.argv) > 1 else HERE / "frames")
