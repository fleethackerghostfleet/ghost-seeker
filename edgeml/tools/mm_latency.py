"""CPU cold-start-frame detector latency, with the production live tiling wrapper."""
import argparse
from dataclasses import replace
import json
import time

import cv2
import numpy as np

from edgeml.sidecar.service import EdgemlEngine
from mm_calibrate import read_rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', required=True)
    parser.add_argument('--out', required=True)
    parser.add_argument('--limit', type=int, default=50)
    args = parser.parse_args()
    cv2.setNumThreads(4)
    rows = read_rows(args.input)
    selected = [rows[i] for i in np.linspace(0, len(rows) - 1, args.limit, dtype=int)]
    engine = EdgemlEngine('~/models/D/yolo11n-640/model.onnx', '~/models/D/mobileclip-s1',
                         engine='ort', threads=4)
    fusion = engine.detector
    cfg = fusion.cfg
    output = {}
    for mode in ('baseline', 'crop', 'scan', 'both'):
        fusion.cfg = replace(cfg, top_k=0 if mode == 'scan' else cfg.top_k,
                             scan_stride=10**9 if mode == 'crop' else cfg.scan_stride)
        fusion.base._cursor.clear()
        fusion.base._cost_ms = 0
        elapsed, counts = [], []
        for row in selected:
            image = cv2.imread(row['path'])
            if image is None:
                raise ValueError(row['path'])
            fusion.frame = 1 if mode == 'crop' else 0
            start = time.perf_counter()
            (fusion.base if mode == 'baseline' else fusion).detect(image)
            elapsed.append(1000 * (time.perf_counter() - start))
            counts.append(0 if mode == 'baseline' else fusion.last_scores['crops'])
        output[mode] = dict(n=len(elapsed), mean_ms=float(np.mean(elapsed)),
                            p95_ms=float(np.percentile(elapsed, 95)), mean_crops=float(np.mean(counts)),
                            open_vocabulary_available=engine.open_vocab is not None, open_labels=[])
    with open(args.out, 'w') as stream:
        json.dump(output, stream, indent=2)
    print(json.dumps(output, indent=2))


if __name__ == '__main__':
    main()
