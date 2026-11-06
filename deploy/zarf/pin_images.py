#!/usr/bin/env python3
"""Build a fresh production image set, then pin every Zarf image reference."""
import argparse
import datetime
import json
from pathlib import Path
import re
import secrets
import subprocess


ROOT = Path(__file__).resolve().parents[2]
IMAGES = ("shore", "edge", "operator", "edgeml-sidecar", "edgeml-models")
TAG_PATTERN = r"prod-[a-f0-9]{12}-[0-9]{14}-[a-f0-9]{8}"
IMAGE_PATTERN = re.compile(r"ghost-seeker/(?:" + "|".join(IMAGES) + r"):[a-zA-Z0-9_.-]+")
LOCK = ROOT / "deploy/zarf/images.lock.json"


def reference_files():
    return [ROOT / "deploy/zarf/zarf.yaml",
            *[ROOT / f"deploy/zarf/{role}/zarf.yaml" for role in ("ship", "shore")],
            *[ROOT / f"deploy/zarf/manifests/{name}.yaml"
              for name in ("shore", "edge", "operator", "edge-inference")]]


def check_lock():
    images = json.loads(LOCK.read_text())
    if set(images) != set(IMAGES):
        raise ValueError("image lock must contain exactly the five production images")
    for name, reference in images.items():
        if not re.fullmatch(r"ghost-seeker/" + name + ":" + TAG_PATTERN, reference):
            raise ValueError("images must use a unique production build tag")
    for path in reference_files():
        for reference in IMAGE_PATTERN.findall(path.read_text()):
            if reference not in images.values():
                raise ValueError(f"{path.relative_to(ROOT)} differs from image lock")
    return images


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="validate references without building")
    args = parser.parse_args()
    if args.check:
        check_lock()
        return
    sha = subprocess.check_output(["git", "rev-parse", "--short=12", "HEAD"], cwd=ROOT, text=True).strip()
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")
    tag = f"prod-{sha}-{stamp}-{secrets.token_hex(4)}"
    subprocess.run(["make", "images", f"IMAGE_TAG={tag}"], cwd=ROOT, check=True)
    images = {name: f"ghost-seeker/{name}:{tag}" for name in IMAGES}
    for reference in images.values():
        subprocess.run(["docker", "image", "inspect", reference], check=True, stdout=subprocess.DEVNULL)
    for path in reference_files():
        path.write_text(IMAGE_PATTERN.sub(lambda m: images[m[0].split("/")[1].split(":")[0]], path.read_text()))
    LOCK.write_text(json.dumps(images, indent=2, sort_keys=True) + "\n")
    check_lock()
    print(f"Pinned five production images to unique build {tag}")


if __name__ == "__main__":
    main()
