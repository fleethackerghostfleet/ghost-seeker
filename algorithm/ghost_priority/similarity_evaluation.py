"""Offline pair review; labels never enter descriptor preparation or ranking."""
from collections import Counter
import math

from .similarity import CachedSimilarity, METHODS

LABELS = ('same_information', 'changed_information', 'uncertain')


def evaluate_pairs(records, pairs, thresholds=(.25, .5, .75, .9), color_scale=.08, phash_scale=8.0):
    """Report duplicate candidates and false suppression candidates separately.

    A threshold is an evaluation probe only: ranking uses continuous soft scores.
    These metrics do not measure actual transmitted-image recall or accuracy.
    """
    by_id = {}
    for record in records:
        key = record.get('id')
        if not isinstance(key, str) or not key or key in by_id:
            raise ValueError('Evaluation records need unique nonempty string IDs')
        by_id[key] = record
    thresholds = list(thresholds)
    if not thresholds or any(isinstance(t, bool) or not isinstance(t, (int, float)) or not math.isfinite(t) or not 0 < t <= 1 for t in thresholds):
        raise ValueError('Evaluation thresholds must be finite numbers in (0, 1]')
    if len(set(thresholds)) != len(thresholds):
        raise ValueError('Evaluation thresholds must be distinct')
    if not isinstance(pairs, list) or not pairs:
        raise ValueError('Pairs must be a nonempty JSON list')
    seen, used = set(), set()
    for pair in pairs:
        if not isinstance(pair, dict):
            raise ValueError('Each reviewed pair must be an object')
        a, b, label = pair.get('a'), pair.get('b'), pair.get('label')
        if not isinstance(a, str) or not isinstance(b, str) or a not in by_id or b not in by_id or a == b:
            raise ValueError('Pair IDs must name two different existing records')
        key = tuple(sorted((a, b)))
        if key in seen:
            raise ValueError('Repeated unordered pair in evaluation')
        seen.add(key)
        used.update((a, b))
        if label not in LABELS:
            raise ValueError(f'Pair label must be one of {LABELS}')
        if not isinstance(pair.get('group', 'ungrouped'), str) or not pair.get('group', 'ungrouped'):
            raise ValueError('Pair group must be a nonempty string')
    comparisons = {method: CachedSimilarity(method, color_scale, phash_scale) for method in METHODS}
    prepared = {method: {key: engine.prepare(by_id[key]) for key in used}
                for method, engine in comparisons.items()}
    results = []
    for pair in pairs:
        results.append({**pair, 'group': pair.get('group', 'ungrouped'),
                        'matches': {method: engine.compare(prepared[method][pair['a']], prepared[method][pair['b']])
                                    for method, engine in comparisons.items()}})
    metrics = []
    for group in [None, *sorted({p['group'] for p in results})]:
        rows = results if group is None else [p for p in results if p['group'] == group]
        counts = Counter(p['label'] for p in rows)
        for method in METHODS:
            for threshold in thresholds:
                flagged = [p for p in rows if p['matches'][method]['available'] and p['matches'][method]['score'] >= threshold]
                duplicate_hits = sum(p['label'] == 'same_information' for p in flagged)
                changed_hits = sum(p['label'] == 'changed_information' for p in flagged)
                metrics.append({'group': group, 'method': method, 'threshold': threshold,
                                'same_information_pairs': counts['same_information'],
                                'changed_information_pairs': counts['changed_information'],
                                'uncertain_pairs': counts['uncertain'],
                                'duplicate_candidates': duplicate_hits,
                                'false_suppression_candidates': changed_hits,
                                'uncertain_candidates': sum(p['label'] == 'uncertain' for p in flagged),
                                'unavailable_pairs': sum(not p['matches'][method]['available'] for p in rows),
                                'duplicate_candidate_recall': duplicate_hits/counts['same_information'] if counts['same_information'] else None,
                                'changed_pair_false_suppression_rate': changed_hits/counts['changed_information'] if counts['changed_information'] else None})
    return {'pair_count': len(pairs), 'label_counts': dict(Counter(p['label'] for p in results)),
            'color_scale': color_scale, 'phash_distance_scale': phash_scale,
            'pairs': results, 'metrics': metrics,
            'notes': ['Pairwise threshold diagnostics, not actual selections or calibrated duplicate probabilities.',
                      'Uncertain pairs do not count as positives or negatives.',
                      'No thresholds or labels are applied to production policy by this command.',
                      'Synthetic changes are mechanism probes, not a held-out real-world quality benchmark.']}
