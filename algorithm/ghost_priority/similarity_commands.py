"""Explicit cache migration and offline similarity-review commands."""
import copy
import html
import json
from pathlib import Path
import time


def cache_similarity(args):
    import cv2
    from .cli import read_jsonl, write_json, write_jsonl
    from .similarity_features import describe
    from .policy import _validate_ids
    path = Path(args.records).resolve()
    out = Path(args.out).resolve()
    if out.exists():
        raise FileExistsError(out)
    records = _validate_ids(read_jsonl(path))
    if not records:
        raise ValueError('Records are empty')
    if any('priority_rank' in r or 'selection_rank' in r for r in records):
        raise ValueError('cache-similarity accepts analysis evidence, not saved rankings/selections; rerank after migration')
    cv2.setNumThreads(0)
    cv2.ocl.setUseOpenCL(False)
    updated = []
    started = time.perf_counter()
    for record in records:
        raw = record.get('input_path')
        if not isinstance(raw, str) or not raw:
            raise ValueError(f"{record['id']}: original input_path is required; cached payload JPEGs are not equivalent source pixels")
        source = (path.parent / raw).resolve()
        if 'frame_index' in record:
            index = record['frame_index']
            if isinstance(index, bool) or not isinstance(index, int) or index < 0:
                raise ValueError('frame_index must be a nonnegative integer')
            cap = cv2.VideoCapture(str(source))
            try:
                if not cap.isOpened() or not cap.set(cv2.CAP_PROP_POS_FRAMES, index):
                    raise ValueError(f'Cannot seek original video frame: {source}:{index}')
                ok, image = cap.read()
                if not ok:
                    image = None
            finally:
                cap.release()
        else:
            image = cv2.imread(str(source), cv2.IMREAD_COLOR)
        if image is None:
            raise ValueError(f'Cannot decode original input: {source}')
        h, w = image.shape[:2]
        if record.get('width', w) != w or record.get('height', h) != h:
            raise ValueError(f"{record['id']}: original dimensions changed")
        color, phash = describe(image, args.analysis_size)
        if color != record.get('fingerprint'):
            raise ValueError(f"{record['id']}: original color fingerprint does not match; verify source pixels and --analysis-size")
        row = copy.deepcopy(record)
        row['phash'] = phash
        row['input_path'] = str(source)
        if 'payload_path' in row:
            row['payload_path'] = str((path.parent / row['payload_path']).resolve())
        updated.append(row)
    elapsed = (time.perf_counter() - started) * 1000
    out.mkdir(parents=True, exist_ok=False)
    write_jsonl(out/'records.jsonl', updated)
    write_json(out/'similarity_cache_summary.json', {
        'records': len(updated), 'source_records': str(path), 'analysis_size': args.analysis_size,
        'elapsed_ms': elapsed, 'descriptor_version': updated[0]['phash']['version'],
        'notes': ['Original color fingerprints verified exactly; not a cryptographic source-integrity check.',
                  'Only pHash added/rebuilt and file paths made absolute. Signals, detections, quality, payload bytes and existing timings preserved.',
                  'No object/visual/quality providers called. Existing analysis timings describe the original analysis.']})
    print(json.dumps({'records': str(out/'records.jsonl'), 'images': len(updated), 'elapsed_ms': elapsed}, indent=2))


def evaluate_similarity(args):
    from .cli import read_jsonl, write_json
    from .policy import load_policy
    from .similarity_evaluation import evaluate_pairs
    out = Path(args.out).resolve()
    if out.exists():
        raise FileExistsError(out)
    policy = load_policy(args.policy)
    report = evaluate_pairs(read_jsonl(args.records), json.loads(Path(args.pairs).read_text()),
                            args.thresholds, policy['duplicate_distance'], policy['phash_distance_scale'])
    rows = ''
    for row in report['metrics']:
        values = [row['group'] or 'All groups', row['method'], row['threshold'],
                  f"{row['duplicate_candidates']} / {row['same_information_pairs']}",
                  f"{row['false_suppression_candidates']} / {row['changed_information_pairs']}",
                  row['uncertain_candidates'], row['unavailable_pairs']]
        rows += '<tr>' + ''.join('<td>'+html.escape(str(v))+'</td>' for v in values) + '</tr>'
    pairs = ''
    for pair in report['pairs']:
        scores = ' · '.join(f"{k}: {v['score']:.4f}" for k, v in pair['matches'].items())
        pairs += '<tr>' + ''.join('<td>'+html.escape(str(v))+'</td>' for v in
                                  (pair['a'], pair['b'], pair['label'], pair['group'], scores, pair.get('note', ''))) + '</tr>'
    page = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Similarity pair review</title><style>body{font:15px system-ui;margin:32px;color:#172638;background:#f5f8fb}table{border-collapse:collapse;margin:24px 0;width:100%}td,th{padding:10px;text-align:left;border:1px solid #ccd6df}td{overflow-wrap:anywhere}h1{margin-bottom:12px}.scroll{overflow:auto}p{max-width:950px}</style><h1>Similarity pair review</h1><p>Higher scores indicate greater appearance redundancy. Thresholds below are evaluation probes only; ranking uses continuous soft penalties. A changed pair crossing a threshold is a potential false suppression. Uncertain pairs are excluded from positive/negative rates. These are not transmitted-image accuracy measurements.</p><p><a href="similarity_evaluation.json">Complete measurements</a></p><div class="scroll"><table><tr><th>Group</th><th>Method</th><th>Threshold</th><th>Duplicate candidates / same pairs</th><th>False suppression candidates / changed pairs</th><th>Uncertain candidates</th><th>Unavailable</th></tr>''' + rows + '''</table></div><h2>Reviewed pairs</h2><div class="scroll"><table><tr><th>Image A</th><th>Image B</th><th>Label</th><th>Group</th><th>Scores</th><th>Review note</th></tr>''' + pairs + '</table></div></html>'
    out.mkdir(parents=True, exist_ok=False)
    write_json(out/'similarity_evaluation.json', report)
    (out/'report.html').write_text(page)
    print(json.dumps({'pairs': report['pair_count'], 'report': str(out/'report.html')}, indent=2))
