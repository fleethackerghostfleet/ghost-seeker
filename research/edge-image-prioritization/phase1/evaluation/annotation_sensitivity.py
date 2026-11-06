#!/usr/bin/env python3
"""Post-hoc annotation-status strata from saved provenance and selection sets only.

Never loads images, raw GT, ranking code or candidate code. No tuning, physical-
identity merging or changes to the original metric sets are performed.
"""
import argparse
import csv
import hashlib
import json
from collections import defaultdict
from pathlib import Path


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def annotation_metadata(provenance):
    result = {}
    for item in provenance['inputs']:
        if 'sequence' not in item:
            continue
        sid = item['sequence']
        if sid in result:
            raise ValueError(f'Duplicate sequence provenance: {sid}')
        meta, labels = item.get('metadata', {}), item.get('labelinfo', {})
        qa = labels.get('qa') or {}
        index_status, qa_status = meta.get('gt_status'), qa.get('gt_status')
        status = str(qa_status or index_status or 'unknown').strip().lower()
        source = labels.get('label_source') or meta.get('label_source') or 'unknown'
        source = str(source)
        group = ('complete' if status == 'complete' else
                 'partial_or_incomplete' if status in ('partial', 'incomplete') else 'unknown_or_other')
        result[sid] = {
            'sequence': sid, 'source_group': item.get('source_group') or meta.get('upstream') or sid,
            'split': meta.get('split', 'unknown'), 'gt_status': status, 'gt_status_group': group,
            'label_source': source, 'index_gt_status': index_status, 'qa_gt_status': qa_status,
            'status_conflict': bool(index_status and qa_status and str(index_status).lower() != str(qa_status).lower()),
            'metadata_label_source': meta.get('label_source'), 'labels_label_source': labels.get('label_source'),
            'label_source_conflict': bool(meta.get('label_source') and labels.get('label_source') and meta['label_source'] != labels['label_source']),
            'qa_reviewed': qa.get('reviewed'), 'qa_same_object_groups': qa.get('same_object_groups', []),
            'qa_existing_merges': qa.get('merge', {}), 'qa_notes': qa.get('notes'), 'metadata_notes': meta.get('notes'),
        }
    return result


def strata(meta):
    return [('all', 'all'), ('gt_status', meta['gt_status']),
            ('gt_status_group', meta['gt_status_group']), ('label_source', meta['label_source']),
            ('gt_status_group_by_label_source', json.dumps([meta['gt_status_group'], meta['label_source']]))]


