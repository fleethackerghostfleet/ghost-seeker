"""Convert this application's outbound payload rate into an offline byte budget.

Presets are hackathon scenarios, not measured or specified Navy link capacities.
Rates are decimal bits per second after allocation to other traffic and overhead.
"""
from fractions import Fraction
import math


BANDWIDTH_PRESETS_BPS = {
    'normal': 2_000_000,
    'degraded': 256_000,
    'ddil': 64_000,
    'severe-ddil': 9_600,
    'disconnected': 0,
}
DEFAULT_AVAILABLE_BANDWIDTH_BPS = BANDWIDTH_PRESETS_BPS['normal']
DEFAULT_WINDOW_SECONDS = 60.0


def bandwidth_budget_bytes(available_bandwidth_bps=DEFAULT_AVAILABLE_BANDWIDTH_BPS,
                           window_seconds=DEFAULT_WINDOW_SECONDS):
    """Return floor(bits/second * seconds / 8), independent of the input size.

The caller supplies effective payload goodput allocated to this service. Do not
pass a modem's advertised rate without accounting for competing traffic, framing,
encryption and retries. Zero rate models a disconnected selection window.
"""
    if (isinstance(available_bandwidth_bps, bool)
            or not isinstance(available_bandwidth_bps, int)
            or available_bandwidth_bps < 0):
        raise ValueError('available_bandwidth_bps must be a nonnegative integer')
    if (isinstance(window_seconds, bool) or not isinstance(window_seconds, (int, float))
            or not math.isfinite(window_seconds) or window_seconds <= 0):
        raise ValueError('window_seconds must be finite and positive')
    # Decimal-text conversion avoids rounding a fractional-byte allowance up.
    return (available_bandwidth_bps * Fraction(str(window_seconds))) // 8


def format_bandwidth(available_bandwidth_bps):
    if available_bandwidth_bps >= 1_000_000:
        return f'{available_bandwidth_bps / 1_000_000:g} Mbps'
    if available_bandwidth_bps >= 1_000:
        return f'{available_bandwidth_bps / 1_000:g} kbps'
    return f'{available_bandwidth_bps} bps'
