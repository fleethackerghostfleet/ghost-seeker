"""Summarise ship_classify_eval.py predictions: top-1 accuracy, macro recall and confusion matrices (markdown)."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.embed.shiptype import TYPES  # noqa: E402


def score(items, pred):
    y = [items[i]["gt"] for i in pred["idx"]]
    p = [r["type"] for r in pred["preds"]]
    acc = sum(a == b for a, b in zip(y, p)) / max(1, len(y))
    present = sorted({*y}, key=TYPES.index)
    rec = [sum(1 for a, b in zip(y, p) if a == b == c) / y.count(c) for c in present]
    return acc, sum(rec) / max(1, len(rec)), y, p


def confusion_md(y, p):
    rows = sorted({*y}, key=TYPES.index)
    cols = sorted({*y, *p}, key=TYPES.index)
    md = ["| gt \\ pred | " + " | ".join(cols) + " | n |", "|" + "---|" * (len(cols) + 2)]
    for r in rows:
        md.append(f"| **{r}** | " + " | ".join(str(sum(1 for a, b in zip(y, p) if a == r and b == c) or "·") for c in cols) + f" | {y.count(r)} |")
    return "\n".join(md)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(Path.home() / "data/shipcls"))
    ap.add_argument("--sets", nargs="+", required=True)
    ap.add_argument("--confusion", nargs="*", default=[], help="model names to print confusion matrices for")
    ap.add_argument("--subset-from", default=None, help="restrict every model to this model's item idx (e.g. gemini-2.5-flash)")
    a = ap.parse_args()
    out = Path(a.out)
    print("| model | " + " | ".join(f"{s} top-1 (macro-recall, n)" for s in a.sets) + " | ms/crop b1 | ms/crop b32 |")
    print("|" + "---|" * (len(a.sets) + 3))
    models = {f.name[5:-5].rsplit(f"_{s}", 1)[0] for f in out.glob("pred_*.json") for s in a.sets if f.name.endswith(f"_{s}.json")}
    mats = []
    for m in sorted(models, key=lambda m: (m.startswith("gemini"), m)):
        cells, lat = [], ("", "")
        for s in a.sets:
            f = out / f"pred_{m}_{s}.json"
            if not f.exists():
                cells.append("–")
                continue
            pr = json.load(open(f))
            ref = out / f"pred_{a.subset_from}_{s}.json" if a.subset_from else None
            if ref is not None and ref.exists():  # score every model on the (capped) items the reference model saw
                keep = set(json.load(open(ref))["idx"])
                pairs = [(i, r) for i, r in zip(pr["idx"], pr["preds"]) if i in keep]
                pr = {**pr, "idx": [i for i, _ in pairs], "preds": [r for _, r in pairs]}
            acc, mr, y, p = score(json.load(open(out / f"items_{s}.json")), pr)
            cells.append(f"{acc:.1%} ({mr:.1%}, {len(y)})")
            lat = (pr.get("latency_ms_b1", pr.get("latency_ms_seq", "")), pr.get("latency_ms_b32", ""))
            if m in a.confusion:
                mats.append(f"\n**{m} — {s}** (top-1 {acc:.1%}, n={len(y)})\n\n" + confusion_md(y, p))
        print(f"| {m} | " + " | ".join(cells) + f" | {lat[0]} | {lat[1]} |")
    print("\n".join(mats))


if __name__ == "__main__":
    main()
