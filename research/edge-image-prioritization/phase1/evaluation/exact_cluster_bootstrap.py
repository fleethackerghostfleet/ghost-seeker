"""Exact empirical paired-cluster bootstrap percentile intervals for small studies.

Resample n source groups n times with replacement. Enumerate multinomial count
vectors, preserving integer ordered-draw multiplicities, and condition on a
positive resampled denominator. No inference about missing annotations is made.
"""
from fractions import Fraction
from functools import lru_cache
from math import comb


def _counts(remaining, groups, prefix=(), weight=1):
    if groups == 1:
        yield prefix + (remaining,), weight
        return
    for count in range(remaining + 1):
        yield from _counts(remaining - count, groups - 1, prefix + (count,),
                           weight * comb(remaining, count))


@lru_cache(maxsize=256)
def _calculate(differences, denominators):
    n = len(differences)
    if not n or not any(denominators):
        return None
    vectorcount = comb(2 * n - 1, n - 1)
    if vectorcount > 1_000_000:
        raise ValueError('Exact enumeration exceeds one million count vectors; use a separately documented larger-study method')
    distribution = {}
    valid_weight = 0
    valid_vectors = 0
    for counts, weight in _counts(n, n):
        denominator = sum(c * den for c, den in zip(counts, denominators))
        if denominator == 0:
            continue
        numerator = sum(c * diff for c, diff in zip(counts, differences))
        value = Fraction(numerator, denominator)
        distribution[value] = distribution.get(value, 0) + weight
        valid_weight += weight
        valid_vectors += 1
    ordered = sorted(distribution.items())
    def quantile(numerator):
        cumulative = 0
        for value, weight in ordered:
            cumulative += weight
            # Left inverse CDF at 1/40 or39/40. Integer arithmetic is exact.
            if cumulative * 40 >= valid_weight * numerator:
                return float(value)
        raise AssertionError('CDF did not reach requested quantile')
    return {'low': quantile(1), 'high': quantile(39),
            'validmass': valid_weight / (n ** n), 'groupcount': n,
            'vectorcount': vectorcount, 'validvectorcount': valid_vectors,
            'method': 'exact paired-source multinomial percentile bootstrap; zero-denominator draws excluded',
            'zero_effect_mass_given_valid_denominator': distribution.get(Fraction(0), 0) / valid_weight}


def exact_interval(differences: list[int], denominators: list[int]) -> dict | None:
    """Return exact95% percentile endpoints as fractions, or None without a denominator.

    Inputs are paired source sums, not per-image observations. A zero-denominator
    source must have zero difference. Equal vectors return equal intervals;
    source order and common positive integer rescaling do not affect endpoints.
    Enumeration is bounded at one million multinomial count vectors (n<=11).
    """
    if len(differences) != len(denominators):
        raise ValueError('Differences and denominators must have equal length')
    if any(type(x) is not int for x in [*differences, *denominators]):
        raise ValueError('Source sums must be integers, excluding booleans')
    if any(d < 0 for d in denominators):
        raise ValueError('Denominators must be nonnegative')
    if any(d == 0 and diff != 0 for diff, d in zip(differences, denominators)):
        raise ValueError('A zero-denominator source cannot have a nonzero coverage difference')
    result = _calculate(tuple(differences), tuple(denominators))
    return None if result is None else dict(result)
