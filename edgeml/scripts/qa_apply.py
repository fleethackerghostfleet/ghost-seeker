"""Apply human QA corrections to teacher tracks and write the final MOT gt + index.jsonl.

Per sequence, `qa/corrections.yaml` (written by the reviewer after looking at qa/track_*.jpg):
    reviewed: true                 # required for label_source=teacher+human
    drop: [3, 7]                   # false-positive tracks (sea, glare, own-ship structure ...)
    merge: {2: [5, 9]}             # id switches / fragments: fold 5 and 9 into 2
    relabel: {4: buoy}             # wrong class
    trim: {6: [1, 120]}            # keep only frames in [start, end]
    add_tracks:                    # missed objects: keyframe boxes [x, y, w, h], linearly interpolated
      - {id: 101, cls: ship, keyframes: {1: [10, 20, 30, 12], 50: [40, 22, 32, 12]}}
    notes: free text
The untouched teacher output is kept in teacher/gt_teacher.txt so the correction is reproducible.

usage: python qa_apply.py --root <set_dir> --meta <set_dir>/clips_meta.yaml
"""

import argparse
import configparser
import json
import shutil
from collections import defaultdict
from pathlib import Path

import yaml

ROOT_PKG = Path(__file__).resolve().parents[1] / "src"
import sys

sys.path.insert(0, str(ROOT_PKG))
from edgeml.data.mot import MOTSequence, validate_sequence

SPLITS_YAML = Path(__file__).resolve().parents[1] / "configs" / "splits.yaml"


def split_of(set_name, seq_id):
    cfg = yaml.safe_load(SPLITS_YAML.read_text())
    for split in ("test", "dev"):
        if seq_id in (cfg.get(split, {}).get(set_name) or []):
            return split
    return "unassigned"


def read_rows(p):
    rows = []
    for ln in p.read_text().splitlines():
        if ln.strip():
            f, i, x, y, w, h, c, k, v = ln.split(",")
            rows.append([int(f), int(i), float(x), float(y), float(w), float(h), int(c), int(k), float(v)])
    return rows


def interp_keyframes(kf, start, end):
    ks = sorted(kf)
    out = {}
    for f in range(start, end + 1):
        if f in kf:
            out[f] = list(kf[f])
            continue
        lo = max((k for k in ks if k < f), default=None)
        hi = min((k for k in ks if k > f), default=None)
        if lo is None or hi is None:
            continue
        t = (f - lo) / (hi - lo)
        out[f] = [a * (1 - t) + b * t for a, b in zip(kf[lo], kf[hi])]
    return out


