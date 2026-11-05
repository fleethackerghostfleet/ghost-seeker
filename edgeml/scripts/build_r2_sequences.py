#!/usr/bin/env python3
"""Build the authoritative annotation-round-2 MOT sequences."""

from __future__ import annotations

import argparse
import configparser
import json
import re
import shutil
import subprocess
from pathlib import Path

import cv2
import numpy as np

BUCKET = "gs://qwiklabs-gcp-02-34a870014566-ghost-seeker"
SET_NAME = "ghost_fleet_r2_1fps"
WINDOW_SIZE = 60
WINDOW_COUNT = 4
ANNOTATION_R2_PLAN = (
    Path(__file__).resolve().parents[1]
    / "configs"
    / "annotation_r2"
    / "PLAN.md"
)


def parse_plan(path: Path) -> tuple[list[tuple[str, int, bool]], dict[str, str]]:
    text = path.read_text(encoding="utf-8")
    section = text.split("Dev sources (GCS frames/<Name>_frames/sec_NNNN.jpg, 1 fps):", 1)[1].split(
        "\nTeacher:", 1
    )[0]
    long_marker = section.index("long (windows):")
    sources = []
    for match in re.finditer(r"([A-Za-z0-9_]+)\((\d+)\)", section):
        is_long = match.start() >= long_marker
        sources.append((match.group(1), int(match.group(2)), is_long))
    class_line = re.search(r"Classes:\s*(.*?)\s*\(ids\s+1\.\.(\d+)\)", text)
    if not class_line:
        raise ValueError("Could not parse the class list from PLAN.md")
    classes = [name.strip() for name in class_line.group(1).split()]
    if len(classes) != int(class_line.group(2)):
        raise ValueError("Class count does not match PLAN.md")
    return sources, {str(index): name for index, name in enumerate(classes, start=1)}


def gcloud(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["gcloud", *args], check=check, capture_output=True, text=True
    )


def source_files(name: str, expected_count: int) -> list[str]:
    result = gcloud("storage", "ls", f"{BUCKET}/frames/{name}_frames/*.jpg", check=False)
    if result.returncode:
        raise RuntimeError(f"Cannot list GCS frames for {name}: {result.stderr.strip()}")
    files = sorted(line.strip() for line in result.stdout.splitlines() if line.strip())
    if len(files) != expected_count:
        raise RuntimeError(
            f"Authoritative source mismatch for {name}: PLAN.md says {expected_count} "
            f"frames, GCS has {len(files)}"
        )
    return files


def get_license(name: str) -> str:
    result = gcloud("storage", "cat", f"{BUCKET}/sources/{name}.json", check=False)
    if result.returncode:
        message = result.stderr.lower()
        if any(token in message for token in ("matched no objects", "no urls matched", "404", "not found")):
            return "unknown"
        raise RuntimeError(f"Cannot read source metadata for {name}: {result.stderr.strip()}")
    metadata = json.loads(result.stdout)
    return str(metadata.get("license") or "unknown")


def mean_luma(path: Path) -> tuple[int, int, float]:
    image = cv2.imread(str(path), cv2.IMREAD_COLOR)
    if image is None:
        raise RuntimeError(f"Could not decode image: {path}")
    gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
    return image.shape[1], image.shape[0], float(np.mean(gray))


def find_windows(lumas: list[float]) -> tuple[list[int], dict[str, object]]:
    n = len(lumas)
    if n <= WINDOW_SIZE:
        return [0], {"dark_window_replacement": None, "window_offsets": [0]}
    offsets = [round(k * (n - WINDOW_SIZE) / (WINDOW_COUNT - 1)) for k in range(WINDOW_COUNT)]
    dark_counts = [
        sum(value < 60 for value in lumas[offset : offset + WINDOW_SIZE])
        for offset in offsets
    ]
    replacement = None
    if any(value < 60 for value in lumas) and max(dark_counts, default=0) < 20:
        darkest_start = min(
            range(n - WINDOW_SIZE + 1),
            key=lambda start: float(np.mean(lumas[start : start + WINDOW_SIZE])),
        )
        nearest = min(range(len(offsets)), key=lambda index: abs(offsets[index] - darkest_start))
        replacement = {
            "replaced_offset": offsets[nearest],
            "darkest_offset": darkest_start,
            "darkest_mean_luma": float(np.mean(lumas[darkest_start : darkest_start + WINDOW_SIZE])),
        }
        offsets[nearest] = darkest_start
    return offsets, {
        "dark_counts_before_replacement": dark_counts,
        "dark_window_replacement": replacement,
        "window_offsets": offsets,
    }


def read_source_lumas(name: str, files: list[str], cache: Path) -> tuple[list[Path], list[float]]:
    source_dir = cache / f"{name}_frames"
    source_dir.mkdir(parents=True, exist_ok=True)
    missing = [uri for uri in files if not (source_dir / uri.rsplit("/", 1)[-1]).exists()]
    if missing:
        gcloud(
            "storage",
            "rsync",
            "-r",
            f"{BUCKET}/frames/{name}_frames",
            str(source_dir),
        )
    paths = [source_dir / uri.rsplit("/", 1)[-1] for uri in files]
    lumas = []
    for path in paths:
        if not path.exists():
            raise FileNotFoundError(f"Missing downloaded source frame: {path}")
        lumas.append(mean_luma(path)[2])
    return paths, lumas


