"""Bounded real-frame CPU benchmark. Reads pixels only; no GT or semantic metrics."""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import subprocess
import sys
import time

from benchmark_scale import configure_threads, memory_snapshot, summaries, write_checkpoint


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def image_paths(directory, expected):
    paths = sorted(p for p in Path(directory).iterdir()
                   if p.is_file() and p.suffix.lower() in {'.jpg', '.jpeg', '.png'})
    if len(paths) != expected:
        raise ValueError(f'Expected exactly {expected} flat-directory images, found {len(paths)}')
    return paths


def candidate_rank(records, method, descriptors, *, prune):
    """Pinned greedy score and ID ties; exact upper-bound pruning is optional."""
    from ghost_priority.policy import score_record
    started = time.perf_counter()
    pending = sorted((r for r in records if r.get('usable') is not False), key=lambda r: r['id'])
    bases = {r['id']: score_record(r)['score'] for r in pending}
    maximum = {r['id']: 0.0 for r in pending}
    nearest = {r['id']: None for r in pending}
    pairs = len(pending) * (len(pending) - 1) // 2
    order = []
    comparisons = bounds = skipped = 0
    while pending:
        best = min(pending, key=lambda r: (-bases[r['id']] * (1 - .85 * maximum[r['id']]), r['id']))
        bid = best['id']
        order.append({'id': bid, 'final_score': bases[bid] * (1 - .85 * maximum[bid]),
                      'max_similarity': maximum[bid], 'nearest_prior_id': nearest[bid]})
        pending.remove(best)
        for other in pending:
            oid = other['id']
            if prune:
                bounds += 1
                if method.upper_bound(descriptors[bid], descriptors[oid]) <= maximum[oid]:
                    skipped += 1
                    continue
            value = float(method.compare(descriptors[bid], descriptors[oid]))
            comparisons += 1
            if not math.isfinite(value) or not 0 <= value <= 1:
                raise ValueError('Invalid similarity')
            if value > maximum[oid]:
                maximum[oid], nearest[oid] = value, bid
    return order, {'wall_seconds': time.perf_counter() - started, 'possible_pairs': pairs,
                   'full_comparisons': comparisons, 'bound_checks': bounds,
                   'skipped_comparisons': skipped}


