#!/usr/bin/env python3
"""Repeat real embeddings for a labeled synthetic scale test, never a quality test."""
import argparse
import copy
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path, help="Existing backend {items:[...]} export")
    parser.add_argument("output", type=Path)
    parser.add_argument("--count", type=int, default=1000)
    args = parser.parse_args()
    source = json.loads(args.input.read_text())["items"]
    if not source or not 1 <= args.count <= 1000:
        parser.error("source must have items and count must be in1..1000")
    items = []
    for i in range(args.count):
        item = copy.deepcopy(source[i % len(source)])
        item.update(id=f"scale-{i:04d}", source="synthetic-scale-repeat-real-crop")
        items.append(item)
    args.output.write_text(json.dumps({"items": items}) + "\n")
    print(f"Wrote {len(items)} scale entries from {len(source)} source crops; not independent quality examples.")


if __name__ == "__main__":
    main()