def apply(seq_dir: Path, corr: dict, classes: dict) -> tuple[list, dict]:
    teacher = seq_dir / "teacher" / "gt_teacher.txt"
    gt = seq_dir / "gt" / "gt.txt"
    raw_teacher = "qa" not in json.loads((seq_dir / "gt" / "labels.json").read_text())  # qa_apply has not rewritten gt/ yet
    if not teacher.exists() or (raw_teacher and gt.stat().st_mtime > teacher.stat().st_mtime):
        shutil.copy(gt, teacher)  # gt/ is raw teacher output (fresh rerun): refresh the snapshot
    rows = read_rows(teacher)
    name_to_id = {v: int(k) for k, v in classes.items()}
    drop = set(corr.get("drop") or [])
    remap = {old: new for new, olds in (corr.get("merge") or {}).items() for old in olds}
    relabel = corr.get("relabel") or {}
    trim = corr.get("trim") or {}
    out = []
    for f, i, x, y, w, h, c, k, v in rows:
        if i in drop:
            continue
        if i in trim and not (trim[i][0] <= f <= trim[i][1]):
            continue  # trim is by teacher id, before merge renames it
        i = remap.get(i, i)
        if i in relabel:
            if relabel[i] not in name_to_id:
                name_to_id[relabel[i]] = max(name_to_id.values(), default=0) + 1
            k = name_to_id[relabel[i]]
        out.append([f, i, x, y, w, h, c, k, v])
    for t in corr.get("add_tracks") or []:
        if t["cls"] not in name_to_id:
            name_to_id[t["cls"]] = max(name_to_id.values(), default=0) + 1
        kf = {int(f): b for f, b in t["keyframes"].items()}
        boxes = interp_keyframes(kf, min(kf), max(kf))
        for f, (x, y, w, h) in boxes.items():
            out.append([f, int(t["id"]), x, y, w, h, 1, name_to_id[t["cls"]], 1.0 if f in kf else 0.5])
    # after merges a frame may hold two boxes with the same id: keep the larger box (full object extent), vis breaks ties
    best = {}
    for r in out:
        key = (r[0], r[1])
        if key not in best or (r[4] * r[5], r[8]) > (best[key][4] * best[key][5], best[key][8]):
            best[key] = r
    out = sorted(best.values(), key=lambda r: (r[0], r[1]))
    return out, {str(v): k for k, v in sorted(name_to_id.items(), key=lambda kv: kv[1])}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--meta", required=True, type=Path, help="yaml: {seq_id: {viewpoint, platform_motion, sea_state, ...}}")
    a = ap.parse_args()
    meta = yaml.safe_load(a.meta.read_text())
    entries = []
    for seq_dir in sorted(p for p in a.root.iterdir() if (p / "seqinfo.ini").exists()):
        labels = json.loads((seq_dir / "gt" / "labels.json").read_text())
        corr_p = seq_dir / "qa" / "corrections.yaml"
        corr = yaml.safe_load(corr_p.read_text()) if corr_p.exists() else {}
        rows, classes = apply(seq_dir, corr or {}, labels["classes"])
        (seq_dir / "gt" / "gt.txt").write_text("".join(
            f"{f},{i},{x:.1f},{y:.1f},{w:.1f},{h:.1f},{c},{k},{v:.2f}\n" for f, i, x, y, w, h, c, k, v in rows))
        reviewed = bool(corr.get("reviewed"))
        labels.update({"classes": classes, "label_source": "teacher+human" if reviewed else "teacher",
                       "qa": {"reviewed": reviewed, "drop": corr.get("drop") or [], "merge": corr.get("merge") or {},
                              "relabel": corr.get("relabel") or {}, "trim": corr.get("trim") or {},
                              "added_tracks": [t["id"] for t in corr.get("add_tracks") or []],
                              "same_object_groups": corr.get("same_object_groups") or [],
                              "gt_status": corr.get("gt_status", "complete"),
                              "notes": corr.get("notes", "")}})
        teacher_rows = read_rows(seq_dir / "teacher" / "gt_teacher.txt")
        (seq_dir / "gt" / "labels.json").write_text(json.dumps(labels, indent=1))
        cp = configparser.ConfigParser()
        cp.read(seq_dir / "seqinfo.ini")
        s = cp["Sequence"]
        m = meta.get(seq_dir.name, {})
        seq = MOTSequence(seq_dir)
        errs = validate_sequence(seq)
        if not rows and (reviewed or corr.get("gt_status") == "incomplete"):
            errs = [e for e in errs if "gt.txt empty" not in e]  # declared zero-GT sequence
        if errs:
            print(seq_dir.name, "INVALID", errs)
        st = seq.stats()
        by_id = defaultdict(int)
        for r in rows:
            by_id[r[1]] += 1
        e = {"seq_id": seq_dir.name, "set": a.root.name, "source_dataset": s.get("source_dataset", a.root.name),
             "split": split_of(a.root.name, seq_dir.name), "fps": float(s["frameRate"]),
             "n_frames": int(s["seqLength"]), "n_tracks": len(by_id), "n_boxes": len(rows),
             "width": int(s["imWidth"]), "height": int(s["imHeight"]),
             "viewpoint": m.get("viewpoint", "unknown"), "platform_motion": m.get("platform_motion", "unknown"),
             "sea_state": m.get("sea_state", "unknown"), "label_source": labels["label_source"],
             "classes": sorted({classes[str(r[7])] for r in rows}), "box_area_px_median": st.get("box_area_px_median"),
             "gt_status": corr.get("gt_status", "complete"),
             "n_tracks_teacher": len({r[1] for r in teacher_rows}), "n_boxes_teacher": len(teacher_rows),
             "qa_dropped_tracks": len(corr.get("drop") or []),
             "qa_merged_tracks": sum(len(v) for v in (corr.get("merge") or {}).values()),
             "qa_added_tracks": len(corr.get("add_tracks") or []),
             "license": s.get("license", ""), "upstream": s.get("source_url", m.get("source_url", "")),
             "notes": m.get("notes", "")}
        entries.append(e)
        print(f"{e['seq_id']}: {e['n_frames']} fr, {e['n_tracks']} tracks, {e['n_boxes']} boxes, {e['label_source']}, {e['split']}")
    (a.root / "index.jsonl").write_text("".join(json.dumps(e) + "\n" for e in entries))


if __name__ == "__main__":
    main()
