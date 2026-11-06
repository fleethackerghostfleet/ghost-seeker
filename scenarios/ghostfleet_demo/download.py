"""Sync camera datasets and generate the local live replay scenario."""

import fnmatch
import json
import os
from pathlib import Path
import shutil
import subprocess


HERE = Path(__file__).resolve().parent
TEAM_BUCKET = "gs://qwiklabs-gcp-02-34a870014566-ghost-seeker"
DEFAULT_CLIPS = "Submarine_surfacing Cruise_ship_Mediterranean_sunset_off_Italy Spotted_A_Submarine"


def selected(name, include, exclude):
    return (any(fnmatch.fnmatchcase(name, p) for p in include)
            and not any(fnmatch.fnmatchcase(name, p) for p in exclude))


def images(path):
    return path.is_dir() and any(
        p.is_file() and p.suffix.lower() in {".jpg", ".jpeg", ".png"}
        for p in path.iterdir()
    )


def sync(source, dest):
    dest.mkdir(parents=True, exist_ok=True)
    print(f"syncing {source} -> {dest}", flush=True)
    subprocess.run(["gcloud", "storage", "rsync", source, str(dest),
                    "--recursive", "--checksums-only", "--delete-unmatched-destination-objects"], check=True)


def gcloud_ready():
    return bool(shutil.which("gcloud")) and subprocess.run(
        ["gcloud", "auth", "print-access-token"], stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL).returncode == 0


def prepare(env):
    bucket = env.get("BUCKET", TEAM_BUCKET + "/edgeml/eval/ghost_fleet_5fps").rstrip("/")
    frames_bucket = env.get("FRAMES_BUCKET", "/".join(bucket.split("/")[:3])).rstrip("/")
    dest = Path(env.get("DEST", HERE / "frames")).resolve()
    output = HERE / "scenario.generated.json"
    scenario = json.loads((HERE / "scenario.json").read_text())
    defaults = {s["source"]: s for s in scenario["sets"]}
    sets = []
    clips = env.get("CLIPS", DEFAULT_CLIPS).split()
    for clip in clips:
        if Path(clip).name != clip or clip in {".", ".."}:
            raise ValueError(f"invalid clip name: {clip}")
    cloud = gcloud_ready() if clips or env.get("INCLUDE_BUCKET_FRAMES", "1") != "0" else True
    if not cloud:
        if env.get("BUCKET") or env.get("FRAMES_BUCKET") or not set(clips) <= set(DEFAULT_CLIPS.split()):
            raise RuntimeError("gcloud authentication required for custom camera datasets")
        print("WARNING: gcloud unavailable or unauthenticated; using released eval clips only. "
              "Bucket frame discovery/refresh is unavailable.", flush=True)
        subprocess.run([str(HERE / "../../deploy/demo-data.sh"),
                        "demo-frames.tar.gz", str(dest), "1"], check=True)
    for clip in clips:
        path = dest / clip / "img1"
        if cloud:
            sync(bucket + "/" + clip, dest / clip)
        elif not images(path):
            raise RuntimeError(f"release missing eval clip: {clip}")
        if images(path):
            entry = dict(defaults.get(clip, {"source": clip}))
            entry["path"] = os.path.relpath(path, HERE)
            sets.append(entry)
    if cloud and env.get("INCLUDE_BUCKET_FRAMES", "1") != "0":
        prefix = frames_bucket + "/frames/"
        result = subprocess.run(["gcloud", "storage", "ls", prefix],
                                capture_output=True, text=True)
        if result.returncode and "One or more URLs matched no objects" not in result.stderr:
            raise subprocess.CalledProcessError(result.returncode, result.args,
                                                output=result.stdout, stderr=result.stderr)
        listing = result.stdout
        include = env.get("FRAME_SETS_INCLUDE", "*").split(",")
        exclude = env.get("FRAME_SETS_EXCLUDE", "").split(",")
        names = sorted({line.strip()[len(prefix):].rstrip("/")
                        for line in listing.splitlines()
                        if line.strip().startswith(prefix) and line.strip().endswith("_frames/")})
        for name in names:
            if "/" in name or not selected(name, include, exclude):
                continue
            path = dest / "bucket" / name
            sync(prefix + name, path)
            if images(path):
                sets.append({"path": os.path.relpath(path, HERE), "source": name})
    if env.get("INCLUDE_OCEAN_SCENES", "0") == "1":
        live = json.loads((HERE / "live.json").read_text())
        for entry in live["sets"]:
            if "scene_every_s" not in entry or entry["source"] in {s["source"] for s in sets}:
                continue
            entry = dict(entry)
            path = dest / entry["source"] / "img1"
            if not images(path):
                raise RuntimeError("ocean scenes missing; run make demo-ocean first")
            entry["path"] = os.path.relpath(path, HERE)
            sets.append(entry)
    if not sets:
        output.unlink(missing_ok=True)
        print("No image sets downloaded; using scenario.json fallback")
        return
    scenario["sets"] = sets
    pending = output.with_suffix(".json.tmp")
    pending.write_text(json.dumps(scenario, indent=2) + "\n")
    pending.replace(output)
    print(f"Generated {output} ({len(sets)} camera sets)")
    for entry in sets:
        print(f"  {entry['source']}: {entry['path']}")


if __name__ == "__main__":
    prepare(os.environ)
