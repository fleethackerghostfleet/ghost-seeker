"""MOTChallenge-layout evaluation sequences (CONTRACTS §3).

    <root>/<set>/<seq_id>/{seqinfo.ini, img1/000001.jpg.., gt/gt.txt, gt/labels.json}
    <root>/<set>/index.jsonl

`root` may be a local directory or a `gs://bucket/prefix` URI. GCS sets are mirrored into a
local cache (default `~/.cache/edgeml/eval`) with `gcloud storage rsync` on first use, so the
rest of the code only ever touches local files.
"""
from __future__ import annotations

import configparser
import json
import re
import os
import subprocess
from collections.abc import Iterator
from dataclasses import dataclass, field
from pathlib import Path

import cv2
import numpy as np

DEFAULT_ROOT = "gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval"
DEFAULT_CACHE = Path(os.environ.get("EDGEML_EVAL_CACHE", "~/.cache/edgeml/eval")).expanduser()


@dataclass
class GTBox:
    frame: int  # 1-based
    track_id: int
    box: list[float]  # pixel [x, y, w, h]
    conf: float = 1.0
    class_id: int = 1
    visibility: float = 1.0


@dataclass
class SeqInfo:
    name: str
    frame_rate: float
    seq_length: int
    im_width: int
    im_height: int
    im_ext: str = ".jpg"
    im_dir: str = "img1"
    source: str = ""
    license: str = ""
    extra: dict = field(default_factory=dict)


class MOTSequence:
    def __init__(self, seq_dir: Path):
        self.dir = Path(seq_dir)
        self.seq_id = self.dir.name
        self.info = self._read_seqinfo(self.dir / "seqinfo.ini")
        self._gt: list[GTBox] | None = None
        self._labels: dict | None = None

    @staticmethod
    def _read_seqinfo(path: Path) -> SeqInfo:
        cp = configparser.ConfigParser()
        cp.read(path)
        s = cp["Sequence"]
        known = {"name", "framerate", "seqlength", "imwidth", "imheight", "imext", "imdir"}
        return SeqInfo(
            name=s.get("name", path.parent.name),
            frame_rate=float(s.get("frameRate")),
            seq_length=int(s.get("seqLength")),
            im_width=int(s.get("imWidth")),
            im_height=int(s.get("imHeight")),
            im_ext=s.get("imExt", ".jpg"),
            im_dir=s.get("imDir", "img1"),
            source=s.get("source", ""),
            license=s.get("license", ""),
            extra={k: v for k, v in s.items() if k.lower() not in known | {"source", "license"}},
        )

    def __len__(self) -> int:
        return self.info.seq_length

    def frame_path(self, frame: int) -> Path:
        return self.dir / self.info.im_dir / f"{frame:06d}{self.info.im_ext}"

    def frame_ids(self) -> range:
        return range(1, self.info.seq_length + 1)

    def read_frame(self, frame: int) -> np.ndarray:
        img = cv2.imread(str(self.frame_path(frame)), cv2.IMREAD_COLOR)
        if img is None:
            raise FileNotFoundError(self.frame_path(frame))
        return img

    def iter_frames(self, start: int = 1, stop: int | None = None, step: int = 1) -> Iterator[tuple[int, float, np.ndarray]]:
        """Yield (frame_idx, t_s, image_bgr)."""
        stop = self.info.seq_length if stop is None else min(stop, self.info.seq_length)
        for f in range(start, stop + 1, step):
            yield f, (f - 1) / self.info.frame_rate, self.read_frame(f)

    @property
    def labels(self) -> dict:
        if self._labels is None:
            p = self.dir / "gt" / "labels.json"
            self._labels = json.loads(p.read_text()) if p.exists() else {}
        return self._labels

    @property
    def label_source(self) -> str:
        return self.labels.get("label_source", "unknown")

    def class_name(self, class_id: int) -> str:
        return self.labels.get("classes", {}).get(str(class_id), str(class_id))

    @property
    def gt(self) -> list[GTBox]:
        if self._gt is None:
            self._gt = []
            p = self.dir / "gt" / "gt.txt"
            if p.exists():
                for line in p.read_text().splitlines():
                    if not line.strip():
                        continue
                    v = line.split(",")
                    self._gt.append(GTBox(int(v[0]), int(v[1]), [float(x) for x in v[2:6]],
                                          float(v[6]) if len(v) > 6 else 1.0,
                                          int(float(v[7])) if len(v) > 7 else 1,
                                          float(v[8]) if len(v) > 8 else 1.0))
        return self._gt

    def gt_by_frame(self) -> dict[int, list[GTBox]]:
        out: dict[int, list[GTBox]] = {}
        for g in self.gt:
            out.setdefault(g.frame, []).append(g)
        return out

    def gt_by_track(self) -> dict[int, list[GTBox]]:
        out: dict[int, list[GTBox]] = {}
        for g in self.gt:
            out.setdefault(g.track_id, []).append(g)
        for v in out.values():
            v.sort(key=lambda g: g.frame)
        return out

    def stats(self) -> dict:
        tracks = self.gt_by_track()
        areas = [g.box[2] * g.box[3] for g in self.gt]
        return {
            "seq_id": self.seq_id, "frames": self.info.seq_length, "fps": self.info.frame_rate,
            "tracks": len(tracks), "boxes": len(self.gt),
            "frames_with_gt": len(self.gt_by_frame()),
            "track_len_median": float(np.median([len(v) for v in tracks.values()])) if tracks else 0.0,
            "box_area_px_median": float(np.median(areas)) if areas else 0.0,
            "box_area_px_p10": float(np.percentile(areas, 10)) if areas else 0.0,
            "box_area_px_p90": float(np.percentile(areas, 90)) if areas else 0.0,
            "label_source": self.label_source,
        }


