"""Local MOT-layout sequence loader + converters used by run_c.py (A's canonical loader is edgeml_research.data.mot, PR #16)."""

import configparser
import json
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, Optional


@dataclass
class MotSequence:
    seq_id: str
    root: Path
    fps: float
    n_frames: int
    width: int
    height: int
    gt: list[tuple[int, int, list[float], int, int, float]] = field(default_factory=list)  # frame,id,box,conf,cls,vis
    labels: dict = field(default_factory=dict)
    meta: dict = field(default_factory=dict)

    def frame_path(self, frame_idx: int) -> Path:
        return self.root / "img1" / f"{frame_idx:06d}.jpg"

    def frames(self, every: int = 1, max_frames: Optional[int] = None):
        n = 0
        for i in range(1, self.n_frames + 1, every):
            yield i, (i - 1) / self.fps, self.frame_path(i)
            n += 1
            if max_frames and n >= max_frames:
                break

    @property
    def duration_s(self) -> float:
        return self.n_frames / self.fps

    def gt_tracks(self) -> dict[int, list[tuple[int, list[float]]]]:
        """id -> [(frame, box)] for evaluated (conf=1) GT rows."""
        out: dict[int, list[tuple[int, list[float]]]] = defaultdict(list)
        for fr, oid, box, conf, _cls, _vis in self.gt:
            if conf:
                out[oid].append((fr, box))
        return out


def gt_by_frame(seq: MotSequence) -> dict[int, list[tuple[int, list[float], int]]]:
    out: dict[int, list[tuple[int, list[float], int]]] = defaultdict(list)
    for fr, oid, box, conf, cls, _vis in seq.gt:
        if conf:
            out[fr].append((oid, box, cls))
    return out


def load_sequence(root: str | Path) -> MotSequence:
    root = Path(root)
    cp = configparser.ConfigParser()
    cp.read(root / "seqinfo.ini")
    s = cp["Sequence"]
    gt = []
    gtp = root / "gt" / "gt.txt"
    if gtp.exists():
        for line in gtp.read_text().splitlines():
            if not line.strip():
                continue
            v = line.split(",")
            fr, oid = int(float(v[0])), int(float(v[1]))
            box = [float(v[2]), float(v[3]), float(v[4]), float(v[5])]
            conf = int(float(v[6])) if len(v) > 6 else 1
            cls = int(float(v[7])) if len(v) > 7 else 1
            vis = float(v[8]) if len(v) > 8 else 1.0
            gt.append((fr, oid, box, conf, cls, vis))
    labels = {}
    lp = root / "gt" / "labels.json"
    if lp.exists():
        labels = json.loads(lp.read_text())
    meta = {k: s[k] for k in s if k not in ("imdir", "imext")}
    return MotSequence(seq_id=s.get("name", root.name), root=root, fps=float(s.get("frameRate", 1)), n_frames=int(s["seqLength"]),
                       width=int(s["imWidth"]), height=int(s["imHeight"]), gt=gt, labels=labels, meta=meta)


def list_sequences(set_root: str | Path, split: Optional[str] = None) -> list[MotSequence]:
    set_root = Path(set_root)
    idx = set_root / "index.jsonl"
    seqs = []
    if idx.exists():
        for line in idx.read_text().splitlines():
            if not line.strip():
                continue
            r = json.loads(line)
            if split and r.get("split") != split:
                continue
            if (set_root / r["seq_id"] / "seqinfo.ini").exists():
                s = load_sequence(set_root / r["seq_id"])
                s.meta.update({k: v for k, v in r.items() if k not in s.meta})
                seqs.append(s)
    else:
        for d in sorted(set_root.iterdir()):
            if (d / "seqinfo.ini").exists():
                seqs.append(load_sequence(d))
    return seqs


def write_sequence(root: str | Path, name: str, fps: float, width: int, height: int,
                   gt_rows: Iterable[tuple[int, int, list[float], int, int, float]], labels: dict,
                   n_frames: int, extra_ini: Optional[dict] = None) -> None:
    root = Path(root)
    (root / "gt").mkdir(parents=True, exist_ok=True)
    (root / "img1").mkdir(parents=True, exist_ok=True)
    ini = ["[Sequence]", f"name={name}", f"imDir=img1", f"frameRate={fps:g}", f"seqLength={n_frames}", f"imWidth={width}",
           f"imHeight={height}", "imExt=.jpg"]
    for k, v in (extra_ini or {}).items():
        ini.append(f"{k}={v}")
    (root / "seqinfo.ini").write_text("\n".join(ini) + "\n")
    with open(root / "gt" / "gt.txt", "w") as f:
        for fr, oid, box, conf, cls, vis in gt_rows:
            f.write(f"{fr},{oid},{box[0]:.2f},{box[1]:.2f},{box[2]:.2f},{box[3]:.2f},{conf},{cls},{vis:.2f}\n")
    (root / "gt" / "labels.json").write_text(json.dumps(labels, indent=1))


def append_index(set_root: str | Path, row: dict) -> None:
    set_root = Path(set_root)
    p = set_root / "index.jsonl"
    rows = []
    if p.exists():
        rows = [json.loads(l) for l in p.read_text().splitlines() if l.strip()]
    rows = [r for r in rows if r.get("seq_id") != row["seq_id"]] + [row]
    p.write_text("".join(json.dumps(r) + "\n" for r in rows))
