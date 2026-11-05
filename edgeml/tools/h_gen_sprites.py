#!/usr/bin/env python
"""Generate chroma-keyed RGBA cut-outs of unusual sea-surface objects with Vertex AI Gemini image generation.

Usage: h_gen_sprites.py --out ~/data/synth_src/sprites [--per-class 8] [--classes ...]
Needs a signed-in gcloud (fleet-hackathon-gcp skill). Each class gets `per_class` images from varied prompts; the flat
green background is keyed out (hue test + despill) and the sprite is cropped to its alpha bounding box.
"""
from __future__ import annotations

import argparse
import base64
import json
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import cv2
import numpy as np

PROJECT = "qwiklabs-gcp-02-34a870014566"
MODEL = "gemini-2.5-flash-image"
URL = f"https://aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/global/publishers/google/models/{MODEL}:generateContent"

# class -> (object description variants, real-world width m, real-world height above waterline m)
CLASSES: dict[str, tuple[list[str], float, float]] = {
    "semi_submersible": (["a low-profile narco semi-submersible vessel, grey-blue hull barely above the waterline, small conning tower, side view",
                          "a makeshift semi-submersible smuggling boat, dark fibreglass, only the top deck and a short tower visible, three-quarter view",
                          "a long low semi-submersible craft with a small exhaust pipe and hatch, matte grey, side view in daylight"], 14.0, 1.3),
    "periscope_mast": (["a submarine periscope mast sticking straight up out of the water with a small feather of white spray, side view",
                        "a thin dark snorkel mast and periscope of a submerged submarine above the surface with a small wake, side view",
                        "a submarine's raised periscope and radio mast, grey, close together, small splash at the base, side view"], 1.2, 2.5),
    "usv": (["a small grey unmanned surface vessel (USV) with a sensor mast and antennas, no people, side view",
             "a military unmanned surface drone boat, dark grey, low cabin with a radar dome, three-quarter view",
             "an autonomous rigid-hull patrol boat with antennas and no crew, light grey, side view"], 7.0, 2.0),
    "jet_ski": (["a person riding a jet ski (personal watercraft), bright colours, side view",
                 "a red and white jet ski with one rider, three-quarter front view",
                 "a black jet ski with two riders, side view, small spray"], 3.2, 1.6),
    "life_raft": (["an orange inflatable life raft with a canopy, side view",
                   "a round orange survival life raft with a dark canopy and reflective tape, three-quarter view",
                   "an inflatable emergency life raft, orange and black, floating, side view"], 3.0, 1.4),
    "debris_container": (["a shipping container floating half-submerged, rusty blue, side view",
                          "a partially submerged red shipping container, corner tilted up, three-quarter view",
                          "a pile of floating debris: broken wooden pallets, a plastic barrel and a log, side view at water level"], 9.0, 1.2),
    "buoy": (["a yellow navigation buoy with a light on top, side view",
              "a red and white striped channel marker buoy, side view",
              "a large steel mooring buoy, dark green, side view"], 2.5, 3.0),
}
STYLE = (" The object is seen from sea level at a distance, as a telephoto photograph in natural daylight. "
         "Place the whole object on a solid, perfectly flat, uniform bright green (#00FF00) chroma-key background. "
         "No water, no waves, no reflections, no shadow, no horizon, no text, no other objects. Photorealistic.")
VARIANTS = ["", " Slightly hazy, overcast light.", " Bright sun, hard shadows on the object.", " Golden evening light."]


def token() -> str:
    return subprocess.check_output(["gcloud", "auth", "print-access-token"], text=True).strip()