def _is_gcs(p: str) -> bool:
    return str(p).startswith("gs://")


def sync_set(root: str, set_name: str, cache: Path = DEFAULT_CACHE, refresh: bool = False) -> Path:
    """Return a local directory for `<root>/<set_name>`, rsyncing from GCS when needed."""
    if not _is_gcs(root):
        return Path(root).expanduser() / set_name
    root_slug = re.sub(r"[^A-Za-z0-9_.-]+", "_", root.rstrip("/").removeprefix("gs://"))
    local = cache / root_slug / set_name
    if local.exists() and (local / "index.jsonl").exists() and not refresh:
        return local
    local.mkdir(parents=True, exist_ok=True)
    subprocess.run(["gcloud", "storage", "rsync", "-r", f"{root.rstrip('/')}/{set_name}", str(local)],
                   check=True, stdout=subprocess.DEVNULL)
    return local


def read_index(set_dir: Path) -> list[dict]:
    p = Path(set_dir) / "index.jsonl"
    return [json.loads(line) for line in p.read_text().splitlines() if line.strip()]


def load_set(set_name: str, root: str = DEFAULT_ROOT, cache: Path = DEFAULT_CACHE,
             split: str | None = None, splits_yaml: Path | None = None) -> list[MOTSequence]:
    """All sequences of a set (optionally filtered to a split via configs/splits.yaml)."""
    set_dir = sync_set(root, set_name, cache)
    index = read_index(set_dir)
    seq_ids = [e["seq_id"] for e in index]
    if split is not None:
        allowed = set(split_seq_ids(split, set_name, splits_yaml))
        seq_ids = [s for s in seq_ids if s in allowed]
    return [MOTSequence(set_dir / s) for s in seq_ids]


def split_seq_ids(split: str, set_name: str, splits_yaml: Path | None = None) -> list[str]:
    import yaml  # lazy; only needed when filtering by split

    p = splits_yaml or Path(__file__).resolve().parents[3] / "configs" / "splits.yaml"
    cfg = yaml.safe_load(Path(p).read_text())
    v = cfg.get(split, {}).get(set_name, [])
    return list(v) if isinstance(v, list) else []


def validate_sequence(seq: MOTSequence) -> list[str]:
    """Contract checks; returns a list of problems (empty = OK)."""
    errs = []
    n = seq.info.seq_length
    missing = [f for f in seq.frame_ids() if not seq.frame_path(f).exists()]
    if missing:
        errs.append(f"{len(missing)} missing frames (first {missing[:3]})")
    extra = len(list((seq.dir / seq.info.im_dir).glob(f"*{seq.info.im_ext}"))) - n
    if extra > 0:
        errs.append(f"{extra} extra image files beyond seqLength")
    if not seq.gt:
        errs.append("gt.txt empty or missing")
    for g in seq.gt:
        if not (1 <= g.frame <= n):
            errs.append(f"gt frame {g.frame} outside 1..{n}")
            break
    for g in seq.gt:
        x, y, w, h = g.box
        if w <= 0 or h <= 0 or x < -w or y < -h or x > seq.info.im_width or y > seq.info.im_height:
            errs.append(f"bad box {g.box} frame {g.frame} id {g.track_id}")
            break
    if seq.label_source not in {"human", "public_gt", "teacher+human", "teacher"}:
        errs.append(f"label_source={seq.label_source!r} not in contract")
    return errs
