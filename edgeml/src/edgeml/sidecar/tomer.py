"""Tomer's CPU image prioritizer (algorithm/ghost_priority) as a sidecar scoring backend.

The package is imported lazily and unmodified from ``<repo>/algorithm`` (or
``$GHOST_PRIORITY_PATH``). Per frame: ``Analyzer.analyze`` -> ``policy.score_record``
(base score) -> color+pHash similarity against the source's recent *kept* frames.
The returned score is his ``rank_records`` term, ``base * (1 - duplicate_strength *
max_similarity)``; the stream replaces the offline pool, so "earlier ranked" means
"earlier in time". Bandwidth selection stays with the Go scheduler.
"""
from __future__ import annotations

import collections
import logging
import os
import pathlib
import sys
import time

log = logging.getLogger("edgeml.sidecar.tomer")

MODES = ("edgeml", "tomer", "both")


def find_algorithm_dir() -> pathlib.Path | None:
    env = os.environ.get("GHOST_PRIORITY_PATH")
    if env:
        return pathlib.Path(env)
    for parent in pathlib.Path(__file__).resolve().parents:
        if (parent / "algorithm" / "ghost_priority" / "policy.py").is_file():
            return parent / "algorithm"
    return None


def import_ghost_priority():
    try:
        import ghost_priority  # noqa: F401  (installed or already on sys.path)
    except ImportError:
        d = find_algorithm_dir()
        if d is None:
            raise ImportError("ghost_priority not importable: check out algorithm/ at the repo root "
                              "(PR #29) or set GHOST_PRIORITY_PATH") from None
        sys.path.insert(0, str(d))
    from ghost_priority import analysis, policy, similarity

    return analysis, policy, similarity


class TomerPrioritizer:
    """Stateful per-source wrapper; one instance serves every source of a sidecar."""

    name = "tomer"

    def __init__(self, model_path: str | None = None, policy_path: str | None = None, *, width: int = 320,
                 threads: int = 0, window: int = 64, dup_threshold: float = 0.5):
        analysis, policy, similarity = import_ghost_priority()
        self.analyzer = analysis.Analyzer(model_path, width=width, threads=threads)
        self.policy = policy.load_policy(policy_path)
        self._score_record = policy.score_record
        self.similarity = similarity.engine_from_policy(self.policy)
        self.window = window
        self.dup_threshold = dup_threshold
        self._hist: dict[str, collections.deque] = {}
        self._n = 0
        self.engine = "ghost_priority/" + getattr(self.similarity, "name", self.policy["similarity_method"])

    def reset(self, source: str | None = None) -> None:
        if source is None:
            self._hist.clear()
        else:
            self._hist.pop(source, None)

    def score(self, source: str, bgr) -> dict:
        """Returns protobuf-JSON for ghostseeker.v1.ImagePriority."""
        t0 = time.perf_counter()
        rec = self.analyzer.analyze(bgr)
        self._n += 1
        rec["id"] = f"{source or 'frame'}#{self._n}"
        signals = rec.get("signals") or {}
        out = {"engine": f"ghost_priority/{rec.get('engine', '?')}", "frameId": rec["id"],
               "visualInterest": float(signals.get("visual_interest", 0.0)), "quality": float(signals.get("quality", 0.0)),
               "objectRelevance": -1.0 if rec.get("object_relevance") is None else float(rec["object_relevance"]),
               "nearestPriorId": "", "maxSimilarity": 0.0, "nearDuplicate": False}
        if rec.get("usable") is False:  # rank_records drops these; we score them last instead
            out.update(score=0.0, baseScore=0.0, latencyMs=str(int((time.perf_counter() - t0) * 1000)))
            return out
        base = float(self._score_record(rec, self.policy)["score"])
        desc = self.similarity.prepare(rec)
        hist = self._hist.setdefault(source, collections.deque(maxlen=self.window))
        max_sim, nearest = 0.0, ""
        for pid, pdesc in hist:
            m = self.similarity.compare(desc, pdesc)
            if m.get("available", True) and m["score"] > max_sim:
                max_sim, nearest = float(m["score"]), pid
        final = base * (1 - self.policy["duplicate_strength"] * max_sim)
        dup = max_sim >= self.dup_threshold
        if not dup:
            hist.append((rec["id"], desc))
        out.update(score=final, baseScore=base, maxSimilarity=max_sim, nearestPriorId=nearest, nearDuplicate=dup,
                   latencyMs=str(int((time.perf_counter() - t0) * 1000)))
        return out
