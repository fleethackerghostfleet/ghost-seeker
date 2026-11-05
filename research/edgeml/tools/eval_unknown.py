#!/usr/bin/env python
"""CPU dev-only calibration/sweep; frozen, single-pass test evaluation.

Before/after share detector, tracker, matching and known-confidence rule; only unknown
eligibility changes. Missing GT completeness is never assumed to mean complete.
"""
from __future__ import annotations

import argparse
from collections import Counter
from dataclasses import asdict
import hashlib
import json
from pathlib import Path
import sys

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'src'))

from run_c import det_cache_path, load_sets
from edgeml_research.data.seqs import gt_by_frame
from edgeml.detectors.cached import CachedDetector
from edgeml_research.eval.sightings import match_sightings
from edgeml.sidecar.service import _ZeroShotAggregator
from edgeml.tracking import AggregatorParams, SightingAggregator, make_tracker
from edgeml.tracking.embed import crop


class CachedEmbedder:
    def __init__(self, embedder):
        self.embedder, self.cache = embedder, {}

    def embed_crops(self, crops):
        result = []
        for c in crops:
            key = (c.shape, hashlib.sha256(c.tobytes()).digest())
            if key not in self.cache:
                self.cache[key] = self.embedder.embed_crops([c])[0]
            result.append(self.cache[key])
        return np.asarray(result)


def calibrate(embedder, seqs):
    """At most four GT crops/identity, capped at 256, exclusively dev sources."""
    crops, sources = [], []
    for seq in seqs:
        for oid, rows in seq.gt_tracks().items():
            for index in np.linspace(0, len(rows) - 1, min(4, len(rows)), dtype=int):
                fi, box = rows[index]
                image = cv2.imread(str(seq.frame_path(fi)))
                if image is None:
                    raise ValueError(f'Unreadable calibration frame: {seq.frame_path(fi)}')
                crops.append(crop(image, box, .25))
                sources.append({'set': seq.root.parent.name, 'seq': seq.seq_id, 'object': oid, 'frame': fi})
                if len(crops) == 256:
                    break
            if len(crops) == 256:
                break
        if len(crops) == 256:
            break
    if not crops:
        raise ValueError('No dev GT crops for calibration')
    feats = np.concatenate([embedder.embed_crops(crops[i:i + 16]) for i in range(0, len(crops), 16)])
    return feats, sources


def load_model(config, seqs=None):
    if config.get('unknownness_source') == 'label-score':
        return None, None
    from edgeml.embed.clip_embedder import ClipEmbedder
    from edgeml.embed.zeroshot import ZeroShotClassifier

    try:
        embedder = ClipEmbedder('mobileclip-s1', backend='torch')
    except (RuntimeError, OSError) as exc:
        if seqs is None:
            raise RuntimeError('Frozen MobileCLIP model unavailable; refusing test fallback') from exc
        config.update(unknownness_source='label-score', fallback_reason=str(exc))
        return None, None
    classifier = ZeroShotClassifier(embedder)
    if seqs is not None:
        feats, sources = calibrate(embedder, seqs)
        config.update(unknownness_source='mobileclip-s1-mcm', calibration=classifier.calibrate(feats),
                      calibration_crops=sources)
    else:
        classifier.sim_ref = config['calibration']['sim_ref']
        classifier.sim_width = config['calibration']['sim_width']
    return CachedEmbedder(embedder), classifier


def tracked_frames(seq, data, cache):
    path = det_cache_path(data, cache, seq)
    if not path.exists():
        raise FileNotFoundError(path)
    det = CachedDetector(path)
    tracker = make_tracker('bytetrack', fps=seq.fps)
    rows = []
    for fi, t, path in seq.frames():
        image = cv2.imread(str(path))
        if image is None:
            raise ValueError(f'Unreadable frame: {path}')
        det.set_frame(fi)
        rows.append((fi, t, tracker.update(det.detect(image), image, fi, t)))
    return rows


def replay(seq, rows, params, embedder, classifier):
    kw = dict(params=AggregatorParams(**params), frame_size=(seq.width, seq.height))
    if classifier is None:
        agg = SightingAggregator(seq.seq_id, **kw)
    else:
        agg = _ZeroShotAggregator(seq.seq_id, embedder=embedder, classifier=classifier, **kw)
    events = []
    for fi, t, tracks in rows:
        image = cv2.imread(str(seq.frame_path(fi)))
        events.extend(agg.update(tracks, image, fi, t))
    events.extend(agg.flush(seq.duration_s + agg.p.close_after_s + 1))
    return events, agg.trajectories()


def measure(seq, events, trajectories, tau, known_score=.5):
    matches = match_sightings(trajectories, seq.gt_tracks())
    # Every annotation, including ignored boxes, excludes a frame from sea-only negatives.
    annotated = {row[0] for row in seq.gt}
    status = seq.meta.get('gt_status', 'unspecified')
    out = {}
    for mode in ('before', 'after'):
        surfaced, unknown, eligible, sea_false = set(), set(), set(), set()
        for e in events:
            sid = e['sighting_id']
            is_unknown = e['unknownness'] >= tau
            if is_unknown:
                eligible.add(sid)
            flagged = is_unknown if mode == 'before' else e['unknown_real']
            if flagged:
                unknown.add(sid)
                if matches.get(sid) is None and e['frame_idx'] not in annotated:
                    sea_false.add(sid)
            if flagged or (not is_unknown and e['best_score'] >= known_score):
                surfaced.add(sid)
        matched_candidates = {s for s in eligible if matches.get(s) is not None}
        out[mode] = dict(gt=len(seq.gt_tracks()),
                         surfaced_gt=len({matches[s] for s in surfaced if matches.get(s) is not None}),
                         unknown_gt=len({matches[s] for s in unknown if matches.get(s) is not None}),
                         matched_unknown_candidates=len(matched_candidates),
                         matched_unknown_flagged=len(matched_candidates & unknown),
                         sea_frames=seq.n_frames - len(annotated) if status == 'complete' else 0,
                         false_unknown_sea=len(sea_false) if status == 'complete' else 0,
                         unmatched_noncomplete=sum(matches.get(s) is None for s in unknown) if status != 'complete' else 0)
    return {'set': seq.root.parent.name, 'seq': seq.seq_id, 'gt_status': status, **out}


