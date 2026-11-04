#!/usr/bin/env python
"""Markdown tables for edgeml/reports/C.md from run_c.py run dirs (pooled over GT objects; see C.md §3)."""
from __future__ import annotations

import json
import sys
from collections import defaultdict
from pathlib import Path

import numpy as np

RUNS = Path(sys.argv[1]).expanduser() if len(sys.argv) > 1 else Path("~/runs").expanduser()
B = ("9.6kbps", "64kbps", "256kbps", "2Mbps")


def f(v, p=2):
    if v is None or (isinstance(v, float) and np.isnan(v)):
        return "-"
    return f"{v:.{p}f}" if isinstance(v, float) else str(v)


def pooled(rows):
    ngt = sum(r["sighting"]["n_gt_objects"] for r in rows)
    matched = sum(len(set(r["sighting"]["matched_sightings"].values())) for r in rows)
    mins = sum(r["sighting"]["minutes"] for r in rows)
    dup = [r["sighting"]["duplicates_per_object"] for r in rows if r["sighting"]["duplicates_per_object"] is not None]
    ttfr = [r["sighting"]["ttfr_mean_s"] for r in rows if r["sighting"]["ttfr_mean_s"] is not None]
    rpo = [r["sighting"]["reports_per_object"] for r in rows if r["sighting"]["reports_per_object"] is not None]
    d = {b: (sum(r["delivery"]["budgets"][b]["objects_delivered_image"] for r in rows) / ngt if ngt else None) for b in B}
    bpo = {b: (sum(r["delivery"]["budgets"][b]["bytes_used"] for r in rows) / max(1, sum(r["delivery"]["budgets"][b]["objects_delivered_image"] for r in rows))) for b in B}
    return {"n_gt": ngt, "recall": matched / ngt if ngt else None, "dup": float(np.mean(dup)) if dup else None,
            "false_min": sum(r["sighting"]["false_sightings"] for r in rows) / mins if mins else None,
            "ttfr": float(np.mean(ttfr)) if ttfr else None, "rpo": float(np.mean(rpo)) if rpo else None,
            "n_rep": sum(r["sighting"]["n_reports"] for r in rows), "img": d, "bpo": bpo}


def load(run, status=None):
    rows = [json.loads(l) for l in (RUNS / run / "results.jsonl").read_text().splitlines() if l.strip()]
    if status:
        rows = [r for r in rows if r.get("gt_status", "complete") in status]
    by = defaultdict(list)
    for r in rows:
        by[r["cfg"]].append(r)
    te = {}
    tp = RUNS / run / "trackeval.json"
    if tp.exists():
        for k, v in json.loads(tp.read_text()).items():
            if isinstance(v, dict) and "COMBINED_SEQ" in v:
                te[k] = v["COMBINED_SEQ"]
    return by, te


HEAD = "| config | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | img@72KB | img@480KB | img@1.92MB | img@15MB | KB/obj@480KB |"
SEP = "|" + "---|" * 18


def line(label, rows, te):
    p = pooled(rows)
    t = te or {}
    return "| " + " | ".join([label, str(p["n_gt"]), f(p["recall"]), f(p["dup"]), f(p["false_min"], 1), f(p["ttfr"], 1), f(p["rpo"], 1), str(p["n_rep"]),
                              f(t.get("HOTA")), f(t.get("HOTA_a0.3")), f(t.get("IDF1")), f(t.get("IDSW")), f(t.get("Frag")),
                              f(p["img"]["9.6kbps"]), f(p["img"]["64kbps"]), f(p["img"]["256kbps"]), f(p["img"]["2Mbps"]), f(p["bpo"]["64kbps"] / 1024, 0)]) + " |"


def table(run, cfgs=None, labels=None, status=None, label_prefix=""):
    by, te = load(run, status)
    cfgs = cfgs or list(by)
    out = [HEAD, SEP]
    for i, c in enumerate(cfgs):
        if c in by:
            out.append(line(label_prefix + (labels[i] if labels else c), by[c], te.get(c)))
    return "\n".join(out)


