"""HOTA (mean and at alpha=0.3) / IDF1 / MOTA / IDSW / Frag via TrackEval on MOT-layout sequences (CONTRACTS §3 layout, class-agnostic)."""
from __future__ import annotations

import re
import shutil
import tempfile
from pathlib import Path
from typing import Optional


def run_trackeval(seq_roots: list[Path], tracker_files: dict[str, Path], tracker_name: str = "edgeml", workdir: Optional[Path] = None,
                  frame_stride: int = 1) -> dict:
    """seq_roots: MOT sequence dirs; tracker_files: seq_id -> tracks.txt. Returns per-seq + COMBINED metrics.

    frame_stride > 1 evaluates on detector frames only (frames 1, 1+stride, ...): GT and tracker rows on other frames are
    dropped and frames renumbered, so a `detect_every=N` run is scored against the frames it actually processed."""

    def _keep(fr: int) -> bool:
        return (fr - 1) % frame_stride == 0

    def _renum(fr: int) -> int:
        return (fr - 1) // frame_stride + 1

    import numpy as np
    for _n, _t in (("float", float), ("int", int), ("bool", bool)):   # the PyPI TrackEval still uses np.float etc.
        if not hasattr(np, _n):
            setattr(np, _n, _t)
    import trackeval  # noqa: F401  (optional extra)

    wd = Path(workdir or tempfile.mkdtemp(prefix="trackeval_"))
    gt_root = wd / "gt" / "edgeml-all"
    tr_root = wd / "trackers" / "edgeml-all" / tracker_name / "data"
    gt_root.mkdir(parents=True, exist_ok=True)
    tr_root.mkdir(parents=True, exist_ok=True)
    names = []
    for s in seq_roots:
        name = s.name
        names.append(name)
        d = gt_root / name
        if d.exists():
            shutil.rmtree(d)
        d.mkdir(parents=True)
        info = (s / "seqinfo.ini").read_text()
        if frame_stride > 1:
            info = re.sub(r"(?m)^seqLength\s*=\s*(\d+)", lambda m: f"seqLength={_renum(int(m.group(1)))}", info)
        (d / "seqinfo.ini").write_text(info)
        (d / "gt").mkdir()
        # TrackEval expects class 1 = evaluated; conf column as evaluate flag. We make every GT row class 1.
        rows, seen = [], set()
        for line in (s / "gt" / "gt.txt").read_text().splitlines():
            if not line.strip():
                continue
            v = line.split(",")
            v = v[:9] + ["1"] * (9 - len(v))
            v[7] = "1"
            fr = int(float(v[0]))
            if not _keep(fr):
                continue
            v[0] = str(_renum(fr))
            key = (fr, int(float(v[1])))
            if key in seen:   # TrackEval rejects duplicate (frame, id) rows; keep the first
                continue
            seen.add(key)
            rows.append(",".join(v))
        (d / "gt" / "gt.txt").write_text("\n".join(rows) + "\n")
        src = tracker_files.get(name)
        trows = []
        if src and Path(src).exists():
            for line in Path(src).read_text().splitlines():
                if not line.strip():
                    continue
                v = line.split(",")
                fr = int(float(v[0]))
                if _keep(fr):
                    v[0] = str(_renum(fr))
                    trows.append(",".join(v))
        (tr_root / f"{name}.txt").write_text("\n".join(trows) + ("\n" if trows else ""))
    (gt_root.parent / "seqmaps").mkdir(exist_ok=True)
    (gt_root.parent / "seqmaps" / "edgeml-all.txt").write_text("name\n" + "\n".join(names) + "\n")

    eval_cfg = trackeval.Evaluator.get_default_eval_config()
    eval_cfg.update({"USE_PARALLEL": False, "PRINT_RESULTS": False, "PRINT_CONFIG": False, "TIME_PROGRESS": False,
                     "OUTPUT_SUMMARY": False, "OUTPUT_DETAILED": False, "PLOT_CURVES": False, "LOG_ON_ERROR": None, "DISPLAY_LESS_PROGRESS": True})
    ds_cfg = trackeval.datasets.MotChallenge2DBox.get_default_dataset_config()
    ds_cfg.update({"GT_FOLDER": str(gt_root), "TRACKERS_FOLDER": str(tr_root.parent.parent), "BENCHMARK": "edgeml", "SPLIT_TO_EVAL": "all",
                   "TRACKERS_TO_EVAL": [tracker_name], "SEQMAP_FILE": str(gt_root.parent / "seqmaps" / "edgeml-all.txt"),
                   "DO_PREPROC": False, "SKIP_SPLIT_FOL": True, "GT_LOC_FORMAT": "{gt_folder}/{seq}/gt/gt.txt", "PRINT_CONFIG": False})
    evaluator = trackeval.Evaluator(eval_cfg)
    dataset = trackeval.datasets.MotChallenge2DBox(ds_cfg)
    metrics = [trackeval.metrics.HOTA({"PRINT_CONFIG": False}), trackeval.metrics.CLEAR({"PRINT_CONFIG": False}), trackeval.metrics.Identity({"PRINT_CONFIG": False})]
    res, _ = evaluator.evaluate([dataset], metrics)
    r = res["MotChallenge2DBox"][tracker_name]
    out = {}
    for seq, cls in r.items():
        m = cls["pedestrian"]
        hota = m["HOTA"]["HOTA"]  # array over alpha = 0.05, 0.10, ..., 0.95
        out[seq] = {"HOTA": float(hota.mean()), "HOTA_a0.3": float(hota[5]), "IDF1": float(m["Identity"]["IDF1"]), "MOTA": float(m["CLEAR"]["MOTA"]),
                    "IDSW": int(m["CLEAR"]["IDSW"]), "Frag": int(m["CLEAR"]["Frag"]), "DetA": float(m["HOTA"]["DetA"].mean()), "AssA": float(m["HOTA"]["AssA"].mean()),
                    "MT": int(m["CLEAR"]["MT"]), "ML": int(m["CLEAR"]["ML"]), "FP": int(m["CLEAR"]["CLR_FP"]), "FN": int(m["CLEAR"]["CLR_FN"]), "GT_IDs": int(m["Count"]["GT_IDs"]) if "Count" in m else None}
    return out
