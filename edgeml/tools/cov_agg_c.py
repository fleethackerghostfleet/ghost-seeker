"""Aggregate a `run_c.py matrix` results.jsonl into all / holdout-only / trained-clip sighting numbers per detector.

The holdout clips are the dev clips that `cov_build_detector_dataset.py` never trains on (same set as G_detector.md §4a).
Usage: python edgeml/tools/cov_agg_c.py ~/runs/cov/cdev_<name>/results.jsonl [--per-clip]
"""
import argparse
import collections
import json
from pathlib import Path

HOLDOUT = {"fvessel_clip08", "fvessel_clip10", "Submarine_surfacing", "Ship_in_the_Distance_Utopia_of_the_Seas"}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("results")
    ap.add_argument("--per-clip", action="store_true")
    a = ap.parse_args()
    agg = collections.defaultdict(collections.Counter)
    per = collections.defaultdict(dict)
    for line in Path(a.results).expanduser().open():
        r = json.loads(line)
        s, det = r["sighting"], r["cfg"].split("|")[0]
        found = round((s["object_recall"] or 0) * s["n_gt_objects"])
        per[r["seq"]][det] = (s["n_gt_objects"], found, s["false_sightings"])
        for k in ("all", "holdout" if r["seq"] in HOLDOUT else "trained_clips"):
            c = agg[(det, k)]
            c["n"] += s["n_gt_objects"]
            c["found"] += found
            c["false"] += s["false_sightings"]
            c["min"] += s["minutes"]
            c["reports"] += s["n_reports"]
    for (det, k), c in sorted(agg.items()):
        print(f"{det:16s} {k:14s} recall {c['found']}/{c['n']}={c['found'] / max(c['n'], 1):.2f} "
              f"false {c['false']} false/min {c['false'] / max(c['min'], 1e-9):.2f} reports {c['reports']}")
    if a.per_clip:
        for seq, d in sorted(per.items()):
            print(seq, {k: f"{v[1]}/{v[0]} found, {v[2]} false" for k, v in d.items()})


if __name__ == "__main__":
    main()
