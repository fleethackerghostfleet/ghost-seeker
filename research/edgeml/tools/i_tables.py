#!/usr/bin/env python
"""Markdown tables for research/edgeml/reports/I_tracking.md from run_c.py run dirs, pooled per eval set.

  python research/edgeml/tools/i_tables.py ~/runs/sw rmd2 tent0.5_2 ...        # one block per run, rows = cfg x set
  python research/edgeml/tools/i_tables.py ~/runs base_c --cfg yolo11n|bytetrack|every1 --sets synth_dense_5fps ghost_fleet_5fps
"""
from __future__ import annotations

import argparse
import json
from collections import defaultdict
from pathlib import Path

from c_tables import B, f, pooled  # noqa: E402  (same directory)

HEAD = "| run / config | set | GT objs | recall | dup/obj | false/min | TTFR s | reports/obj | img@9.6k | img@64k | img@256k | img@2M |"
SEP = "|" + "---|" * 12
SET_LABEL = {"synth_dense_5fps": "synth dense (dev)", "ghost_fleet_5fps": "ghost_fleet_5fps", "fvessel_clip10": "fvessel", "all": "all"}


def rows_of(run_dir: Path) -> list[dict]:
    rows = [json.loads(l) for l in (run_dir / "results.jsonl").read_text().splitlines() if l.strip()]
    return list({(r["cfg"], r["seq"]): r for r in rows}.values())


def line(label: str, set_name: str, rows: list[dict]) -> str:
    p = pooled(rows)
    return "| " + " | ".join([label, SET_LABEL.get(set_name, set_name), str(p["n_gt"]), f(p["recall"]), f(p["dup"]), f(p["false_min"], 1),
                              f(p["ttfr"], 1), f(p["rpo"], 1)] + [f(p["img"][b]) for b in B]) + " |"


def table(root: Path, runs: list[str], cfgs: list[str] | None, sets: list[str], label_mode: str) -> str:
    out = [HEAD, SEP]
    for run in runs:
        by = defaultdict(lambda: defaultdict(list))
        for r in rows_of(root / run):
            if cfgs and r["cfg"] not in cfgs:
                continue
            by[r["cfg"]][r["set"]].append(r)
            by[r["cfg"]]["all"].append(r)
        for cfg, per_set in by.items():
            label = run if label_mode == "run" else f"{run}: {cfg}" if label_mode == "both" else cfg
            for s in sets:
                if s in per_set:
                    out.append(line(label, s, per_set[s]))
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("root")
    ap.add_argument("runs", nargs="+")
    ap.add_argument("--cfg", nargs="*", default=None)
    ap.add_argument("--sets", nargs="*", default=["synth_dense_5fps", "ghost_fleet_5fps", "fvessel_clip10", "all"])
    ap.add_argument("--label", default="both", choices=["run", "cfg", "both"])
    a = ap.parse_args()
    print(table(Path(a.root).expanduser(), a.runs, a.cfg, a.sets, a.label))


if __name__ == "__main__":
    main()