def main():
    out = {}
    exists = lambda r: (RUNS / r / "results.jsonl").exists()
    # §4 main dev table: yolo11s, default aggregator, centroid vs iou
    if exists("dev_yolo_centroid"):
        cf = ["yolo11s|naive|every1", "yolo11s|naive|every2", "yolo11s|naive|every5", "yolo11s|imgdedup|every1", "yolo11s|imgdedup|every2", "yolo11s|imgdedup|every5"]
        cf += [f"yolo11s|{t}|every{e}" for t in ("bytetrack", "ocsort", "botsort", "botsort_cmc") for e in (1, 2, 5)]
        out["dev_yolo_centroid"] = table("dev_yolo_centroid", cf)
    if exists("dev_yolo_iou"):
        out["dev_yolo_iou"] = table("dev_yolo_iou", [f"yolo11s|{t}|every{e}" for t in ("bytetrack", "botsort_cmc") for e in (1, 2, 5)], label_prefix="iou: ")
    for run in ("dev_oracle_centroid", "dev_oracle_iou"):
        if exists(run):
            cf = ["oracle|naive|every1", "oracle|imgdedup|every1"] + [f"{d}|{t}|every{e}" for d in ("oracle", "oracle-d0.2-j0.05") for t in ("bytetrack", "ocsort", "botsort", "botsort_cmc") for e in (1, 2, 5)]
            out[run] = table(run, cf)
    # §5 tuning
    tune = ["base", "score0.35", "score0.5", "minhits1", "minhits3", "close3", "close10", "relink5", "relink20", "kf0.6", "kfoff", "upd1s", "upd10s", "lost1.5", "lost6", "mask", "combo"]
    rows = [HEAD, SEP]
    for v in tune:
        if exists(f"tune/{v}"):
            by, te = load(f"tune/{v}")
            for c in ("yolo11s|bytetrack|every1", "yolo11s|botsort_cmc|every1", "yolo11s|bytetrack|every2", "yolo11s|botsort_cmc|every2"):
                if c in by and (v == "combo" or c.endswith("every1")):
                    rows.append(line(f"{v}: {c.split('|')[1]}" + (" every2" if c.endswith("2") else ""), by[c], te.get(c)))
    out["tune"] = "\n".join(rows)
    # §6 ghost_fleet_5fps dev, complete vs partial
    for asso in ("centroid", "iou"):
        for st in ("complete", "partial"):
            if exists(f"gf5_{asso}"):
                cf = [f"{d}|{t}|every{e}" for d in ("yolo11s", "yolo11n", "oracle") for t in ("naive", "imgdedup", "bytetrack", "botsort_cmc") for e in (1, 5)]
                out[f"gf5_{asso}_{st}"] = table(f"gf5_{asso}", cf, status=(st,))
    # §7 E2
    for run in ("e2", "e2--stabilize"):
        if exists(run):
            by, te = load(run)
            rows = [HEAD, SEP]
            for c in by:
                rr = by[c]
                ms = np.mean([r.get("timing", {}).get("stabilizer", {}).get("ms_per_frame", 0) for r in rr]) if run.endswith("stabilize") else 0
                rows.append(line(f"{'stab' if run.endswith('stabilize') else 'raw'}: {c}", rr, te.get(c)))
            out[run] = "\n".join(rows)
    # §8 test
    if exists("test_final"):
        by, te = load("test_final")
        sets = defaultdict(list)
        for c, rr in by.items():
            for r in rr:
                sets[r["set"]].append(r)
        out["test_all"] = table("test_final", status=("complete", "partial"))
        for s in sorted(sets):
            rows = [HEAD, SEP]
            for c, rr in by.items():
                sub = [r for r in rr if r["set"] == s]
                if sub:
                    rows.append(line(f"{s}: {c}", sub, None))
            out[f"test_{s}"] = "\n".join(rows)
    (RUNS / "tables.json").write_text(json.dumps(out, indent=1))
    for k, v in out.items():
        print(f"\n### {k}\n{v}")


if __name__ == "__main__":
    main()
