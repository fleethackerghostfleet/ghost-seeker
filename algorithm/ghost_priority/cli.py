"""Analyze once, rerank cached evidence, and compare offline payload budgets."""
from __future__ import annotations

import argparse
import hashlib
import html
import json
import math
import os
import platform
import sys
import time
from collections import Counter
from pathlib import Path

try:
    import resource
except ImportError:  # Windows does not expose the Unix resource module.
    resource = None

IMAGE_SUFFIXES = {'.jpg', '.jpeg', '.png', '.bmp', '.webp', '.tif', '.tiff'}


def read_jsonl(path):
    with Path(path).open() as f:
        return [json.loads(line) for line in f if line.strip()]


def write_json(path, data):
    Path(path).write_text(json.dumps(data, indent=2, allow_nan=False) + '\n')


def write_jsonl(path, rows):
    Path(path).write_text(''.join(json.dumps(r, allow_nan=False) + '\n' for r in rows))


def percentile(values, fraction):
    if not values:
        return None
    values = sorted(values)
    pos = (len(values) - 1) * fraction
    lo = int(pos)
    return values[lo] + (values[min(lo + 1, len(values)-1)] - values[lo]) * (pos - lo)


def image_items(args):
    import cv2
    if args.manifest:
        base = Path(args.manifest).resolve().parent
        rows = read_jsonl(args.manifest)
    else:
        base = Path(args.images).resolve()
        rows = [{'id': p.relative_to(base).as_posix(), 'path': str(p), 'source': p.parent.relative_to(base).as_posix()}
                for p in sorted(base.rglob('*')) if p.suffix.lower() in IMAGE_SUFFIXES]
    seen = set()
    for row in rows[:args.limit or None]:
        if not isinstance(row.get('id'), str) or not row['id'] or row['id'] in seen:
            raise ValueError('Manifest IDs must be nonempty unique strings')
        seen.add(row['id'])
        # Explicit whitelist: labels and annotations never enter the analyzer.
        path = Path(row['path'])
        path = path if path.is_absolute() else base / path
        started = time.perf_counter()
        bgr = cv2.imread(str(path), cv2.IMREAD_COLOR)
        elapsed = (time.perf_counter() - started) * 1000
        if bgr is None:
            raise ValueError(f'Cannot decode image: {path}')
        yield {'id': row['id'], 'source': str(row.get('source', 'images')),
               'timestamp_s': row.get('timestamp_s'), 'input_path': str(path.resolve()),
               'archive_member': row.get('archive_member')}, bgr, elapsed


def video_items(args):
    import cv2
    path = Path(args.video).resolve()
    cap = cv2.VideoCapture(str(path))
    if not cap.isOpened():
        raise ValueError(f'Cannot open video: {path}')
    fps = cap.get(cv2.CAP_PROP_FPS)
    if not math.isfinite(fps) or fps <= 0:
        cap.release()
        raise ValueError('Video has no usable FPS metadata; extract timestamped frames first')
    frame_idx, emitted, next_time, pending_ms = 0, 0, 0.0, 0.0
    try:
        while True:
            started = time.perf_counter()
            ok = cap.grab()
            pending_ms += (time.perf_counter() - started) * 1000
            if not ok:
                break
            timestamp = frame_idx / fps
            if timestamp + 1e-9 >= next_time:
                started = time.perf_counter()
                ok, bgr = cap.retrieve()
                pending_ms += (time.perf_counter() - started) * 1000
                if not ok:
                    raise ValueError(f'Video frame {frame_idx} could not be decoded')
                yield {'id': f'{path.stem}:{frame_idx}', 'source': path.stem,
                       'timestamp_s': timestamp, 'timestamp_basis': 'frame_index/container_fps_estimate',
                       'input_path': str(path), 'frame_index': frame_idx}, bgr, pending_ms
                emitted += 1
                pending_ms = 0.0
                next_time += 1 / min(args.fps, fps)
                if args.limit and emitted >= args.limit:
                    break
            frame_idx += 1
    finally:
        args.video_tail_decode_ms = pending_ms
        cap.release()