def write_sequence(
    root: Path,
    name: str,
    license_name: str,
    files: list[str],
    source_paths: list[Path],
    lumas: list[float],
    offset: int,
    length: int,
) -> dict[str, object]:
    selected_files = files[offset : offset + length]
    selected_paths = source_paths[offset : offset + length]
    selected_lumas = lumas[offset : offset + length]
    first_sec = int(re.search(r"sec_(\d+)\.jpg$", selected_files[0]).group(1))
    seq_id = name if length == len(files) else f"{name}_w{first_sec}"
    seq_dir = root / seq_id
    img_dir = seq_dir / "img1"
    img_dir.mkdir(parents=True, exist_ok=True)
    widths, heights = [], []
    frame_map = {}
    luma_rows = []
    for mot_frame, (uri, source_path, luma) in enumerate(
        zip(selected_files, selected_paths, selected_lumas), start=1
    ):
        width, height, _ = mean_luma(source_path)
        widths.append(width)
        heights.append(height)
        shutil.copyfile(source_path, img_dir / f"{mot_frame:06d}.jpg")
        source_filename = uri.rsplit("/", 1)[-1]
        frame_map[str(mot_frame)] = {"source_frame": source_filename, "gs_uri": uri}
        luma_rows.append(
            json.dumps(
                {"frame": mot_frame, "source_frame": source_filename, "mean_luma": luma},
                separators=(",", ":"),
            )
        )
    if len(set(widths)) != 1 or len(set(heights)) != 1:
        raise ValueError(f"Inconsistent native image dimensions in {name}")
    seq_dir.mkdir(parents=True, exist_ok=True)
    cp = configparser.ConfigParser()
    cp.optionxform = str
    cp["Sequence"] = {
        "name": seq_id,
        "imDir": "img1",
        "frameRate": "1",
        "seqLength": str(length),
        "imWidth": str(widths[0]),
        "imHeight": str(heights[0]),
        "imExt": ".jpg",
        "source_dataset": name,
        "license": license_name,
    }
    with (seq_dir / "seqinfo.ini").open("w", encoding="utf-8") as stream:
        cp.write(stream, space_around_delimiters=False)
    (seq_dir / "frame_map.json").write_text(json.dumps(frame_map, indent=2) + "\n")
    (seq_dir / "frame_luma.jsonl").write_text("\n".join(luma_rows) + "\n")
    return {
        "seq_id": seq_id,
        "source": name,
        "start_source_frame": frame_map["1"]["source_frame"],
        "frames": length,
        "width": widths[0],
        "height": heights[0],
        "mean_luma": float(np.mean(selected_lumas)),
        "min_luma": float(min(selected_lumas)),
        "dark_frames_luma_lt_60": sum(value < 60 for value in selected_lumas),
    }


def build(root: Path, plan: Path) -> dict[str, object]:
    sources, _ = parse_plan(plan)
    root.mkdir(parents=True, exist_ok=True)
    cache = root / "source_frames"
    validations = {name: source_files(name, expected) for name, expected, _ in sources}
    summaries = []
    window_details = {}
    for name, expected, is_long in sources:
        files = validations[name]
        source_paths, lumas = read_source_lumas(name, files, cache)
        license_name = get_license(name)
        if is_long:
            offsets, details = find_windows(lumas)
            details["source_frames"] = expected
            window_details[name] = details
            for offset in offsets:
                summaries.append(
                    write_sequence(
                        root, name, license_name, files, source_paths, lumas, offset, WINDOW_SIZE
                    )
                )
        else:
            summaries.append(
                write_sequence(
                    root, name, license_name, files, source_paths, lumas, 0, expected
                )
            )
    report = {"set_name": SET_NAME, "window_choices": window_details, "sequences": summaries}
    (root / "build_summary.json").write_text(json.dumps(report, indent=2) + "\n")
    for row in summaries:
        print(
            f"{row['seq_id']}: source={row['source']} start={row['start_source_frame']} "
            f"frames={row['frames']} size={row['width']}x{row['height']} "
            f"mean_luma={row['mean_luma']:.2f} min_luma={row['min_luma']:.2f} "
            f"luma_lt_60={row['dark_frames_luma_lt_60']}"
        )
    print(f"build summary: {root / 'build_summary.json'}")
    return report


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--plan", type=Path, default=ANNOTATION_R2_PLAN)
    parser.add_argument(
        "--root", type=Path, default=Path("~/eval/ghost_fleet_r2_1fps").expanduser()
    )
    args = parser.parse_args()
    build(args.root.expanduser(), args.plan.expanduser())


if __name__ == "__main__":
    main()
