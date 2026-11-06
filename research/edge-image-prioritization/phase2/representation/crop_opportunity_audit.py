#!/usr/bin/env python3
"""Post-hoc geometry audit of encoded predictions, separate from inference."""
import argparse
from collections import defaultdict
import csv
import hashlib
import json
from pathlib import Path


def clip_box(box, width, height):
    x, y, w, h = box
    x0, y0 = max(0., x), max(0., y)
    x1, y1 = min(width, x+w), min(height, y+h)
    return [x0, y0, max(0., x1-x0), max(0., y1-y0)]


def visible_geometry(box, form):
    x, y, w, h = box
    rx, ry, rw, rh = form["roi"]
    iw = max(0., min(x+w, rx+rw) - max(x, rx))
    ih = max(0., min(y+h, ry+rh) - max(y, ry))
    containment = iw * ih / (w*h) if w*h else 0.
    shortside = min(iw*form["payload_width"]/rw, ih*form["payload_height"]/rh)
    return containment, shortside


def read_gt(path):
    result = defaultdict(list)
    for line in path.read_text().splitlines():
        values = list(map(float, line.split(",")))
        if values[6] <= 0 or values[4] <= 0 or values[5] <= 0:
            continue
        result[int(values[0])].append({"track": int(values[1]), "box": values[2:6], "class_id": int(values[7])})
    return result


def write_csv(path, rows):
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0]) if rows else ["no_rows"])
        writer.writeheader()
        writer.writerows(rows)


def run(records_path, data_root, out):
    out.mkdir(parents=True, exist_ok=False)
    raw = records_path.read_bytes()
    records = json.loads(raw)
    index = {r["seq_id"]: r for r in map(json.loads, (data_root/"index.jsonl").read_text().splitlines())}
    if any(index[r["source"]]["split"] != "dev" for r in records):
        raise ValueError("Exploratory audit accepts dev records only")
    gt = {sid: read_gt(data_root/sid/"gt/gt.txt") for sid in {r["source"] for r in records}}
    families = ["full_1280_q80", "full_1280_q50", "overview_640_q80", "overview_320_q50", "top2_predicted_crops"]
    rows = []
    for record in records:
        annotations = gt[record["source"]].get(record["frame_index"], [])
        for a in annotations:
            clipped = clip_box(a["box"], record["width"], record["height"])
            if clipped[2]*clipped[3] == 0:
                continue
            for family in families:
                forms = [f for f in record["forms"] if (f["kind"] == "crop" if family == "top2_predicted_crops" else f["form_id"] == family)]
                measures = [(f, *visible_geometry(clipped, f)) for f in forms]
                eligible = [(f, fraction, shortside) for f, fraction, shortside in measures if fraction >= .90]
                best = max(eligible, key=lambda v: v[2], default=None)
                rows.append({"id": record["id"], "sequence": record["source"],
                             "gt_status": index[record["source"]]["gt_status"],
                             "frame_index": record["frame_index"], "track": a["track"],
                             "class_id": a["class_id"], "family": family,
                             "max_containment": max((v[1] for v in measures), default=0),
                             "best_ge90_shortside_px": best[2] if best else 0,
                             "best_ge90_form": best[0]["form_id"] if best else "",
                             "ge90_8px": int(bool(best and best[2] >= 8)),
                             "ge90_16px": int(bool(best and best[2] >= 16)),
                             "ge90_32px": int(bool(best and best[2] >= 32))})
    summary = []
    for status in ["all", *sorted({r["gt_status"] for r in rows})]:
        for family in families:
            subset = [r for r in rows if r["family"] == family and (status == "all" or r["gt_status"] == status)]
            row = {"gt_status": status, "family": family, "frame_box_observations": len(subset),
                   "sequences": len({r["sequence"] for r in subset})}
            for threshold in [8, 16, 32]:
                row[f"ge90_{threshold}px_count"] = sum(r[f"ge90_{threshold}px"] for r in subset)
                row[f"ge90_{threshold}px_fraction"] = row[f"ge90_{threshold}px_count"] / len(subset) if subset else None
            summary.append(row)
    per_sequence = []
    for sid in sorted({r["sequence"] for r in rows}):
        for family in families:
            subset = [r for r in rows if r["sequence"] == sid and r["family"] == family]
            per_sequence.append({"sequence": sid, "gt_status": subset[0]["gt_status"], "family": family,
                                 "frame_box_observations": len(subset),
                                 **{f"ge90_{threshold}px_count": sum(r[f"ge90_{threshold}px"] for r in subset) for threshold in [8,16,32]}})
    write_csv(out/"frame-box-opportunities.csv", rows)
    write_csv(out/"summary.csv", summary)
    write_csv(out/"per-sequence.csv", per_sequence)
    metadata = {"encoded_records_sha256": hashlib.sha256(raw).hexdigest(), "split": "dev",
                "annotation_sha256": {sid: hashlib.sha256((data_root/sid/"gt/gt.txt").read_bytes()).hexdigest() for sid in sorted(gt)},
                "index_sha256": hashlib.sha256((data_root/"index.jsonl").read_bytes()).hexdigest(),
                "denominator": "correlated annotated frame-box observations, not independent objects",
                "definition": ">=90% of image-clipped annotation area inside one encoded form; visible intersection short side >= threshold in output pixels",
                "eligibility": "positive-confidence GT rows only; no annotation read by encoder",
                "limitations": ["Teacher+human and incomplete/partial labels remain imperfect", "Geometric coverage is not recognition", "Predicted crops are opportunities before scheduling or bandwidth", "Two crop forms can be redundant", "All-status figures are diagnostic"]}
    (out/"audit.json").write_text(json.dumps({"metadata":metadata,"summary":summary},indent=2)+"\n")
    print(json.dumps(summary,indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--records",type=Path,required=True)
    p.add_argument("--data-root",type=Path,required=True)
    p.add_argument("--out",type=Path,required=True)
    a = p.parse_args()
    run(a.records,a.data_root,a.out)