def analyze(args):
    import cv2
    import numpy as np
    from .analysis import Analyzer
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=False)
    (out / 'payloads').mkdir()
    started, cpu_started = time.perf_counter(), time.process_time()
    load_started = time.perf_counter()
    engine = Analyzer(args.model, width=args.analysis_size, threads=args.threads)
    init_ms = (time.perf_counter() - load_started) * 1000
    records = []
    iterator = video_items(args) if args.video else image_items(args)
    for meta, bgr, read_ms in iterator:
        before = time.perf_counter()
        evidence = engine.analyze(bgr)
        analyze_ms = (time.perf_counter() - before) * 1000
        h, w = bgr.shape[:2]
        before = time.perf_counter()
        scale = min(1.0, args.payload_size / max(h, w))
        payload = cv2.resize(bgr, (max(1, round(w*scale)), max(1, round(h*scale))), interpolation=cv2.INTER_AREA) if scale < 1 else bgr
        ok, encoded = cv2.imencode('.jpg', payload, [cv2.IMWRITE_JPEG_QUALITY, args.jpeg_quality])
        if not ok:
            raise ValueError(f'JPEG encoding failed for {meta["id"]}')
        encode_ms = (time.perf_counter() - before) * 1000
        filename = hashlib.sha256(meta['id'].encode()).hexdigest()[:24] + '.jpg'
        (out / 'payloads' / filename).write_bytes(encoded.tobytes())
        evidence['timing_ms'].update(read=read_ms, analyze_wall=analyze_ms, encode=encode_ms,
                                     total=read_ms+analyze_ms+encode_ms)
        records.append({**meta, **evidence, 'width': w, 'height': h,
                        'payload_path': 'payloads/' + filename, 'payload_bytes': int(encoded.nbytes)})
        if len(records) % 25 == 0:
            print(f'Analyzed {len(records)} images', flush=True)
    if not records:
        raise ValueError('No images were found')
    write_jsonl(out / 'records.jsonl', records)
    timings = {name: {'p50': percentile([r['timing_ms'][name] for r in records], .5),
                       'p95': percentile([r['timing_ms'][name] for r in records], .95)}
               for name in ('analysis', 'detector', 'read', 'encode', 'total')}
    rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss if resource is not None else None
    summary = {'images': len(records), 'eligible_images': sum(r.get('usable', True) for r in records),
               'sources': dict(Counter(r['source'] for r in records)), 'engine': records[0]['engine'],
               'python': platform.python_version(), 'platform': platform.platform(),
               'machine': platform.machine(), 'processor': platform.processor(),
               'opencv': cv2.__version__, 'numpy': np.__version__, 'opencv_threads': cv2.getNumThreads(),
               'requested_opencv_threads': args.threads,
               'analysis_size': args.analysis_size, 'payload_size': args.payload_size, 'jpeg_quality': args.jpeg_quality,
               'initialization_ms': init_ms, 'wall_seconds': time.perf_counter()-started,
               'cpu_seconds': time.process_time()-cpu_started,
               'video_tail_decode_ms': getattr(args, 'video_tail_decode_ms', 0.0),
               'peak_process_rss_mib': None if rss is None else rss / (1024**2 if sys.platform == 'darwin' else 1024),
               'timing_ms': timings,
               'notes': ['CPU measurements on this host, not an embedded-device benchmark.',
                         'Per-image total includes reads/decoding, analysis, and JPEG encoding; excludes payload writes and initialization.',
                         'Video read timings amortize skipped-frame grabs into the next emitted frame.',
                         'Any video decode tail after the last emitted frame is reported separately; it remains included in whole-run time.',
                         'Video timestamps are frame-index/FPS estimates for constant-frame-rate input, not authoritative VFR timestamps.',
                         'No GPU/NPU requested. OpenCL is disabled. OpenCV threads do not cap every runtime thread.',
                         'No accuracy claims without reviewed evaluation labels.']}
    write_json(out / 'analysis_summary.json', summary)
    print(json.dumps({'records': str(out/'records.jsonl'), 'images': len(records), 'timing_ms': timings}, indent=2))


def card(record, records_dir, report_dir):
    esc = html.escape
    src = esc(os.path.relpath(records_dir / record['payload_path'], report_dir), quote=True)
    dets = record.get('detections')
    description = 'Detector not run' if dets is None else ', '.join(f'{d["label"]} {d["confidence"]:.2f}' for d in dets) or 'No detections'
    boxes = ''
    image_width, image_height = int(record['width']), int(record['height'])
    for region in record.get('regions', []):
        x, y, w, h = (float(v) for v in region['bbox_xywh'])
        boxes += f'<rect x="{x}" y="{y}" width="{w}" height="{h}" class="candidate"/>'
    for det in dets or []:
        x, y, w, h = (float(v) for v in det['bbox_xywh'])
        boxes += f'<rect x="{x}" y="{y}" width="{w}" height="{h}" class="detector"/>'
    score = record.get('score')
    display = 'uniform' if score is None else f'{score:.3f}'
    reasons = esc(', '.join(record.get('reasons', [])))
    return f'''<article><div class="photo" style="aspect-ratio:{image_width}/{image_height}"><img src="{src}" loading="lazy"><svg viewBox="0 0 {image_width} {image_height}">{boxes}</svg></div><div class="body"><b>#{record['rank']} · {display}</b><span>{record['payload_bytes']/1024:.1f} KiB</span><p>{esc(record['source'])}</p><p>{esc(description)}</p><small>{reasons}</small><details><summary>Observation ID</summary>{esc(record['id'])}</details></div></article>'''


