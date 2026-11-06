"""Deterministic synthetic thermal development frames. No real IR inputs."""
from __future__ import annotations

import argparse
import json
from pathlib import Path

import cv2
import numpy as np


def generate(seed=1729, count=120, width=480, height=320):
    rng = np.random.default_rng(seed)
    yy, xx = np.mgrid[:height, :width]
    for index in range(count):
        horizon = rng.uniform(.25, .55) * height + rng.uniform(-.025, .025) * (xx - width / 2)
        sea = yy >= horizon
        image = np.where(sea, 85 + .08 * yy, 145 - .06 * yy)
        image += rng.normal(0, rng.uniform(2, 7), image.shape)
        image += sea * (3 * np.sin(yy * .6 + xx * .04) + 2 * np.sin(yy * 1.4 - xx * .09))
        image = np.clip(image, 0, 255).astype(np.uint8)
        for _ in range(12):
            x, y = int(rng.integers(width)), int(rng.integers(int(height * .6), height))
            cv2.line(image, (x, y), (min(width - 1, x + int(rng.integers(15, 80))), y), int(rng.integers(110, 135)), 1)
        boxes = []
        polarity = 1 if index % 2 == 0 else -1
        if index % 10 >= 3:
            size = int(rng.integers(3, 121))
            bw, bh = size, max(3, int(size * rng.uniform(.25, .7)))
            x = int(rng.integers(0, width - bw))
            y = int(rng.integers(int(height * .6), max(int(height * .6) + 1, height - bh)))
            value = int(np.clip(float(np.median(image[y:y + bh, x:x + bw])) + polarity * rng.uniform(40, 100), 0, 255))
            if index % 3 == 0:
                cv2.ellipse(image, (x + bw // 2, y + bh // 2), (max(1, bw // 2 - 1), max(1, bh // 2 - 1)), 0, 0, 360, value, -1)
            else:
                cv2.rectangle(image, (x, y + bh // 3), (x + bw - 1, y + bh - 1), value, -1)
                cv2.rectangle(image, (x + bw // 3, y), (x + 2 * bw // 3, y + bh // 2), value, -1)
            boxes.append([x, y, bw, bh])
        yield cv2.cvtColor(image, cv2.COLOR_GRAY2BGR), {"index": index, "boxes": boxes, "polarity": polarity, "seed": seed}


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--seed", type=int, default=1729)
    ap.add_argument("--count", type=int, default=120)
    args = ap.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    with (args.output / "manifest.jsonl").open("w") as out:
        for image, record in generate(args.seed, args.count):
            record["path"] = f"{record['index']:06d}.png"
            if not cv2.imwrite(str(args.output / record["path"]), image):
                raise OSError("failed to write synthetic frame")
            out.write(json.dumps(record) + "\n")


if __name__ == "__main__":
    main()
