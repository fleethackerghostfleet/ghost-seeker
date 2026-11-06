"""Stateless frame scores and dev-only threshold selection for marine mammals."""
import argparse
import json
import time
from pathlib import Path

import cv2
import numpy as np

from edgeml.detectors.marine_mammal import MarineMammalConfig, MarineMammalVerifier, raw_scores
from edgeml.embed.clip_embedder import ClipEmbedder
from edgeml.runtime.detectors import load_detector


def metrics(rows, threshold, score_key='score'):
    tp = fp = fn = negatives = 0
    for row in rows:
        positive, predicted = bool(row['label_pos']), row[score_key] >= threshold
        tp += positive and predicted
        fp += not positive and predicted
        fn += positive and not predicted
        negatives += not positive
    precision, recall = tp / max(1, tp + fp), tp / max(1, tp + fn)
    return {'precision': precision, 'recall': recall, 'f1': 2 * precision * recall / max(1e-12, precision + recall),
            'fp_per_1000': 1000 * fp / max(1, negatives), 'tp': tp, 'fp': fp, 'fn': fn}


def pick_threshold(rows, target_fp_per_1000, score_key='score'):
    dev = [r for r in rows if r.get('split', r.get('eval_split')) == 'dev']
    if not dev or not any(not r['label_pos'] for r in dev):
        raise ValueError('threshold selection requires dev rows including negatives')
    candidates = [float(np.nextafter(1.0, 2.0))] + sorted({r[score_key] for r in dev})
    valid = [(threshold, metrics(dev, threshold, score_key)) for threshold in candidates]
    valid = [entry for entry in valid if entry[1]['fp_per_1000'] <= target_fp_per_1000]
    if not valid:
        raise ValueError('no threshold satisfies FP budget')
    return max(valid, key=lambda entry: (entry[1]['f1'], entry[0]))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--detector', default='~/models/D/yolo11n-640/model.onnx')
    parser.add_argument('--embed', default='~/models/D/mobileclip-s1')
    parser.add_argument('--threads', type=int, default=4)
    parser.add_argument('--limit', type=int)
    parser.add_argument('--mm', default='{}')
    parser.add_argument('--live', action='store_true', help='Use the sidecar detector stack, each image as a cold-start frame')
    args = parser.parse_args()
    cv2.setNumThreads(args.threads)
    if args.live:
        from edgeml.sidecar.service import EdgemlEngine

        engine = EdgemlEngine(args.detector, args.embed, engine='ort', threads=args.threads,
                              marine_mammal=json.loads(args.mm))
        detector = engine.detector
    else:
        detector = load_detector(Path(args.detector).expanduser(), engine='ort', threads=args.threads, score_thr=0.05)
        embedder = ClipEmbedder('mobileclip-s1', backend='ort', onnx_dir=Path(args.embed).expanduser(), threads=args.threads)
        verifier = MarineMammalVerifier(embedder)
        cfg = MarineMammalConfig(**json.loads(args.mm))
    timings = []
    with args.input.open() as source, args.out.open('w') as output:
        for index, line in enumerate(source):
            if args.limit is not None and index >= args.limit:
                break
            row = json.loads(line)
            path = row.get('local_path', row.get('path'))
            image = cv2.imread(path)
            if image is None:
                raise ValueError(f"unreadable image: {path}")
            start = time.perf_counter()
            if args.live:
                detector.frame = 0
                detections = detector.detect(image)
                total_ms = (time.perf_counter() - start) * 1000
                result = dict(detector.last_scores)
                yolo_ms = total_ms - result['mm_ms']
                result.update(total_ms=total_ms, tiles=detector.base.n_calls - 1,
                              emitted=any(d.label == 'marine mammal' and d.score >= engine.score_thr for d in detections))
            else:
                detections = detector.detect(image)
                yolo_ms = (time.perf_counter() - start) * 1000
                result = raw_scores(image, detections, verifier, cfg)
            result.update(yolo_ms=yolo_ms, det_labels=[d.label for d in detections])
            result['score'] = max(result['best_crop_p'], result['best_scan_p'])
            result['predicted_subtype'] = result.pop('subtype', None)
            output.write(json.dumps({**row, **result}) + '\n')
            output.flush()
            timings.append((yolo_ms, result['mm_ms'], result['crops']))
    if timings:
        print(json.dumps(dict(zip(('mean_yolo_ms', 'mean_mm_ms', 'mean_crops'), np.mean(timings, axis=0).tolist())), sort_keys=True))
        print(f'Wrote {len(timings)} frames to {args.out}')


if __name__ == '__main__':
    main()
