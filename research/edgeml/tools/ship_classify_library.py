"""Write ship classifications for the synthetic library into internal/catalog/imagery.json.

Reads a prediction file from ship_classify_eval.py run on the ``synthetic`` set (one record per vessel detection,
in imagery.json order) and stores it as ``dets[i].ship``. Non-vessel detections are left untouched.

  python tools/ship_classify_library.py --pred ~/data/shipcls/pred_gemini-2.5-flash_synthetic.json
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pred", required=True)
    ap.add_argument("--items", help="items_synthetic.json (default: next to --pred)")
    ap.add_argument("--catalog", default=str(REPO / "internal/catalog/imagery.json"))
    a = ap.parse_args()
    pred = json.load(open(a.pred))
    items = json.load(open(a.items or Path(a.pred).with_name("items_synthetic.json")))
    raw = open(a.catalog).read()
    lib = json.loads(raw)
    by_file = {im["file"]: im for im in lib}
    n = 0
    for i, rec in zip(pred["idx"], pred["preds"]):
        f, k = items[i]["group"].rsplit("#", 1)
        det = by_file[f]["dets"][int(k)]
        rec = {key: v for key, v in rec.items() if key != "probs"}
        det["ship"] = rec
        n += 1
    text = json.dumps(lib, indent=1, separators=(",", ": "), ensure_ascii=False)
    open(a.catalog, "w").write(text + ("\n" if raw.endswith("\n") else ""))
    print(f"wrote {n} ship records to {a.catalog}")


if __name__ == "__main__":
    main()