def pool(results):
    out = {}
    for mode in ('before', 'after'):
        counts = Counter()
        for r in results:
            counts.update(r[mode])
        c = dict(counts)
        c['object_recall'] = c['surfaced_gt'] / c['gt'] if c['gt'] else None
        c['unknown_recall'] = c['matched_unknown_flagged'] / c['matched_unknown_candidates'] if c['matched_unknown_candidates'] else None
        c['false_unknown_per_sea_frame'] = c['false_unknown_sea'] / c['sea_frames'] if c['sea_frames'] else None
        out[mode] = c
    out['completeness'] = dict(Counter(r['gt_status'] for r in results))
    return out


def verify_split(seqs, split):
    import yaml

    spec = yaml.safe_load((Path(__file__).resolve().parents[1] / 'configs/splits.yaml').read_text())
    for seq in seqs:
        if seq.seq_id not in spec[split][seq.root.parent.name]:
            raise ValueError(f'Source not in canonical {split} split: {seq.seq_id}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data', type=Path, required=True)
    parser.add_argument('--sets', nargs='+', default=['ghost_fleet_5fps', 'fvessel_clip10'])
    parser.add_argument('--cache', default='yolo11n-c05')
    parser.add_argument('--split', choices=['dev', 'test'], required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--frozen', type=Path, help='Required on test; never recalibrate or sweep test')
    args = parser.parse_args()
    if args.split == 'test' and args.frozen is None:
        parser.error('test requires --frozen')
    args.data, args.out = args.data.expanduser(), args.out.expanduser()
    args.out.mkdir(parents=True, exist_ok=True)
    result_path = args.out / f'{args.split}.json'
    if result_path.exists():
        parser.error(f'{result_path} exists; refusing to overwrite an evaluation')
    import torch

    torch.set_num_threads(4)
    cv2.setNumThreads(4)
    seqs = load_sets(args.data, args.sets, args.split)
    verify_split(seqs, args.split)
    if not seqs:
        parser.error('No sequences')
    config = json.loads(args.frozen.expanduser().read_text()) if args.frozen else {
        'params': asdict(AggregatorParams()), 'known_score': .5, 'cache': args.cache,
        'tracker': 'bytetrack', 'calibration_split': 'dev',
        'selection_rule': 'Maximise surfaced GT within one object of best dev recall, then minimise sea false unknowns; prefer unknown recall then stricter persistence.'}
    if config['cache'] != args.cache or config['calibration_split'] != 'dev':
        parser.error('Frozen cache/calibration mismatch')
    embedder, classifier = load_model(config, None if args.frozen else seqs)
    variants = [(config['params']['unknown_min_hits'], config['params']['realness_threshold'])] if args.frozen else [
        (n, rho) for n in (3, 5, 8) for rho in (.3, .5, .7)]
    all_results = [[] for _ in variants]
    for seq in seqs:
        print(f'{args.split}: {seq.seq_id} ({seq.meta.get("gt_status", "unspecified")})', flush=True)
        rows = tracked_frames(seq, args.data, args.cache)
        for index, (n, rho) in enumerate(variants):
            params = config['params'] | {'unknown_min_hits': n, 'realness_threshold': rho}
            events, traj = replay(seq, rows, params, embedder, classifier)
            all_results[index].append(measure(seq, events, traj, params['unknown_threshold'], config['known_score']))
    sweep = [dict(N=n, rho=rho, **pool(r)) for (n, rho), r in zip(variants, all_results)]
    best_recall = max(r['after']['surfaced_gt'] for r in sweep)
    candidates = [i for i, r in enumerate(sweep) if r['after']['surfaced_gt'] >= best_recall - 1]
    chosen = min(candidates, key=lambda i: (sweep[i]['after']['false_unknown_sea'],
                 -sweep[i]['after']['matched_unknown_flagged'], -sweep[i]['N'], -sweep[i]['rho']))
    n, rho = variants[chosen]
    config['params'].update(unknown_min_hits=n, realness_threshold=rho)
    result_path.write_text(json.dumps({'split': args.split, 'config': config, 'sweep': sweep,
                                      'chosen': sweep[chosen], 'sequences': all_results[chosen]}, indent=2))
    if not args.frozen:
        (args.out / 'frozen.json').write_text(json.dumps(config, indent=2))
    print('| N | rho | after GT recall | unknown recall | sea false / frames | noncomplete unmatched |')
    for r in sweep:
        a = r['after']
        print(f'| {r["N"]} | {r["rho"]} | {a["surfaced_gt"]}/{a["gt"]} | {a["matched_unknown_flagged"]}/{a["matched_unknown_candidates"]} | {a["false_unknown_sea"]}/{a["sea_frames"]} | {a["unmatched_noncomplete"]} |')
    print('Chosen:', n, rho)
    print(json.dumps(sweep[chosen], indent=2))


if __name__ == '__main__':
    main()
