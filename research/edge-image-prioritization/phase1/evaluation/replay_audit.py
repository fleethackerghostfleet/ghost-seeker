#!/usr/bin/env python3
"""Generic saved-output replay; reads no images, GT, candidates or ranking code."""
import argparse
import hashlib
import json
from collections import defaultdict
from pathlib import Path
import statistics


def audit(folder):
    RUN = Path(folder).resolve()
    window_seconds = json.loads((RUN / 'lock.json').read_text())['window_seconds']
    def lines(name):
        return [json.loads(x) for x in (RUN / name).read_text().splitlines() if x.strip()]
    rows = lines('selections.jsonl')
    rankings = lines('rankings.jsonl')
    summary = json.loads((RUN / 'summary.json').read_text())
    input_records = json.loads((RUN / 'records.json').read_text())
    records = {r['id']: r for r in input_records}
    assert len(records) == len(input_records)
    assert len(rows) == len({(r['sequence'], r['window'], r['method'], r['bandwidth_bps']) for r in rows})
    rankpools = defaultdict(list)
    for r in rankings:
        rankpools[r['sequence'], r['window'], r['method']].append(r)
    for (seq, wi, method), ranked in rankpools.items():
        assert [r['priority_rank'] for r in ranked] == list(range(1, len(ranked) + 1))
        expected = {r['id'] for r in records.values() if r['source'] == seq and int(r['timestamp_s'] // window_seconds) == wi and r['usable']}
        assert {r['id'] for r in ranked} == expected
        seen = set()
        for r in ranked:
            assert r['payload_bytes'] == records[r['id']]['payload_bytes']
            assert r['nearest_prior_id'] is None or r['nearest_prior_id'] in seen
            seen.add(r['id'])
    for r in rows:
        chosen, spent = [], 0
        assert r['eligible_images'] == len(rankpools[r['sequence'], r['window'], r['method']])
        for x in rankpools[r['sequence'], r['window'], r['method']]:
            size = records[x['id']]['payload_bytes']
            if spent + size <= r['budget_bytes']:
                chosen.append(x['id'])
                spent += size
        assert chosen == r['selected_ids'] and spent == r['selected_bytes']
        assert len(chosen) == len(set(chosen)) == r['selected_images']
        assert set(r['selected_positive_tracks']) <= set(r['sampled_positive_tracks']) <= set(r['all_positive_tracks'])
        assert set(r['selected_small_positive_tracks']) <= set(r['small_positive_tracks']) <= set(r['all_positive_tracks'])
        assert set(r['selected_first_annotation_events_2s']) <= set(r['sampled_first_annotation_events_2s']) <= set(r['first_annotation_events'])

    metric_pairs = {
        'track_window': ('selected_positive_tracks', 'all_positive_tracks'),
        'sampled_track_window': ('selected_positive_tracks', 'sampled_positive_tracks'),
        'small_track_window': ('selected_small_positive_tracks', 'small_positive_tracks'),
        'first_annotation_2s': ('selected_first_annotation_events_2s', 'first_annotation_events'),
    }
    for s in summary:
        matching = [r for r in rows if (r['method'], r['bandwidth_bps']) == (s['method'], s['bandwidth_bps'])]
        assert s['selected_images'] == sum(r['selected_images'] for r in matching)
        assert s['selected_bytes'] == sum(r['selected_bytes'] for r in matching)
        for metric, (numkey, denkey) in metric_pairs.items():
            num, den = sum(len(r[numkey]) for r in matching), sum(len(r[denkey]) for r in matching)
            assert s[metric + '_numerator'] == num and s[metric + '_denominator'] == den
            assert s[metric + '_coverage'] == (num / den if den else None)
        selected = {(r['sequence'], t) for r in matching for t in r['selected_positive_tracks']}
        all_tracks = {(r['sequence'], t) for r in matching for t in r['all_positive_tracks']}
        assert (s['whole_track_numerator'], s['whole_track_denominator']) == (len(selected), len(all_tracks))

    base = {(r['sequence'], r['window'], r['bandwidth_bps']): r for r in rows if r['method'] == 'color-phash'}
    no_sim = {(r['sequence'], r['window'], r['bandwidth_bps']): r for r in rows if r['method'] == 'no_similarity'}
    comparison = []
    for method in sorted({r['method'] for r in rows}):
        method_rows = [r for r in rows if r['method'] == method]
        sims = [r['max_similarity'] for r in rankings if r['method'] == method]
        comparison.append({'method': method, 'selection_rows': len(method_rows),
            'exact_order_matches_no_similarity': sum(r['selected_ids'] == no_sim[r['sequence'], r['window'], r['bandwidth_bps']]['selected_ids'] for r in method_rows),
            'max_redundancy': max(sims), 'median_redundancy': statistics.median(sims),
            'changed_64kbps_pools_from_hybrid': sum(set(r['selected_ids']) != set(base[r['sequence'], r['window'], r['bandwidth_bps']]['selected_ids']) for r in method_rows if r['bandwidth_bps'] == 64000)})

    native = defaultdict(lambda: [0, 0])
    for r in rows:
        if r['method'] != 'color-phash' or r['bandwidth_bps'] != 64000:
            continue
        for track in r['all_positive_tracks']:
            for cls in r['track_classes'][track]:
                native[cls][0] += track in r['selected_positive_tracks']
                native[cls][1] += 1


    selection_diagnostics = []
    coverage_changes = []
    for method in sorted({r['method'] for r in rows}):
        for bps in sorted({r['bandwidth_bps'] for r in rows}):
            subset = [r for r in rows if r['method'] == method and r['bandwidth_bps'] == bps]
            selection_diagnostics.append({'method': method, 'bandwidth_bps': bps, 'pools': len(subset),
                'order_matches_no_similarity': sum(r['selected_ids'] == no_sim[r['sequence'],r['window'],bps]['selected_ids'] for r in subset),
                'set_matches_no_similarity': sum(set(r['selected_ids']) == set(no_sim[r['sequence'],r['window'],bps]['selected_ids']) for r in subset),
                'positive_sets_match_no_similarity': sum(r['selected_positive_tracks'] == no_sim[r['sequence'],r['window'],bps]['selected_positive_tracks'] for r in subset)})
            for r in subset:
                b = base[r['sequence'], r['window'], bps]
                gained = set(r['selected_positive_tracks']) - set(b['selected_positive_tracks'])
                lost = set(b['selected_positive_tracks']) - set(r['selected_positive_tracks'])
                if gained or lost:
                    coverage_changes.append({'sequence': r['sequence'], 'source_group': r['source_group'],
                        'window': r['window'], 'method': method, 'bandwidth_bps': bps,
                        'gained_positive_track_ids': sorted(gained), 'lost_positive_track_ids': sorted(lost),
                        'candidate_selected_ids': r['selected_ids'], 'hybrid_selected_ids': b['selected_ids'],
                        'exactly_no_similarity_ids': r['selected_ids'] == no_sim[r['sequence'],r['window'],bps]['selected_ids'],
                        'known_positive_tracks': r['all_positive_tracks'], 'native_classes': r['track_classes']})
    return {'run': str(RUN), 'checks_passed': True, 'selection_rows_replayed': len(rows),
        'ranking_pools_checked': len(rankpools), 'summary_rows_recounted': len(summary),
        'records': len(records), 'source_groups': len({r['source_group'] for r in rows}),
        'positive_source_groups': len({r['source_group'] for r in rows if r['all_positive_tracks']}),
        'method_diagnostics': comparison, 'selection_diagnostics': selection_diagnostics,
        'coverage_changes_from_hybrid': coverage_changes, 'hybrid_64kbps_native_classes': dict(native),
        'input_sha256': {n: hashlib.sha256((RUN/n).read_bytes()).hexdigest() for n in
                         ['records.json','rankings.jsonl','selections.jsonl','summary.json']},
        'caveat': 'Saved-set accounting only; no raw annotation or JPEG visibility validation. Track IDs need provenance qualifiers.'}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--run', required=True, help='Completed benchmark directory')
    p.add_argument('--out', required=True, help='JSON report path outside the benchmark')
    args = p.parse_args()
    out = Path(args.out).resolve()
    if out.parent == Path(args.run).resolve():
        p.error('Write diagnostics outside the source benchmark directory')
    result = audit(args.run)
    out.parent.mkdir(parents=True,exist_ok=True)
    out.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({k:result[k] for k in ['checks_passed','selection_rows_replayed','ranking_pools_checked','summary_rows_recounted','source_groups','positive_source_groups']}))


if __name__ == '__main__':
    main()
