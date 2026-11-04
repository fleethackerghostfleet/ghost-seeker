"""Stable full-pool prioritization, independent of payload sizes and bandwidth.

Scores are uncalibrated heuristics, not probabilities. Similarity penalizes visual
redundancy against earlier ranked entries; it does not establish object identity.
The simple full-ranking algorithm uses O(N²D) time and O(ND) memory.
"""
from __future__ import annotations

import copy
import json
import math
from collections import deque
from pathlib import Path
from .similarity import engine_from_policy, color_similarity as _similarity


DEFAULT_POLICY = {
    "default_object_weight": 0.65,
    "class_weights": {},
    "visual_interest_weight": 0.65,
    "quality_weight": 0.10,
    "duplicate_strength": 0.85,
    "duplicate_distance": 0.08,
    "similarity_method": "color-phash",
    "phash_distance_scale": 8.0,
}


def _unit(value, name):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{name} must be a finite number between 0 and 1")
    if not math.isfinite(value) or not 0 <= value <= 1:
        raise ValueError(f"{name} must be a finite number between 0 and 1")
    return float(value)


def _policy(overrides=None):
    result = copy.deepcopy(DEFAULT_POLICY)
    if overrides is not None:
        if not isinstance(overrides, dict):
            raise ValueError("policy must be a JSON object")
        overrides = copy.deepcopy(overrides)
        removed = set(overrides) & {"byte_cost_exponent", "coverage_fraction"}
        if removed:
            raise ValueError(
                f"removed policy keys: {sorted(removed)}; remove these settings. "
                "Ranking is now independent of bytes and budget; use select_ranked "
                "to apply a byte allowance after ranking."
            )
        if "novelty_weight" in overrides:
            legacy = _unit(overrides.pop("novelty_weight"), "novelty_weight")
            if "visual_interest_weight" in overrides:
                canonical = _unit(overrides["visual_interest_weight"], "visual_interest_weight")
                if legacy != canonical:
                    raise ValueError("conflicting novelty_weight and visual_interest_weight")
            overrides["visual_interest_weight"] = legacy
        unknown = set(overrides) - set(result)
        if unknown:
            raise ValueError(f"unknown policy keys: {sorted(unknown)}")
        result.update(overrides)
    for key in result:
        if key not in {"class_weights", "similarity_method", "phash_distance_scale"}:
            result[key] = _unit(result[key], key)
    engine_from_policy(result)  # Validate method and metric-specific scales.
    if result["duplicate_distance"] == 0:
        raise ValueError("duplicate_distance must be greater than zero")
    if not isinstance(result["class_weights"], dict):
        raise ValueError("class_weights must be an object")
    for label, weight in result["class_weights"].items():
        if not isinstance(label, str) or not label.strip():
            raise ValueError("class weight names must be nonempty strings")
        result["class_weights"][label] = _unit(weight, f"class_weights.{label}")
    return result


def load_policy(path=None):
    """Merge a JSON override file over defaults, accepting the legacy weight alias."""
    return _policy() if path is None else _policy(json.loads(Path(path).read_text()))


def _validate_ids(records):
    records = list(records)
    seen = set()
    for record in records:
        if not isinstance(record, dict):
            raise ValueError("each record must be an object")
        key = record.get("id")
        if not isinstance(key, str) or not key:
            raise ValueError("each record must have a nonempty string id")
        if key in seen:
            raise ValueError(f"duplicate record id: {key}")
        seen.add(key)
    return records


def validate_records(records):
    """Validate identities and payload accounting for selection/evaluation."""
    records = _validate_ids(records)
    for record in records:
        size = record.get("payload_bytes")
        if isinstance(size, bool) or not isinstance(size, int) or size <= 0:
            raise ValueError(f"{record['id']}: payload_bytes must be a positive integer")
    return records


def _score(record, policy, method="priority"):
    signals = record.get("signals", {})
    if not isinstance(signals, dict):
        raise ValueError("signals must be an object")
    visual_interest = _unit(signals.get("visual_interest", signals.get("novelty", 0)), "visual_interest")
    quality = _unit(signals.get("quality", 0.5), "quality")
    reasons = []
    direct = record.get("object_relevance")
    if direct is not None:
        # Provider relevance is already weighted; detections are display metadata.
        relevance = _unit(direct, "object_relevance")
        if relevance > 0:
            reasons.append("object_relevance")
    else:
        detections = record.get("detections")
        relevance = None if detections is None else 0.0
        if detections is not None:
            if not isinstance(detections, list):
                raise ValueError("detections must be a list or null")
            best_label = None
            for detection in detections:
                if not isinstance(detection, dict):
                    raise ValueError("each detection must be an object")
                label = detection.get("label")
                if not isinstance(label, str) or not label:
                    raise ValueError("detection label must be a nonempty string")
                confidence = _unit(detection.get("confidence"), "detection confidence")
                weight = policy["class_weights"].get(label, policy["default_object_weight"])
                value = confidence * weight
                if value > relevance:
                    relevance, best_label = value, label
            if best_label is not None:
                reasons.append("object_detected:" + best_label)
    visual_contribution = visual_interest * policy["visual_interest_weight"]
    if method == "detector":
        if relevance is None:
            raise ValueError("detector baseline requires evaluated object evidence for every eligible record")
        result = relevance
    elif method == "novelty":
        result = visual_contribution
        reasons = ["visual_interest"] if visual_interest > 0 else []
    else:
        base = max(relevance or 0, visual_contribution)
        result = base * (1 - policy["quality_weight"] * (1 - quality))
        if visual_contribution > 0:
            reasons.append("visual_interest")
        if base > 0 and quality > 0.5:
            reasons.append("image_quality")
    if not reasons:
        reasons = ["no_positive_signal"]
    return {"score": result, "relevance": relevance, "visual_interest": visual_interest,
            "novelty": visual_interest, "reasons": reasons}


