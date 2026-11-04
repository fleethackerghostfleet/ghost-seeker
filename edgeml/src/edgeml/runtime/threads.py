"""Thread pinning so every library honours the CONTRACTS §6 budget (<=4 threads)."""
from __future__ import annotations

import os


def pin_threads(n: int = 4) -> int:
    """Set every thread knob we know about to ``n``. Call before importing torch / onnxruntime / openvino.

    Also pins the process CPU affinity to the first ``n`` CPUs when the OS allows it, so a plain
    ``python`` invocation behaves like ``taskset -c 0-(n-1) python``.
    """
    for var in ("OMP_NUM_THREADS", "MKL_NUM_THREADS", "OPENBLAS_NUM_THREADS", "NUMEXPR_NUM_THREADS",
                "TBB_NUM_THREADS", "VECLIB_MAXIMUM_THREADS"):
        os.environ[var] = str(n)
    try:
        affinity = sorted(os.sched_getaffinity(0))
        if len(affinity) > n:
            os.sched_setaffinity(0, set(affinity[:n]))
    except (AttributeError, OSError):
        pass
    try:
        import cv2

        cv2.setNumThreads(n)
        cv2.ocl.setUseOpenCL(False)
    except ImportError:
        pass
    import sys

    if "torch" in sys.modules:  # never import torch just to pin it: it costs ~300 MB RSS on the laptop
        try:
            sys.modules["torch"].set_num_threads(n)
            sys.modules["torch"].set_num_interop_threads(1)
        except RuntimeError:
            pass
    return n


def affinity_cpus() -> list[int]:
    try:
        return sorted(os.sched_getaffinity(0))
    except (AttributeError, OSError):
        return list(range(os.cpu_count() or 1))
