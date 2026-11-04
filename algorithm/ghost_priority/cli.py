"""Plug in signal providers, rank cached evidence, then select by bandwidth."""
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
    from .providers import load_provider
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=False)
    (out / 'payloads').mkdir()
    started, cpu_started = time.perf_counter(), time.process_time()
    load_started = time.perf_counter()
    engine = Analyzer(args.model, width=args.analysis_size, threads=args.threads,
                      object_provider=load_provider(args.object_provider) if args.object_provider else None,
                      visual_provider=load_provider(args.visual_provider) if args.visual_provider else None,
                      quality_provider=load_provider(args.quality_provider) if args.quality_provider else None)
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
               for name in ('analysis', 'detector', 'similarity_features', 'read', 'encode', 'total')}
    rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss if resource is not None else None
    summary = {'images': len(records), 'eligible_images': sum(r.get('usable', True) for r in records),
               'sources': dict(Counter(r['source'] for r in records)), 'engine': records[0]['engine'],
               'providers': records[0].get('providers', {}),
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
                         'Built-in providers request CPU execution and OpenCL is disabled. Custom providers control their own runtime; OpenCV settings do not constrain other runtimes.',
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
    score = record.get('final_score', record.get('score'))
    display = 'uniform' if score is None else f'{score:.3f}'
    reasons = esc(', '.join(record.get('reasons', [])))
    selected = f" · selection #{record['selection_rank']}" if 'selection_rank' in record else ''
    priority = record.get('priority_rank', record.get('rank'))
    base = record.get('base_score', record.get('score'))
    details = '' if base is None else f'<p>Base {base:.3f} · final {display} · similarity {record.get("max_similarity", 0):.3f}</p>'
    if record.get('object_relevance') is not None:
        description = f"Provider relevance {record['object_relevance']:.3f}; " + description
    match = record.get('similarity_match')
    if match:
        components = []
        if match.get('color_similarity') is not None:
            components.append(f"color {match['color_similarity']:.3f}")
        if match.get('phash_distance_bits') is not None:
            components.append(f"pHash {match['phash_distance_bits']}/64 differing bits")
        details += '<p>' + html.escape(' · '.join(components)) + '</p>'
    return f'''<article><div class="photo" style="aspect-ratio:{image_width}/{image_height}"><img src="{src}" loading="lazy"><svg viewBox="0 0 {image_width} {image_height}">{boxes}</svg></div><div class="body"><b>Priority #{priority}{selected}</b><span>{record['payload_bytes']/1000:.1f} KB</span>{details}<p>{esc(record['source'])}</p><p>{esc(description)}</p><small>{reasons}</small><details><summary>Observation ID</summary>{esc(record['id'])}</details></div></article>'''


def render_report(out, records_dir, records, comparison, chosen, summary):
    rows = ''.join('<tr>' + ''.join(f'<td>{html.escape(str(x))}</td>' for x in [
        r['method'], r['budget_label'], r['selected_images'], r['selected_payload_bytes'],
        r['selected_sources'], 'Not evaluated' if r.get('reviewed_interesting_image_recall') is None else f"{r['reviewed_interesting_image_recall']:.3f}"]) + '</tr>' for r in comparison)
    cards = ''.join(card(r, records_dir, out) for r in chosen)
    page = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Ghost Seeker · CPU baseline</title><style>
    *{box-sizing:border-box}body{margin:0;background:#101923;color:#e3ebf4;font:15px system-ui;line-height:1.5}main{max-width:1300px;margin:auto;padding:36px 24px}h1{font-size:32px;margin-bottom:8px}h2{margin-top:32px}p{color:#b9c8d7}.badge{color:#65d8b5;font-size:12px;letter-spacing:1.5px}table{border-collapse:collapse;width:100%;background:#172432}td,th{text-align:left;padding:12px;border-bottom:1px solid #2b3c4e}.scroll{overflow:auto}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:18px}article{background:#172432;border:1px solid #314356;border-radius:9px;overflow:hidden}.photo{position:relative;max-height:420px;background:#080d13}.photo img{width:100%;height:100%;object-fit:fill}.photo svg{position:absolute;inset:0;width:100%;height:100%}rect{fill:none;stroke-width:2;vector-effect:non-scaling-stroke}.candidate{stroke:#f3c668;stroke-dasharray:5 4}.detector{stroke:#54d7dd}.body{padding:15px}.body span{float:right;color:#91a5b9}.body p{margin:6px 0;font-size:13px}.body small{color:#a5b9ce}details{font-size:11px;margin-top:8px;overflow-wrap:anywhere}code{color:#8bd8bf}pre{overflow:auto;background:#172432;padding:18px;border-radius:8px;font-size:12px}.notice{border-left:3px solid #f3c668;padding-left:15px}a{color:#80d6c3}</style><main>
    <div class="badge">GHOST SEEKER / ALGORITHM BASELINE</div><h1>Image priority and bandwidth selection</h1>
    <p class="notice">A full ranking is built from object relevance, visual interest, quality and similarity. Bandwidth selection walks that fixed order without changing its scores. This is an offline experiment, not a live-link simulation. Yellow dashed boxes are visual candidates; cyan boxes are model predictions. Scores are uncalibrated heuristics.</p>
    ''' + f'<p>{len(records)} observations · {summary["eligible_images"]} eligible · {summary["excluded_constant_images"]} constant images excluded equally from every baseline.</p>'
    if 'has_source_annotated_object' in summary.get('label_definitions', []):
        page += '<p class="notice">For this evaluation, relevant means the source annotation contains at least one object. This measures annotated-image coverage, not operator interest or detection accuracy. Empty annotation lists do not independently establish a physically empty scene.</p>'
    if comparison:
        page += '<h2>Selection under absolute allowances</h2><div class="scroll"><table><thead><tr><th>Method</th><th>Budget</th><th>Images</th><th>Payload bytes</th><th>Sources represented</th><th>Reviewed interesting-image recall</th></tr></thead><tbody>' + rows + '</tbody></table></div>'
    if summary.get('ranking_files'):
        page += '<p>Complete rankings: ' + ' · '.join(f'<a href="{html.escape(path, quote=True)}">{html.escape(method)}</a>' for method, path in summary['ranking_files'].items()) + '</p>'
    page += f'<h2>{html.escape(summary["gallery_budget_label"])}</h2><div class="grid">{cards}</div>'
    if not chosen:
        page += '<p>No eligible images.</p>' if summary.get('mode') == 'ranking_only' else '<p>No complete image fits this byte budget.</p>'
    analysis_path = records_dir / 'analysis_summary.json'
    if analysis_path.exists():
        measurement = json.loads(analysis_path.read_text())
        page += '<h2>Measured analysis time on this host</h2><p>Wall-clock milliseconds per observation; built-in providers use the CPU, while custom providers control their runtime. Initialization, payload writes, and batch ranking are separate.</p><div class="scroll"><table><thead><tr><th>Stage</th><th>p50 ms</th><th>p95 ms</th></tr></thead><tbody>'
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


def read_labels(path):
    labels = json.loads(Path(path).read_text()) if path else None
    if labels is not None and (not isinstance(labels, dict) or any(not isinstance(v, dict) for v in labels.values())):
        raise ValueError('Labels must map observation ID to reviewed annotation objects')
    return labels


def selection_metrics(records, selected, method, spec, labels, selection_ms, ranking_ms=None):
    from .evaluation import evaluate_selection
    metrics = evaluate_selection(records, selected, labels)
    spent = sum(r['payload_bytes'] for r in selected)
    return {'method': method, **spec, 'ranking_ms': ranking_ms, 'selection_ms': selection_ms,
            'selected_images': len(selected), 'selected_payload_bytes': spent,
            'unused_budget_bytes': spec['budget_bytes'] - spent,
            'selected_sources': len({r['source'] for r in selected}),
            'source_counts': dict(Counter(r['source'] for r in selected)),
            'evaluation': metrics,
            'reviewed_interesting_image_recall': metrics['reviewed_interesting_image_recall']}


def summary_notes():
    return [
        'Object, visual-interest and quality providers produce cached evidence; ranking imports no image model.',
        'Full priority ranking includes soft similarity penalties against earlier ranked entries; byte size and bandwidth do not change it.',
        'Selection walks the supplied full ranking, skips payloads that do not fit, and preserves priority ranks and scores.',
        'ranking_ms and selection_ms time separate stages; selecting a saved ranking does not repeat inference or ranking.',
        'Payload sizes exclude metadata, protocol overhead, retransmissions, and source video bitrate.',
        'Bandwidth is effective outbound JPEG-payload goodput allocated to this application, in decimal bits/s.',
        'Bandwidth presets are illustrative scenarios, not destroyer link specifications.',
        'Rate × window / 8 is an offline budget; no packet pacing, changing-rate trace, outages, or latency are simulated.',
        'Labels are used only after selection for evaluation, never by providers or ranking.',
        'Label metrics measure image-ID coverage, not whether annotated details remain visible after JPEG resizing/compression.',
        'Sampled stills have no inferred video event identities, motion, persistence, or deadlines.',
        'Similarity is a coarse appearance heuristic; small meaningful changes can be missed.',
        'Generic detector classes do not establish vessel subtypes or behavior attributes.']


def rank(args):
    from .policy import load_policy, rank_records
    from .selection import select_ranked
    records_path = Path(args.records).resolve()
    records = read_jsonl(records_path)
    if not records:
        raise ValueError('Records are empty')
    policy = load_policy(args.policy)
    labels = read_labels(args.labels)
    eligible = [r for r in records if r.get('usable', True)]
    total_bytes = sum(r['payload_bytes'] for r in eligible)
    if args.ranking_only and any(getattr(args, key) is not None for key in
                                 ('bandwidth_bps', 'bandwidth_preset', 'window_seconds', 'fractions')):
        raise ValueError('--ranking-only does not accept bandwidth, window or fraction settings')
    if args.ranking_only and labels is not None:
        raise ValueError('--labels evaluates a selection; use it with select or omit --ranking-only')
    budgets = [] if args.ranking_only else ranking_budgets(args, total_bytes)
    out = Path(args.out).resolve()
    methods = ['uniform', 'novelty', 'priority']
    if eligible and all(r.get('object_relevance') is not None or r.get('detections') is not None for r in eligible):
        methods.insert(2, 'detector')
    rankings, ranking_times, ranking_files = {}, {}, {}
    for method in methods:
        started = time.perf_counter()
        rankings[method] = rank_records(records, policy, method=method)
        ranking_times[method] = (time.perf_counter()-started)*1000
        # Persist source-resolved paths so a separate select command can find the
        # same encoded payloads without knowing the original evidence directory.
        for record in rankings[method]:
            record['payload_path'] = str((records_path.parent / record['payload_path']).resolve())
        ranking_files[method] = f'ranked-{method}.jsonl'
    out.mkdir(parents=True, exist_ok=False)
    for method, rows in rankings.items():
        write_jsonl(out / ranking_files[method], rows)
    comparison, selected_by_key = [], {}
    for spec in budgets:
        for method in methods:
            selection_start = time.perf_counter()
            selected = select_ranked(rankings[method], spec['budget_bytes'])
            selection_ms = (time.perf_counter()-selection_start)*1000
            comparison.append(selection_metrics(eligible, selected, method, spec, labels, selection_ms, ranking_times[method]))
            selected_by_key[(method, spec['selection_key'])] = selected
            write_jsonl(out / f'{method}-{spec["selection_key"]}.jsonl', selected)
    gallery = max(budgets, key=lambda spec: spec['budget_bytes']) if budgets else None
    summary = {'mode': 'ranking_only' if args.ranking_only else 'rank_and_select',
               'eligible_images': len(eligible), 'excluded_constant_images': len(records)-len(eligible),
               'reference_payload_bytes': total_bytes,
               'gallery_budget_label': 'Full priority ranking — no bandwidth cutoff' if gallery is None else gallery['budget_label'],
               'budgets': budgets, 'policy': policy, 'ranking_files': ranking_files, 'ranking_ms': ranking_times,
               'labels_supplied': labels is not None,
               'label_definitions': sorted({str(v['interest_definition']) for v in (labels or {}).values() if 'interest_definition' in v}),
               'notes': summary_notes()}
    write_json(out / 'ranking_summary.json', summary)
    if not args.ranking_only:
        write_json(out / 'comparison.json', comparison)
        write_json(out / 'selection_summary.json', summary)
    chosen = rankings['priority'] if gallery is None else selected_by_key[('priority', gallery['selection_key'])]
    render_report(out, records_path.parent, records, comparison, chosen, summary)
    print(json.dumps({'report': str(out/'report.html'), 'ranking_files': ranking_files, 'comparison': comparison}, indent=2))


def select(args):
    from .selection import select_ranked
    path = Path(args.ranked).resolve()
    records = read_jsonl(path)
    labels = read_labels(args.labels)
    total_bytes = sum(r['payload_bytes'] for r in records)
    spec = ranking_budgets(args, total_bytes)[0]
    started = time.perf_counter()
    selected = select_ranked(records, spec['budget_bytes'])
    elapsed = (time.perf_counter()-started)*1000
    methods = {r.get('ranking_method', 'priority') for r in records}
    if len(methods) > 1:
        raise ValueError('The saved ranking must use a single ranking method')
    method = next(iter(methods), 'priority')
    row = selection_metrics(records, selected, method, spec, labels, elapsed)
    summary = {'mode': 'selection_only', 'ranked_source': str(path),
               'eligible_images': len(records), 'excluded_constant_images': 0,
               'reference_payload_bytes': total_bytes, 'gallery_budget_label': spec['budget_label'],
               'budgets': [spec], 'ranking_ms': None, 'selection_ms': elapsed,
               'labels_supplied': labels is not None,
               'label_definitions': sorted({str(v['interest_definition']) for v in (labels or {}).values() if 'interest_definition' in v}),
               'notes': summary_notes()}
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=False)
    write_jsonl(out / 'selected.jsonl', selected)
    write_json(out / 'comparison.json', [row])
    write_json(out / 'selection_summary.json', summary)
    render_report(out, path.parent, records, [row], selected, summary)
    print(json.dumps({'report': str(out/'report.html'), 'selected_images': len(selected),
                      'budget_bytes': spec['budget_bytes'], 'selected_payload_bytes': row['selected_payload_bytes']}, indent=2))


def positive_int(value):
    x = int(value)
    if x <= 0:
        raise argparse.ArgumentTypeError('Must be positive')
    return x


def add_bandwidth_arguments(parser, allow_fractions=False):
    from .bandwidth import BANDWIDTH_PRESETS_BPS
    budget = parser.add_mutually_exclusive_group()
    budget.add_argument('--bandwidth-bps', type=int,
                        help='Effective outbound payload bits/s allocated to this service; 0 means disconnected')
    budget.add_argument('--bandwidth-preset', choices=BANDWIDTH_PRESETS_BPS,
                        help='Illustrative connectivity scenario (default: normal, 2000000 bps)')
    if allow_fractions:
        budget.add_argument('--fractions', type=float, nargs='+',
                            help='Legacy relative budgets, e.g. 0.01 0.05 0.10; replaces bandwidth mode')
    else:
        parser.set_defaults(fractions=None)
    parser.add_argument('--window-seconds', type=float,
                        help='Transmission budget window in seconds (default: 60); bandwidth mode only')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    from .similarity_commands import cache_similarity, evaluate_similarity
    c = sub.add_parser('cache-similarity', help='Add pHash to old evidence from original inputs without model inference')
    c.add_argument('--records', required=True)
    c.add_argument('--out', required=True, help='New output directory')
    c.add_argument('--analysis-size', type=positive_int, default=320, help='Must match original fingerprint preprocessing')
    c.set_defaults(func=cache_similarity)
    e = sub.add_parser('evaluate-similarity', help='Review duplicate candidates and false suppression candidates separately')
    e.add_argument('--records', required=True)
    e.add_argument('--pairs', required=True, help='JSON list of a, b, label and optional group/note')
    e.add_argument('--out', required=True, help='New output directory')
    e.add_argument('--thresholds', type=float, nargs='+', default=[.25, .5, .75, .9])
    e.add_argument('--policy', help='Optional metric scales; does not tune or write production policy')
    e.set_defaults(func=evaluate_similarity)
    a = sub.add_parser('analyze', help='Extract evidence and encode a common JPEG representation')
    source = a.add_mutually_exclusive_group(required=True)
    source.add_argument('--manifest', help='JSONL with id, path, source and optional timestamp_s')
    source.add_argument('--images', help='Recursively read a directory of independent images')
    source.add_argument('--video', help='Sample a video; timestamp_s derives from container FPS')
    a.add_argument('--out', required=True, help='New output directory')
    object_engine = a.add_mutually_exclusive_group()
    object_engine.add_argument('--model', help='Optional OpenCV Zoo 2022nov FP32 NanoDet ONNX')
    object_engine.add_argument('--object-provider', help='Custom object provider: module:factory (no-argument factory)')
    a.add_argument('--visual-provider', help='Custom visual-interest provider: module:factory')
    a.add_argument('--quality-provider', help='Custom image-quality provider: module:factory')
    a.add_argument('--threads', type=int, default=0, help='OpenCV thread request; 0 disables parallel regions (default)')
    a.add_argument('--analysis-size', type=positive_int, default=320)
    a.add_argument('--payload-size', type=positive_int, default=1280)
    a.add_argument('--jpeg-quality', type=int, choices=range(1, 101), default=80, metavar='1..100')
    a.add_argument('--fps', type=float, default=1.0)
    a.add_argument('--limit', type=int, default=0, help='Maximum images/frames; 0 means all')
    a.set_defaults(func=analyze)
    r = sub.add_parser('rank', help='Build full rankings; optionally compare bandwidth selections')
    r.add_argument('--records', required=True)
    r.add_argument('--out', required=True, help='New output directory')
    r.add_argument('--policy', help='JSON overrides; no inference required')
    r.add_argument('--labels', help='Optional evaluation-only JSON mapping IDs to reviewed annotations')
    r.add_argument('--ranking-only', action='store_true', help='Save complete rankings and gallery without applying bandwidth')
    add_bandwidth_arguments(r, allow_fractions=True)
    r.set_defaults(func=rank)
    s = sub.add_parser('select', help='Select from a saved full ranking without inference or rescoring')
    s.add_argument('--ranked', required=True, help='Full ranked JSONL, e.g. ranked-priority.jsonl')
    s.add_argument('--out', required=True, help='New output directory')
    s.add_argument('--labels', help='Optional evaluation-only labels')
    add_bandwidth_arguments(s)
    s.set_defaults(func=select)
    args = parser.parse_args()
    if args.command == 'analyze' and (args.limit < 0 or not math.isfinite(args.fps) or args.fps <= 0):
        parser.error('--limit must be nonnegative and --fps finite and positive')
    try:
        args.func(args)
    except (ValueError, FileNotFoundError, FileExistsError, KeyError) as exc:
        parser.exit(2, f'Error: {exc}\n')
