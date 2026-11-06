#!/usr/bin/env python3
"""Build the split production release without changing source image pins."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

import yaml

ROOT = Path(__file__).resolve().parents[1]
VERSION_PATTERN = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?"
LIMIT = 2_000_000_000


def validate_version(version):
    if not re.fullmatch(VERSION_PATTERN, version):
        raise ValueError("VERSION must be x.y.z with an optional prerelease suffix (e.g. -rc1)")
    return version


def check_versions(root=ROOT):
    version = validate_version((root / "VERSION").read_text().strip())
    for role in ("ship", "shore"):
        package = yaml.safe_load((root / f"deploy/zarf/{role}/zarf.yaml").read_text())
        bundle = yaml.safe_load((root / f"bundles/prod-uds/{role}/uds-bundle.yaml").read_text())
        if (package["metadata"]["version"] != version or
                bundle["metadata"]["version"] != version or
                any(p["ref"] != version for p in bundle["packages"])):
            raise ValueError(f"{role}: version drift from VERSION ({version})")
    return version


def run(*args, cwd=ROOT, **kwargs):
    return subprocess.run(args, cwd=cwd, check=True, **kwargs)


def checksums(output):
    files = sorted(p for p in output.rglob("*") if p.is_file() and p.name != "SHA256SUMS")
    for path in files:
        if path.stat().st_size > LIMIT:
            with path.open("rb") as source:
                index = 0
                while chunk := source.read(64 * 1024 * 1024):
                    part = path.with_name(f"{path.name}.part{index // 16:04d}")
                    with part.open("ab") as dest:
                        dest.write(chunk)
                    index += 1
    files = sorted(p for p in output.rglob("*") if p.is_file() and p.name != "SHA256SUMS")
    with (output / "SHA256SUMS").open("w") as sums:
        for path in files:
            with path.open("rb") as source:
                digest = hashlib.file_digest(source, "sha256").hexdigest()
            sums.write(f"{digest}  {path.relative_to(output).as_posix()}\n")


def release(version, arch, allow_dirty):
    validate_version(version)
    check_versions()
    dirty = subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True)
    if dirty and not allow_dirty:
        raise ValueError("release requires a clean working tree; set ALLOW_DIRTY=1 for non-release testing")
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    print(f"Building release {version} for {arch} from git {sha} (dirty={bool(dirty)})", flush=True)
    output = ROOT / "dist/release" / version
    work = ROOT / "dist" / f"release-work-{version}"
    if output.exists() or work.exists():
        raise ValueError("release output/work directory already exists; use a fresh VERSION or archive previous run")
    output.mkdir(parents=True)
    work.mkdir(parents=True)
    paths = subprocess.check_output(["git", "ls-files", "-co", "--exclude-standard", "-z"], cwd=ROOT).split(b"\0")
    for raw in set(paths) - {b""}:
        relative = Path(os.fsdecode(raw))
        if (ROOT / relative).is_file():
            (work / relative).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, work / relative)
    (work / "VERSION").write_text(version + "\n")
    for role in ("ship", "shore"):
        for relative in (f"deploy/zarf/{role}/zarf.yaml", f"bundles/prod-uds/{role}/uds-bundle.yaml"):
            path = work / relative
            text = re.sub(r"(?m)^(  version:|    ref:) .*", lambda m: f"{m[1]} {version}", path.read_text())
            text = re.sub(r"(?m)^  architecture: .*", f"  architecture: {arch}", text)
            if "uds-bundle" in relative:
                text = text.replace("path: ../../../dist", f"path: {output}")
            path.write_text(text)
    check_versions(work)
    env = dict(os.environ, DOCKER_DEFAULT_PLATFORM=f"linux/{arch}", EDGEML_TRACKING="1")
    run("make", "pin-images", "EDGEML_TRACKING=1", cwd=work, env=env)
    images = json.loads((work / "deploy/zarf/images.lock.json").read_text())
    image_info = {}
    for name, ref in images.items():
        info = json.loads(subprocess.check_output(["docker", "image", "inspect", ref]))[0]
        if info["Architecture"] != arch:
            raise ValueError(f"{ref}: wrong architecture")
        image_info[name] = {"ref": ref, "config_digest": info["Id"], "repo_digests": info["RepoDigests"]}
    for role in ("ship", "shore"):
        run("zarf", "package", "create", f"deploy/zarf/{role}", "--confirm", "--architecture", arch, "--output", str(output), cwd=work)
        package = output / f"zarf-package-ghost-seeker-{role}-{arch}-{version}.tar.zst"
        run("zarf", "package", "inspect", "sbom", str(package), "--output", str(output / "sbom" / role), cwd=work)
        if not any((output / "sbom" / role).rglob("*.json")):
            raise ValueError(f"{role}: no SBOM JSON exported")
        run("uds", "create", f"bundles/prod-uds/{role}", "--confirm", "--architecture", arch, "--output", str(output), cwd=work)
    shutil.copy2(work / "deploy/zarf/images.lock.json", output / "images.lock.json")
    shutil.copy2(ROOT / "deploy/models/THIRD_PARTY_NOTICES.md", output)
    (output / "release.json").write_text(json.dumps({"version": version, "git_sha": sha, "dirty": bool(dirty), "arch": arch, "images": image_info}, indent=2) + "\n")
    checksums(output)
    shutil.rmtree(work)
    print(f"Release complete: {output} (git {sha})")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check-versions", action="store_true")
    parser.add_argument("--version", default=os.environ.get("VERSION", ""))
    parser.add_argument("--arch", choices=("amd64", "arm64"), default=os.environ.get("ARCH", "amd64"))
    args = parser.parse_args()
    try:
        if args.check_versions:
            print(f"Release versions consistent: {check_versions()}")
        else:
            release(args.version, args.arch, os.environ.get("ALLOW_DIRTY") == "1")
    except (ValueError, subprocess.CalledProcessError) as exc:
        parser.exit(1, f"release failed: {exc}\n")
