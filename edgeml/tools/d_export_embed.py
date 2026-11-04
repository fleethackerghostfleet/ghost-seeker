"""Workstream D: export CLIP image towers (MobileCLIP-S0/S1, SigLIP-B16, OpenCLIP-B32) to ONNX + OpenVINO fp32/fp16/int8
and cache the maritime text bank so the laptop never loads the text tower.

  python tools/d_export_embed.py --out ~/models/clip --models mobileclip-s0 mobileclip-s1 --calib-ann ~/data/catalog/ann/seaships7000.jsonl \
      --calib-ids ~/data/seaships_calib_ids.txt --calib-images ~/data/seaships/calib --n-calib 200
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--models", nargs="+", default=["mobileclip-s0", "mobileclip-s1", "siglip-b16", "openclip-b32"])
    ap.add_argument("--calib-ann")
    ap.add_argument("--calib-ids")
    ap.add_argument("--calib-images")
    ap.add_argument("--n-calib", type=int, default=200)
    args = ap.parse_args()
    import cv2

    from edgeml.embed.clip_embedder import ClipEmbedder
    from edgeml.embed.zeroshot import SUBTYPES, UNKNOWN_PROMPTS
    from edgeml.forms import crop_with_context

    prompts = [p for c in SUBTYPES.values() for p in c] + UNKNOWN_PROMPTS
    crops = []
    if args.calib_ann:
        ids = set(Path(args.calib_ids).read_text().split()) if args.calib_ids else None
        for line in Path(args.calib_ann).read_text().splitlines():
            d = json.loads(line)
            if not d["box"] or (ids and d["image_id"] not in ids):
                continue
            p = Path(args.calib_images) / (d["image_id"].split("/")[-1] + ".jpg")
            if p.exists():
                im = cv2.imread(str(p))
                crops.append(crop_with_context(im, d["box"], 1.5)[0])
            if len(crops) >= args.n_calib:
                break
        print(f"{len(crops)} calibration crops (dev only)", file=sys.stderr)
    for short in args.models:
        e = ClipEmbedder(short)
        d = e.export_image_tower(Path(args.out) / short, prompts=prompts, int8_calib=crops or None)
        sizes = {p.relative_to(d).as_posix(): p.stat().st_size for p in d.rglob("model.bin")}
        sizes["model.onnx"] = (d / "model.onnx").stat().st_size
        print(json.dumps({"model": short, "dim": e.dim, "input": e.size, "bytes": sizes}), file=sys.stderr)


if __name__ == "__main__":
    main()