def summarize(label, provenance, rows):
    metadata = annotation_metadata(provenance)
    groups, keys = defaultdict(list), set()
    for row in rows:
        key = row['sequence'], row['window'], row['method'], row['bandwidth_bps']
        if key in keys:
            raise ValueError(f'Duplicate metric row: {key}')
        keys.add(key)
        meta = metadata[row['sequence']]
        if row['source_group'] != meta['source_group']:
            raise ValueError(f'Source group differs from provenance: {row["sequence"]}')
        if not set(row['selected_positive_tracks']) <= set(row['sampled_positive_tracks']) <= set(row['all_positive_tracks']):
            raise ValueError(f'Coverage sets not nested: {key}')
        for axis, value in strata(meta):
            groups[axis, value, row['method'], row['bandwidth_bps']].append(row)
    output = []
    for (axis, value, method, bps), subset in sorted(groups.items()):
        whole = {(r['sequence'], t) for r in subset for t in r['all_positive_tracks']}
        selected_whole = {(r['sequence'], t) for r in subset for t in r['selected_positive_tracks']}
        num = sum(len(r['selected_positive_tracks']) for r in subset)
        den = sum(len(r['all_positive_tracks']) for r in subset)
        sampled_den = sum(len(r['sampled_positive_tracks']) for r in subset)
        output.append({'run': label, 'stratum_axis': axis, 'stratum': value, 'method': method,
            'bandwidth_bps': bps, 'sequence_count': len({r['sequence'] for r in subset}),
            'source_group_count': len({r['source_group'] for r in subset}),
            'positive_source_group_count': len({r['source_group'] for r in subset if r['all_positive_tracks']}),
            'window_count': len(subset), 'positive_window_count': sum(bool(r['all_positive_tracks']) for r in subset),
            'selected_images': sum(r['selected_images'] for r in subset),
            'selected_bytes': sum(r['selected_bytes'] for r in subset),
            'selected_positive_track_windows': num, 'positive_track_windows': den,
            'positive_track_window_coverage': num / den if den else None,
            'sampled_positive_track_windows': sampled_den,
            'sampled_positive_track_window_coverage': num / sampled_den if sampled_den else None,
            'selected_local_track_ids': len(selected_whole), 'positive_local_track_ids': len(whole),
            'whole_local_track_coverage': len(selected_whole) / len(whole) if whole else None,
            'status_conflict_sequences': len({r['sequence'] for r in subset if metadata[r['sequence']]['status_conflict']}),
            'label_source_conflict_sequences': len({r['sequence'] for r in subset if metadata[r['sequence']]['label_source_conflict']})})
    used = sorted({r['sequence'] for r in rows})
    return output, [{'run': label, **metadata[sid]} for sid in used]


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--run', action='append', required=True, help='Label=benchmark-directory; repeat for additional completed runs')
    p.add_argument('--outdir', required=True)
    args = p.parse_args()
    all_rows, notes, runs = [], [], []
    seen = set()
    for spec in args.run:
        label, directory = spec.split('=', 1)
        if not label or label in seen:
            p.error('Each run needs a unique nonempty label')
        seen.add(label)
        folder = Path(directory).resolve()
        provpath, selpath = folder / 'provenance.json', folder / 'selections.jsonl'
        provenance = json.loads(provpath.read_text())
        selections = [json.loads(x) for x in selpath.read_text().splitlines() if x.strip()]
        data, caveats = summarize(label, provenance, selections)
        all_rows.extend(data)
        notes.extend(caveats)
        runs.append({'label': label, 'benchmark_directory': str(folder), 'provenance_sha256': sha(provpath),
                     'selections_sha256': sha(selpath), 'selection_rows': len(selections)})
    out = Path(args.outdir).resolve()
    if any(out == Path(r['benchmark_directory']) for r in runs):
        p.error('Write diagnostics outside the original benchmark directory')
    out.mkdir(parents=True, exist_ok=True)
    with (out / 'annotation-sensitivity.csv').open('w') as f:
        fields = list(all_rows[0]) if all_rows else []
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        writer.writerows(all_rows)
    (out / 'annotation-sensitivity.json').write_text(json.dumps(all_rows, indent=2, allow_nan=False) + '\n')
    (out / 'annotation-provenance-caveats.json').write_text(json.dumps(notes, indent=2) + '\n')
    same_object = [{'run': r['run'], 'sequence': r['sequence'], 'source_group': r['source_group'],
                    'same_object_groups': r['qa_same_object_groups'], 'already_merged_track_ids': r['qa_existing_merges'],
                    'qa_notes': r['qa_notes']} for r in notes if r['qa_same_object_groups'] or r['qa_existing_merges']]
    (out / 'same-physical-object-notes.json').write_text(json.dumps(same_object, indent=2) + '\n')
    (out / 'analysis-provenance.json').write_text(json.dumps({'script_sha256': sha(Path(__file__)), 'runs': runs}, indent=2) + '\n')
    (out / 'README.txt').write_text(
        'Descriptive annotation-sensitivity strata; original rankings and metric sets remain unchanged.\n'
        'QA gt_status is used when supplied, otherwise index status; conflicts are exposed. '
        'label_source prefers labels.json provenance, then index; conflicts are exposed.\n'
        'Axes: all, exact gt_status, complete versus partial_or_incomplete, label_source, and their cross. '
        'Each axis partitions sequence rows, but source-group counts can overlap across strata. '
        'Local tracks are keyed by sequence+track ID; these are not necessarily distinct physical objects. '
        'Same-object passes and prior merges are separately listed without merging any metrics.\n'
        'A sequence with no known positives contributes no recall denominator; its images/bytes remain reported. '
        'Undefined coverage is null/blank, not zero. Counts are annotation-confirmed retention, not true all-object recall '
        'or object delivery/recognizability. Incomplete labels can miss objects in selected images. '
        'Even complete status can coexist with QA notes about potentially missed objects. '
        'These post-hoc strata are descriptive diagnostics, not a basis for held-out tuning or significance claims.\n')
    print(json.dumps({'runs': len(runs), 'stratum_rows': len(all_rows), 'outdir': str(out)}))


if __name__ == '__main__':
    main()