def generate(prompt: str, tok: str, seed: int) -> bytes | None:
    import urllib.request

    body = {"contents": [{"role": "user", "parts": [{"text": prompt}]}],
            "generationConfig": {"responseModalities": ["IMAGE"], "seed": seed}}
    req = urllib.request.Request(URL, data=json.dumps(body).encode(), headers={"Authorization": f"Bearer {tok}", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=180) as r:
            d = json.load(r)
    except Exception as e:  # noqa: BLE001
        print("gen failed:", str(e)[:200], file=sys.stderr)
        return None
    for p in d.get("candidates", [{}])[0].get("content", {}).get("parts", []):
        if "inlineData" in p:
            return base64.b64decode(p["inlineData"]["data"])
    return None


def chroma_key(img_bgr: np.ndarray) -> np.ndarray | None:
    """Key out the green screen -> BGRA sprite cropped to content. Returns None when the key fails."""
    b, g, r = [img_bgr[..., i].astype(np.float32) for i in range(3)]
    dom = g - np.maximum(r, b)                       # green dominance
    alpha = np.clip((40.0 - dom) / 40.0, 0, 1)       # dom >= 40 -> 0, dom <= 0 -> 1
    alpha = (alpha * 255).astype(np.uint8)
    hard = (alpha > 128).astype(np.uint8)
    hard = cv2.morphologyEx(hard, cv2.MORPH_OPEN, np.ones((5, 5), np.uint8))
    n, lab, stats, _ = cv2.connectedComponentsWithStats(hard)
    if n < 2:
        return None
    keep = np.zeros_like(hard)
    big = stats[1:, cv2.CC_STAT_AREA].max()
    for i in range(1, n):
        if stats[i, cv2.CC_STAT_AREA] >= 0.02 * big:
            keep[lab == i] = 1
    alpha = (alpha.astype(np.float32) * keep).astype(np.uint8)
    frac = (alpha > 128).mean()
    if frac < 0.002 or frac > 0.85:   # periscope/snorkel masts are ~0.3 % of the render
        return None
    # despill: cap green at the mean of red and blue where it dominates
    out = img_bgr.copy().astype(np.float32)
    spill = dom > 0
    out[..., 1][spill] = np.minimum(out[..., 1][spill], (r[spill] + b[spill]) / 2 + 5)
    ys, xs = np.where(alpha > 10)
    y0, y1, x0, x1 = ys.min(), ys.max() + 1, xs.min(), xs.max() + 1
    bgra = np.dstack([out.clip(0, 255).astype(np.uint8), alpha])[y0:y1, x0:x1]
    return bgra


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="~/data/synth_src/sprites")
    ap.add_argument("--per-class", type=int, default=8)
    ap.add_argument("--classes", nargs="*", default=list(CLASSES))
    ap.add_argument("--workers", type=int, default=4)
    a = ap.parse_args()
    out = Path(a.out).expanduser()
    tok = token()
    jobs = []
    for c in a.classes:
        descs, _, _ = CLASSES[c]
        for i in range(a.per_class):
            prompt = "A photograph of " + descs[i % len(descs)] + "." + STYLE + VARIANTS[(i // len(descs)) % len(VARIANTS)]
            jobs.append((c, i, prompt))
    (out / "_raw").mkdir(parents=True, exist_ok=True)
    meta = {"model": MODEL, "classes": {c: {"width_m": CLASSES[c][1], "height_m": CLASSES[c][2]} for c in CLASSES}, "sprites": []}

    def run(job):
        c, i, prompt = job
        dst = out / c / f"{c}_{i:02d}.png"
        if dst.exists():
            return c, i, prompt, True
        png = generate(prompt, tok, seed=1000 + i)
        if png is None:
            return c, i, prompt, False
        raw = out / "_raw" / f"{c}_{i:02d}.png"
        raw.write_bytes(png)
        img = cv2.imdecode(np.frombuffer(png, np.uint8), cv2.IMREAD_COLOR)
        sp = chroma_key(img)
        if sp is None:
            return c, i, prompt, False
        dst.parent.mkdir(parents=True, exist_ok=True)
        cv2.imwrite(str(dst), sp)
        return c, i, prompt, True

    with ThreadPoolExecutor(a.workers) as ex:
        for c, i, prompt, ok in ex.map(run, jobs):
            print(f"{c}_{i:02d}: {'ok' if ok else 'FAILED'}", flush=True)
            meta["sprites"].append({"class": c, "idx": i, "prompt": prompt, "ok": ok})
    (out / "meta.json").write_text(json.dumps(meta, indent=1))


if __name__ == "__main__":
    main()
