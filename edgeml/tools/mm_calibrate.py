"""Calibrate cached live scores on dev; summarize frozen held-out scores."""
import argparse
import json
from pathlib import Path

import numpy as np


def read_rows(path):
    return [json.loads(line) for line in Path(path).read_text().splitlines()]


def predictions(rows, crop_thr, scan_thr):
    return np.array([r['best_crop_p'] >= crop_thr or r['best_scan_p'] >= scan_thr for r in rows], dtype=bool)


def measure(rows, predicted):
    labels = np.array([bool(r['label']) for r in rows], dtype=bool)
    tp = int(np.sum(labels & predicted))
    fp = int(np.sum(~labels & predicted))
    fn = int(np.sum(labels & ~predicted))
    precision, recall = tp / max(1, tp + fp), tp / max(1, tp + fn)
    return dict(n=len(rows), positives=int(labels.sum()), tp=tp, fp=fp, fn=fn,
                precision=precision, recall=recall,
                f1=2 * tp / max(1, 2 * tp + fp + fn),
                fp_per_1000=1000 * fp / max(1, int((~labels).sum())))


def summarize(rows, crop_thr, scan_thr):
    def emitted_scores(row):
        scores = row.get('scores', [])
        crops = [s for s in scores if s['kind'] == 'crop' and s['p'] >= crop_thr]
        scan = max((s for s in scores if s['kind'] == 'scan' and s['p'] >= scan_thr),
                   key=lambda s: s['p'], default=None)
        return crops + ([scan] if scan is not None else [])

    groups = {'all': rows,
              'public': [r for r in rows if r['source'].startswith('public:')],
              'v3': [r for r in rows if r['source'].startswith('v3_')],
              'v3_neg': [r for r in rows if r['source'].startswith('v3_neg:')],
              'public_hardneg': [r for r in rows if r['source'] == 'public:hardneg']}
    groups.update({s: [r for r in rows if r['source'] == s] for s in sorted({r['source'] for r in rows})})
    groups.update({'subtype:' + s: [r for r in rows if r['label'] and r['subtype'] == s]
                   for s in ('whale', 'blow', 'fluke', 'dolphin')})
    result = {name: measure(group, predictions(group, crop_thr, scan_thr)) for name, group in groups.items()}
    for subtype in ('whale', 'blow', 'fluke', 'dolphin'):
        name = 'subtype:' + subtype
        group = groups[name]
        correct = sum(any(s['subtype'] == subtype for s in emitted_scores(row)) for row in group)
        result[name].update(correct_subtype_tp=correct, correct_subtype_recall=correct / max(1, len(group)))
    return result


def calibrate(rows, target=10.0, score_thr=.35):
    if not rows or not any(r['source'].startswith('v3_neg:') for r in rows):
        raise ValueError('calibration needs dev rows with v3 negatives')
    label = np.array([bool(r['label']) for r in rows])
    negatives = np.array([r['source'].startswith('v3_neg:') and not r['label'] for r in rows])
    crop = np.array([r['best_crop_p'] for r in rows])
    scan = np.array([r['best_scan_p'] for r in rows])
    crops = np.unique(np.r_[score_thr, 1., crop[crop >= score_thr]])
    scans = np.unique(np.r_[score_thr, 1., scan[scan >= score_thr]])
    best = None
    scan_hits = scan[None, :] >= scans[:, None]
    for c in crops:
        predicted = (crop >= c)[None, :] | scan_hits
        fp_budget = (predicted & negatives).sum(axis=1) * 1000 / negatives.sum()
        tp = (predicted & label).sum(axis=1)
        fp = (predicted & ~label).sum(axis=1)
        fn = label.sum() - tp
        f1 = 2 * tp / np.maximum(1, 2 * tp + fp + fn)
        for i in range(len(scans)):
            rank = (fp_budget[i] <= target, f1[i] if fp_budget[i] <= target else -fp_budget[i],
                    f1[i], -fp[i], float(c), float(scans[i]))
            if best is None or rank > best[0]:
                best = (rank, float(c), float(scans[i]))
    return {'crop_thr': best[1], 'scan_thr': best[2], 'target_v3_fp_per_1000': target,
            'feasible': bool(best[0][0])}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--dev', required=True)
    parser.add_argument('--frozen', required=True)
    parser.add_argument('--test')
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    dev = read_rows(args.dev)
    if args.test:
        frozen = json.loads(Path(args.frozen).read_text())
    else:
        frozen = calibrate(dev)
        Path(args.frozen).write_text(json.dumps(frozen, indent=2) + '\n')
    result = {'frozen': frozen, 'dev': summarize(dev, frozen['crop_thr'], frozen['scan_thr'])}
    if args.test:
        test = read_rows(args.test)
        predicted = predictions(test, frozen['crop_thr'], frozen['scan_thr'])
        if any(bool(p) != row['emitted'] for p, row in zip(predicted, test)):
            raise ValueError('cached prediction disagrees with live emitted detections')
        result['test'] = summarize(test, frozen['crop_thr'], frozen['scan_thr'])
    Path(args.out).write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