def worker(args):
    threads = configure_threads(args.threads)
    import cv2
    import numpy as np
    sys.path.insert(0, str(Path(args.baseline_path).resolve()))
    from ghost_priority.analysis import Analyzer
    from ghost_priority.policy import rank_records
    from candidate_methods import get_methods, descriptor_nbytes

    paths = image_paths(args.image_dir, args.expected_count)
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    target = out / (args.worker + '.json')
    candidates = get_methods()
    setup_started = time.perf_counter()
    analyzer = Analyzer(args.model_path if args.worker == 'nanodet' else None, width=320,
                        threads=0 if threads['opencv_serial_fallback'] else args.threads)
    initialization_seconds = time.perf_counter() - setup_started
    threads['opencv_after_analyzer'] = cv2.getNumThreads()
    source = Path(__file__).resolve().parent
    result = {'status': 'running', 'mode': args.worker,
              'scope': 'Real image computational benchmark; no annotations, semantic metrics, or device certification.',
              'input_count': len(paths), 'nominal_window_seconds': args.window_seconds,
              'nominal_average_input_fps': len(paths) / args.window_seconds,
              'analyzer_initialization_seconds': initialization_seconds,
              'providers': analyzer.provider_names,
              'environment': {'platform': platform.platform(), 'machine': platform.machine(),
                  'python': platform.python_version(), 'numpy': np.__version__, 'opencv': cv2.__version__,
                  'logical_cpus': os.cpu_count(), 'threads': threads},
              'config': {'analysis_size': 320, 'payload_size': 1280, 'jpeg_quality': 80,
                         'candidate_configs': {n: m.config for n, m in candidates.items()}},
              'provenance': {'candidate_sha256': sha(source / 'candidate_methods.py'),
                  'script_sha256': sha(__file__), 'scale_helpers_sha256': sha(source / 'benchmark_scale.py'),
                  'model_sha256': sha(args.model_path) if args.worker == 'nanodet' else None,
                  'baseline_source_sha256': {str(p.relative_to(args.baseline_path)): sha(p)
                      for p in sorted((Path(args.baseline_path) / 'ghost_priority').rglob('*.py'))},
                  'inputs': [{'name': p.name, 'sha256': sha(p)} for p in paths]},
              'frames': [], 'methods': {}, 'parity_checks': {},
              'memory': {'before_frames': memory_snapshot()},
              'limitations': [
                  'Per-method pipeline totals sum measured common stages plus that method preparation/ranking; they are not independent end-to-end worker wall times.',
                  'All candidates share one process per analyzer mode; peak RSS includes every candidate cache and cannot be attributed to one method.',
                  'Measured encode includes resize and in-memory 1280/q80 JPEG encoding, not payload writes, transmission or selection.',
                  'File hashing occurs before processing and can warm the filesystem cache; storage cold-start latency is not measured.',
                  'Amortized FPS is batch compute capacity, not streaming or first-report latency. Startup, parity checks and study bookkeeping are separate.',
                  'One development scene and one pass do not certify sustained laptop performance or tail latency.'
              ]}
    write_checkpoint(target, result)
    records = []
    descriptors = {name: {} for name in candidates}
    processing_started = time.perf_counter()
    for i, path in enumerate(paths):
        tick = time.perf_counter()
        bgr = cv2.imread(str(path))
        decode_ms = (time.perf_counter() - tick) * 1000
        if bgr is None:
            raise ValueError(f'Unreadable image: {path.name}')
        tick = time.perf_counter()
        evidence = analyzer.analyze(bgr)
        analysis_ms = (time.perf_counter() - tick) * 1000
        tick = time.perf_counter()
        h, w = bgr.shape[:2]
        scale = min(1., 1280 / max(h, w))
        payload = cv2.resize(bgr, (max(1, round(w * scale)), max(1, round(h * scale))),
                             interpolation=cv2.INTER_AREA) if scale < 1 else bgr
        ok, encoded = cv2.imencode('.jpg', payload, [cv2.IMWRITE_JPEG_QUALITY, 80])
        if not ok:
            raise ValueError('JPEG encoding failed')
        encode_ms = (time.perf_counter() - tick) * 1000
        rid = f'frame-{i:06d}'
        records.append({**evidence, 'id': rid, 'payload_bytes': int(encoded.nbytes)})
        row = {'id': rid, 'file': path.name, 'shape_bgr': list(bgr.shape),
               'payload_bytes': int(encoded.nbytes), 'usable': evidence['usable'],
               'read_decode_ms': decode_ms, 'analysis_ms': analysis_ms, 'encode_ms': encode_ms,
               'common_pipeline_ms': decode_ms + analysis_ms + encode_ms, 'prepare_ms': {}}
        for name, method in candidates.items():
            tick = time.perf_counter()
            descriptors[name][rid] = method.prepare(bgr)
            row['prepare_ms'][name] = (time.perf_counter() - tick) * 1000
        result['frames'].append(row)
    result['frame_processing_all_methods_wall_seconds'] = time.perf_counter() - processing_started
    result['memory']['after_all_descriptors'] = memory_snapshot()
    result['usable_count'] = sum(r['usable'] for r in records)
    result['common_stages'] = {}
    for key in ['read_decode_ms', 'analysis_ms', 'encode_ms', 'common_pipeline_ms']:
        values = [r[key] for r in result['frames']]
        result['common_stages'][key] = {**summaries(values), 'total_seconds': sum(values) / 1000}
    common = result['common_stages']['common_pipeline_ms']['total_seconds']
    write_checkpoint(target, result)
    for name in ['color-phash', 'no_similarity', *candidates]:
        if name in candidates:
            order, stats = candidate_rank(records, candidates[name], descriptors[name], prune=True)
            prep = [r['prepare_ms'][name] for r in result['frames']]
            prep_seconds = sum(prep) / 1000
            prepare = {**summaries(prep), 'total_seconds': prep_seconds}
            payload_bytes = sum(descriptor_nbytes(d) for d in descriptors[name].values())
        else:
            tick = time.perf_counter()
            ranked = rank_records(records, policy={'duplicate_strength': 0} if name == 'no_similarity'
                                  else {'similarity_method': 'color-phash'})
            elapsed = time.perf_counter() - tick
            order = [{k: r[k] for k in ['id', 'final_score', 'max_similarity', 'nearest_prior_id']} for r in ranked]
            stats = {'wall_seconds': elapsed, 'full_comparisons': None, 'bound_checks': None,
                     'skipped_comparisons': None, 'counter_note': 'Builtin counters are not instrumented.'}
            prep_seconds = 0.
            prepare, payload_bytes = None, None
        total = common + prep_seconds + stats['wall_seconds']
        result['methods'][name] = {'ranking': stats, 'preparation': prepare,
            'descriptor_payload_bytes': payload_bytes, 'pipeline_stage_sum_seconds': total,
            'pipeline_stage_sum_with_analyzer_startup_seconds': total + initialization_seconds,
            'amortized_input_fps': len(paths) / total, 'compute_to_window_ratio': total / args.window_seconds,
            'order': order, 'rank_sha256': hashlib.sha256(json.dumps(order, sort_keys=True).encode()).hexdigest()}
        write_checkpoint(target, result)
    subset = records[:min(args.parity_size, len(records))]
    for name, method in candidates.items():
        raw, raw_stats = candidate_rank(subset, method, descriptors[name], prune=False)
        fast, fast_stats = candidate_rank(subset, method, descriptors[name], prune=True)
        same = raw == fast
        result['parity_checks'][name] = {'input_count': len(subset), 'unpruned': raw_stats,
            'pruned': fast_stats, 'exact_order_scores_and_nearest_parity': same,
            'excluded_from_pipeline_totals': True}
        write_checkpoint(target, result)
        if not same:
            raise AssertionError(f'Exact pruning parity failure: {name}')
    result['memory']['final'] = memory_snapshot()
    result['status'] = 'complete'
    write_checkpoint(target, result)
    print(json.dumps({'mode': args.worker, 'status': 'complete', 'report': str(target)}), flush=True)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image-dir', required=True)
    parser.add_argument('--out', required=True)
    parser.add_argument('--model-path', help='When present, run both NoObjects and NanoDet modes in fresh processes.')
    parser.add_argument('--baseline-path')
    parser.add_argument('--threads', type=int, default=4)
    parser.add_argument('--expected-count', type=int, default=120)
    parser.add_argument('--parity-size', type=int, default=30)
    parser.add_argument('--window-seconds', type=float, default=60.)
    parser.add_argument('--timeout-seconds', type=int, default=600)
    parser.add_argument('--worker', choices=['no_objects', 'nanodet'], help=argparse.SUPPRESS)
    args = parser.parse_args()
    if min(args.threads, args.expected_count, args.parity_size, args.timeout_seconds) < 1 or args.window_seconds <= 0:
        parser.error('Counts, timeout, threads and window must be positive.')
    if args.expected_count > 300:
        parser.error('Bounded study accepts at most 300 images.')
    if args.model_path and not Path(args.model_path).is_file():
        parser.error('Model path must name an existing local file; no downloads are performed.')
    if not args.baseline_path:
        source = Path(__file__).resolve()
        options = [source.parents[4] / 'algorithm']
        args.baseline_path = next((str(p) for p in options if (p / 'ghost_priority').is_dir()), None)
    if not args.baseline_path or not (Path(args.baseline_path) / 'ghost_priority').is_dir():
        parser.error('Provide --baseline-path pointing at the pinned algorithm directory.')
    args.baseline_path = str(Path(args.baseline_path).resolve())
    image_paths(args.image_dir, args.expected_count)
    if args.worker:
        return worker(args)
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=False)
    modes = ['no_objects', 'nanodet'] if args.model_path else ['no_objects']
    deadline = time.monotonic() + args.timeout_seconds
    report = {'status': 'running', 'modes': [], 'expected_input_count': args.expected_count,
              'total_timeout_seconds': args.timeout_seconds, 'script_sha256': sha(__file__)}
    for mode in modes:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            report['modes'].append({'mode': mode, 'status': 'skipped_timeout'})
            continue
        command = [sys.executable, str(Path(__file__).resolve()), '--worker', mode,
            '--image-dir', str(Path(args.image_dir).resolve()), '--out', str(out),
            '--baseline-path', args.baseline_path, '--threads', str(args.threads),
            '--expected-count', str(args.expected_count), '--parity-size', str(args.parity_size),
            '--window-seconds', str(args.window_seconds)]
        if args.model_path:
            command += ['--model-path', str(Path(args.model_path).resolve())]
        started = time.perf_counter()
        try:
            completed = subprocess.run(command, capture_output=True, text=True, timeout=remaining)
            (out / (mode + '.log')).write_text(completed.stdout + completed.stderr)
            status = 'complete' if completed.returncode == 0 else 'failed'
            entry = {'mode': mode, 'status': status, 'returncode': completed.returncode}
        except subprocess.TimeoutExpired as exc:
            data = (exc.stdout or b'') + (exc.stderr or b'')
            (out / (mode + '.log')).write_bytes(data)
            entry = {'mode': mode, 'status': 'timeout'}
        entry['worker_wall_seconds'] = time.perf_counter() - started
        report['modes'].append(entry)
        write_checkpoint(out / 'summary.json', report)
        print(json.dumps(entry), flush=True)
    report['status'] = 'complete' if all(r['status'] == 'complete' for r in report['modes']) else 'incomplete'
    write_checkpoint(out / 'summary.json', report)
    return 0 if report['status'] == 'complete' else 1


if __name__ == '__main__':
    raise SystemExit(main())