def score_record(record, policy=None):
    """Compute a base score before similarity. Ground-truth fields are ignored."""
    return _score(record, _policy(policy))


def _uniform_order(records):
    """Dyadic spacing over stable source/time/id order, independent of scores."""
    def key(record):
        timestamp = record.get("timestamp_s")
        if timestamp is not None and (isinstance(timestamp, bool) or not isinstance(timestamp, (int, float)) or not math.isfinite(timestamp)):
            raise ValueError("timestamp_s must be finite or null")
        return (str(record.get("source", "")), timestamp if timestamp is not None else 0, record["id"])
    ordered = sorted(records, key=key)
    intervals = deque([(0, len(ordered))])
    while intervals:
        lo, hi = intervals.popleft()
        if lo >= hi:
            continue
        mid = (lo + hi) // 2
        yield ordered[mid]
        intervals.append((lo, mid))
        intervals.append((mid + 1, hi))


def _ranked_copy(record, details, rank, method, final_score, similarity=0.0, nearest=None):
    row = copy.deepcopy(record)
    # Re-ranking cached selections must not retain claims about an old subset.
    row.pop("selection_rank", None)
    row.pop("selection_lane", None)
    row.pop("similarity_engine", None)
    row.pop("similarity_match", None)
    row.update(copy.deepcopy(details))
    row.update(priority_rank=rank, rank=rank, base_score=details["score"],
               final_score=final_score, marginal_score=final_score,
               max_similarity=similarity, nearest_prior_id=nearest,
               ranking_method=method)
    if final_score is not None and final_score < details["score"]:
        row["reasons"].append("redundancy_penalty")
    return row


def rank_records(records, policy=None, method="priority"):
    """Rank every eligible observation without inspecting payload size or bandwidth.

    Priority ranking repeatedly chooses the highest base_score *
    (1 - duplicate_strength * max_similarity_to_earlier_ranked), tie-breaking by ID.
    Similar entries remain in the full order. Detector/novelty comparison methods
    sort their own score without quality or similarity adjustments; uniform uses
    dyadic source/time/id order. The returned rank field aliases priority_rank.
    """
    if method not in {"priority", "uniform", "detector", "novelty"}:
        raise ValueError("unknown ranking method")
    policy = _policy(policy)
    records = sorted((r for r in _validate_ids(records) if r.get("usable") is not False), key=lambda r: r["id"])
    if method == "uniform":
        details = {"score": None, "relevance": None, "visual_interest": None,
                   "novelty": None, "reasons": ["representative_sample"]}
        return [_ranked_copy(row, details, i+1, method, None) for i, row in enumerate(_uniform_order(records))]
    scores = {r["id"]: _score(r, policy, method) for r in records}
    if method != "priority":
        ordered = sorted(records, key=lambda r: (-scores[r["id"]]["score"], r["id"]))
        return [_ranked_copy(row, scores[row["id"]], i+1, method, scores[row["id"]]["score"]) for i, row in enumerate(ordered)]

    engine = engine_from_policy(policy)
    descriptors = {r["id"]: engine.prepare(r) for r in records}
    similarity = {r["id"]: 0.0 for r in records}
    nearest = {r["id"]: None for r in records}
    matches = {r["id"]: None for r in records}
    pending, ranked = list(records), []
    while pending:
        def key(record):
            name = record["id"]
            final = scores[name]["score"] * (1 - policy["duplicate_strength"] * similarity[name])
            return (-final, name)
        best = min(pending, key=key)
        name = best["id"]
        final = -key(best)[0]
        ranked.append(_ranked_copy(best, scores[name], len(ranked)+1, method,
                                   final, similarity[name], nearest[name]))
        ranked[-1].update(similarity_engine=engine.name, similarity_match=matches[name])
        pending.remove(best)
        for other in pending:
            other_id = other["id"]
            match = engine.compare(descriptors[name], descriptors[other_id])
            value = match['score']
            if value > similarity[other_id]:
                similarity[other_id], nearest[other_id] = value, name
                matches[other_id] = match
    return ranked


def select_records(records, budget_bytes, policy=None, method="priority"):
    """Compatibility convenience: full ranking followed by separate byte selection.

    Call rank_records followed by selection.select_ranked directly to reuse the
    exact same complete ranking across several bandwidth budgets.
    """
    from .selection import select_ranked
    return select_ranked(rank_records(records, policy=policy, method=method), budget_bytes)