def render_report(out, records_dir, records, comparison, chosen, summary):
    rows = ''.join('<tr>' + ''.join(f'<td>{html.escape(str(x))}</td>' for x in [
        r['method'], r['budget_label'], r['selected_images'], r['selected_payload_bytes'],
        r['selected_sources'], 'Not evaluated' if r.get('reviewed_interesting_image_recall') is None else f"{r['reviewed_interesting_image_recall']:.3f}"]) + '</tr>' for r in comparison)
    cards = ''.join(card(r, records_dir, out) for r in chosen)
    page = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Ghost Seeker · CPU baseline</title><style>
    *{box-sizing:border-box}body{margin:0;background:#101923;color:#e3ebf4;font:15px system-ui;line-height:1.5}main{max-width:1300px;margin:auto;padding:36px 24px}h1{font-size:32px;margin-bottom:8px}h2{margin-top:32px}p{color:#b9c8d7}.badge{color:#65d8b5;font-size:12px;letter-spacing:1.5px}table{border-collapse:collapse;width:100%;background:#172432}td,th{text-align:left;padding:12px;border-bottom:1px solid #2b3c4e}.scroll{overflow:auto}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:18px}article{background:#172432;border:1px solid #314356;border-radius:9px;overflow:hidden}.photo{position:relative;max-height:420px;background:#080d13}.photo img{width:100%;height:100%;object-fit:fill}.photo svg{position:absolute;inset:0;width:100%;height:100%}rect{fill:none;stroke-width:2;vector-effect:non-scaling-stroke}.candidate{stroke:#f3c668;stroke-dasharray:5 4}.detector{stroke:#54d7dd}.body{padding:15px}.body span{float:right;color:#91a5b9}.body p{margin:6px 0;font-size:13px}.body small{color:#a5b9ce}details{font-size:11px;margin-top:8px;overflow-wrap:anywhere}code{color:#8bd8bf}pre{overflow:auto;background:#172432;padding:18px;border-radius:8px;font-size:12px}.notice{border-left:3px solid #f3c668;padding-left:15px}a{color:#80d6c3}</style><main>
    <div class="badge">GHOST SEEKER / ALGORITHM BASELINE</div><h1>What gets selected under a byte budget?</h1>
    <p class="notice">Experimental ranking, not validated object identification. This is an offline comparison of JPEG payload bytes, not a live-link simulation. Yellow dashed boxes are visual candidates; cyan boxes are model predictions. Scores are uncalibrated heuristics.</p>
    ''' + f'<p>{len(records)} observations · {summary["eligible_images"]} eligible · {summary["excluded_constant_images"]} constant images excluded equally from every baseline.</p>'
    if 'has_source_annotated_object' in summary.get('label_definitions', []):
        page += '<p class="notice">For this evaluation, relevant means the source annotation contains at least one object. This measures annotated-image coverage, not operator interest or detection accuracy. Empty annotation lists do not independently establish a physically empty scene.</p>'
    page += '<h2>Budget comparison</h2><div class="scroll"><table><thead><tr><th>Method</th><th>Budget</th><th>Images</th><th>Payload bytes</th><th>Sources represented</th><th>Reviewed interesting-image recall</th></tr></thead><tbody>' + rows + '</tbody></table></div>'
    page += f'<h2>Default selection: {html.escape(summary["gallery_budget_label"])}</h2><div class="grid">{cards}</div>'
    if not chosen:
        page += '<p>No complete image fits this byte budget.</p>'
    analysis_path = records_dir / 'analysis_summary.json'
    if analysis_path.exists():
        measurement = json.loads(analysis_path.read_text())
        page += '<h2>Measured CPU processing time on this host</h2><p>Milliseconds per observation. These timings do not predict another CPU. Initialization, payload writes, and batch ranking are separate.</p><div class="scroll"><table><thead><tr><th>Stage</th><th>p50 ms</th><th>p95 ms</th></tr></thead><tbody>'
        for name, stats in measurement['timing_ms'].items():
            page += f'<tr><td>{html.escape(name)}</td><td>{stats["p50"]:.2f}</td><td>{stats["p95"]:.2f}</td></tr>'
        page += '</tbody></table></div>'
    page += '<h2>Policy and measurement notes</h2><pre>' + html.escape(json.dumps(summary, indent=2)) + '</pre></main></html>'
    page = page.replace('object-fit:fill', 'object-fit:contain')
    (out / 'report.html').write_text(page)


def ranking_budgets(args, total_bytes):
    from .bandwidth import (BANDWIDTH_PRESETS_BPS, bandwidth_budget_bytes,
                            format_bandwidth)
    if args.fractions is not None:
        if args.window_seconds is not None:
            raise ValueError('--window-seconds requires a bandwidth budget, not --fractions')
        if any(not math.isfinite(x) or not 0 <= x <= 1 for x in args.fractions):
            raise ValueError('Fractions must be finite and between zero and one')
        if len({f'{x:.6f}' for x in args.fractions}) != len(args.fractions):
            raise ValueError('Fractions must be distinct at six-decimal precision')
        return [{'budget_mode': 'fraction', 'budget_fraction': x,
                 'budget_bytes': int(total_bytes * x), 'selection_key': f'{x:.6f}',
                 'budget_label': f'{x * 100:g}% of eligible JPEG bytes ({int(total_bytes * x):,} bytes)'}
                for x in args.fractions]
    from .bandwidth import DEFAULT_WINDOW_SECONDS
    preset = args.bandwidth_preset or ('normal' if args.bandwidth_bps is None else None)
    bps = BANDWIDTH_PRESETS_BPS[preset] if preset else args.bandwidth_bps
    seconds = DEFAULT_WINDOW_SECONDS if args.window_seconds is None else args.window_seconds
    budget = bandwidth_budget_bytes(bps, seconds)
    return [{'budget_mode': 'bandwidth', 'available_bandwidth_bps': bps,
             'window_seconds': seconds, 'bandwidth_preset': preset,
             'budget_bytes': budget, 'budget_fraction': budget / total_bytes if total_bytes else None,
             'selection_key': 'bandwidth',
             'budget_label': f'{format_bandwidth(bps)} × {seconds:g} s ({budget:,} bytes)'}]


def rank(args):
    from .policy import load_policy, select_records
    from .evaluation import evaluate_selection
    records_path = Path(args.records).resolve()
    records = read_jsonl(records_path)
    if not records:
        raise ValueError('Records are empty')
    policy = load_policy(args.policy)
    labels = json.loads(Path(args.labels).read_text()) if args.labels else None
    if labels is not None and not isinstance(labels, dict):
        raise ValueError('Labels must map observation ID to reviewed annotation')
    eligible = [r for r in records if r.get('usable', True)]
    total_bytes = sum(r['payload_bytes'] for r in eligible)
    budgets = ranking_budgets(args, total_bytes)
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=False)
    methods = ['uniform', 'novelty', 'priority']
    if eligible and all(r.get('detections') is not None for r in eligible):
        methods.insert(2, 'detector')
    comparison, selected_by_key = [], {}
    for spec in budgets:
        budget = spec['budget_bytes']
        for method in methods:
            selection_start = time.perf_counter()
            selected = select_records(eligible, budget, policy, method=method)
            selection_ms = (time.perf_counter()-selection_start)*1000
            metrics = evaluate_selection(eligible, selected, labels)
            row = {'method': method, **spec,
                   'selection_ms': selection_ms,
                   'selected_images': len(selected), 'selected_payload_bytes': sum(r['payload_bytes'] for r in selected),
                   'selected_sources': len({r['source'] for r in selected}),
                   'source_counts': dict(Counter(r['source'] for r in selected)), 'evaluation': metrics}
            if 'reviewed_interesting_image_recall' in metrics:
                row['reviewed_interesting_image_recall'] = metrics['reviewed_interesting_image_recall']
            comparison.append(row)
            row['unused_budget_bytes'] = budget - row['selected_payload_bytes']
            selected_by_key[(method, spec['selection_key'])] = selected
            write_jsonl(out / f'{method}-{spec["selection_key"]}.jsonl', selected)
    gallery = max(budgets, key=lambda spec: spec['budget_bytes'])
    summary = {'eligible_images': len(eligible), 'excluded_constant_images': len(records)-len(eligible),
               'reference_payload_bytes': total_bytes, 'gallery_fraction': gallery['budget_fraction'],
               'gallery_budget_label': gallery['budget_label'], 'budgets': budgets, 'policy': policy,
               'labels_supplied': labels is not None,
               'label_definitions': sorted({str(v['interest_definition']) for v in (labels or {}).values() if 'interest_definition' in v}),
               'notes': [
                   'Byte denominator is identical JPEG representations of eligible inputs, not raw source size.',
                   'Payload sizes exclude metadata, protocol overhead, retransmissions, and source video bitrate.',
                   'Bandwidth is effective outbound JPEG-payload goodput allocated to this application, in decimal bits/s.',
                   'Bandwidth presets are illustrative scenarios, not destroyer link specifications.',
                   'Rate × window / 8 is an offline budget; no packet pacing, changing-rate trace, outages, or latency are simulated.',
                   'Ranking uses cached evidence; the --labels file is used only after selection for evaluation.',
                   'Label metrics measure image-ID coverage, not whether annotated details remain visible after JPEG resizing/compression.',
                   'Sampled stills have no inferred video event identities, motion, persistence, or deadlines.',
                   'Diversity is a coarse image-similarity heuristic; small meaningful changes can be missed.',
                   'Representative coverage is a soft allocation limited by indivisible image sizes.',
                   'UI subtypes and behavior attributes are unsupported by generic COCO predictions.']}
    write_json(out / 'comparison.json', comparison)
    write_json(out / 'selection_summary.json', summary)
    chosen = selected_by_key[('priority', gallery['selection_key'])]
    render_report(out, records_path.parent, records, comparison, chosen, summary)
    print(json.dumps({'report': str(out/'report.html'), 'comparison': comparison}, indent=2))


def positive_int(value):
    x = int(value)
    if x <= 0:
        raise argparse.ArgumentTypeError('Must be positive')
    return x


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    a = sub.add_parser('analyze', help='Extract evidence and encode a common JPEG representation')
    source = a.add_mutually_exclusive_group(required=True)
    source.add_argument('--manifest', help='JSONL with id, path, source and optional timestamp_s')
    source.add_argument('--images', help='Recursively read a directory of independent images')
    source.add_argument('--video', help='Sample a video; timestamp_s derives from container FPS')
    a.add_argument('--out', required=True, help='New output directory')
    a.add_argument('--model', help='Optional OpenCV Zoo 2022nov FP32 NanoDet ONNX')
    a.add_argument('--threads', type=int, default=0, help='OpenCV thread request; 0 disables parallel regions (default)')
    a.add_argument('--analysis-size', type=positive_int, default=320)
    a.add_argument('--payload-size', type=positive_int, default=1280)
    a.add_argument('--jpeg-quality', type=int, choices=range(1, 101), default=80, metavar='1..100')
    a.add_argument('--fps', type=float, default=1.0)
    a.add_argument('--limit', type=int, default=0, help='Maximum images/frames; 0 means all')
    a.set_defaults(func=analyze)
    r = sub.add_parser('rank', help='Compare selection at fixed offline byte budgets')
    r.add_argument('--records', required=True)
    r.add_argument('--out', required=True, help='New output directory')
    r.add_argument('--policy', help='JSON overrides; no inference required')
    r.add_argument('--labels', help='Optional evaluation-only JSON mapping IDs to reviewed annotations')
    from .bandwidth import BANDWIDTH_PRESETS_BPS
    budget = r.add_mutually_exclusive_group()
    budget.add_argument('--bandwidth-bps', type=int,
                        help='Effective outbound payload bits/s allocated to this service; 0 means disconnected')
    budget.add_argument('--bandwidth-preset', choices=BANDWIDTH_PRESETS_BPS,
                        help='Illustrative connectivity scenario (default: normal, 2000000 bps)')
    budget.add_argument('--fractions', type=float, nargs='+',
                        help='Legacy relative budgets, e.g. 0.01 0.05 0.10; replaces bandwidth mode')
    r.add_argument('--window-seconds', type=float,
                   help='Transmission budget window in seconds (default: 60); bandwidth mode only')
    r.set_defaults(func=rank)
    args = parser.parse_args()
    if args.command == 'analyze' and (args.limit < 0 or not math.isfinite(args.fps) or args.fps <= 0):
        parser.error('--limit must be nonnegative and --fps finite and positive')
    try:
        args.func(args)
    except (ValueError, FileNotFoundError, FileExistsError, KeyError) as exc:
        parser.exit(2, f'Error: {exc}\n')
