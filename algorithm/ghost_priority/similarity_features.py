"""CPU descriptor preparation, separate from pure-Python cached comparison."""
import cv2
import numpy as np

from .providers import resized_bgr
from .similarity import PHASH_VERSION


def describe(bgr, width=320):
    """Reuse one resized preview for the original color fingerprint and pHash.

    DCT pHash: 32x32 gray, upper 8x8 coefficients with DC zeroed, threshold
    around their mean, 64 bits packed in row-major little-bit order.
    """
    small = resized_bgr(bgr, width)
    values = small.astype(np.float32) / 255.0
    fp = cv2.resize(values, (8, 8), interpolation=cv2.INTER_AREA).reshape(-1)
    color = [round(float(x), 5) for x in fp]
    tiny = cv2.resize(small, (32, 32), interpolation=cv2.INTER_LINEAR_EXACT)
    gray = cv2.cvtColor(tiny, cv2.COLOR_BGR2GRAY).astype(np.float32)
    low = cv2.dct(gray)[:8, :8].copy()
    low[0, 0] = 0.0
    packed = np.packbits((low > low.mean()).reshape(-1), bitorder='little')
    encoded = int.from_bytes(packed.tobytes(), 'little')
    return color, {'version': PHASH_VERSION, 'hex': f'{encoded:016x}', 'analysis_size': width}
