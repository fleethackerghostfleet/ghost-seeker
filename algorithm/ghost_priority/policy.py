"""Deterministic offline JPEG selection; heuristic scores are not probabilities.

This is a batch comparison, not a causal network scheduler. Defaults are provisional.
Coverage is reserved in encoded JPEG bytes; unspendable reserve returns to the main
selection. Byte costs exclude transport and metadata overhead. Fingerprint similarity
is only a visual redundancy heuristic, not object identity.
"""
from __future__ import annotations

import copy
import json
import math
from collections import deque
from pathlib import Path


DEFAULT_POLICY = {
    "default_object_weight": 0.65,
    "class_weights": {},
    "novelty_weight": 0.65,
    "quality_weight": 0.10,
    "duplicate_strength": 0.85,
    "duplicate_distance": 0.08,
    "coverage_fraction": 0.05,
    "byte_cost_exponent": 0.5,
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
        unknown = set(overrides) - set(result)
        if unknown:
            raise ValueError(f"unknown policy keys: {sorted(unknown)}")
        result.update(copy.deepcopy(overrides))
    for key in result:
        if key != "class_weights":
            result[key] = _unit(result[key], key)
    if result["duplicate_distance"] == 0:
        raise ValueError("duplicate_distance must be greater than zero")
    if not isinstance(result["class_weights"], dict):
        raise ValueError("class_weights must be an object")
    for label, weight in result["class_weights"].items():
        if not isinstance(label, str) or not label.strip():
            raise ValueError("class weight names must be nonempty strings")
        _unit(weight, f"class_weights.{label}")
    return result


def load_policy(path=None):
    """Load a JSON override file merged over a fresh copy of the defaults."""
    if path is None:
        return _policy()
    return _policy(json.loads(Path(path).read_text()))


def validate_records(records):
    """Validate identities and payload accounting, including excluded records."""
    records = list(records)
    seen = set()
    for record in records:
        key = record.get("id")
        if not isinstance(key, str) or not key:
            raise ValueError("each record must have a nonempty string id")
        if key in seen:
            raise ValueError(f"duplicate record id: {key}")
        seen.add(key)
        size = record.get("payload_bytes")
        if isinstance(size, bool) or not isinstance(size, int) or size <= 0:
            raise ValueError(f"{key}: payload_bytes must be a positive integer")
    return records


def _score(record, policy, method="priority"):
    signals = record.get("signals", {})
    novelty = _unit(signals.get("novelty", 0), "novelty")
    quality = _unit(signals.get("quality", 0.5), "quality")
    detections = record.get("detections")
    relevance = None if detections is None else 0.0
    reasons = []
    if detections is not None:
        if not isinstance(detections, list):
            raise ValueError("detections must be a list or null")
        best_label = None
        for detection in detections:
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
    novelty_contribution = novelty * policy["novelty_weight"]
    if method == "detector":
        if relevance is None:
            raise ValueError("detector baseline requires evaluated detections for every eligible record")
        base = relevance
        # Strict detector baseline: quality and novelty do not influence selection.
        result = base
    elif method == "novelty":
        base = novelty_contribution
        result = base
        reasons = ["unusual_region"] if novelty > 0 else []
    else:
        base = max(relevance or 0, novelty_contribution)
        result = base * (1 - policy["quality_weight"] * (1 - quality))
        if novelty_contribution > 0:
            reasons.append("unusual_region")
        if base > 0 and quality > 0.5:
            reasons.append("image_quality")
    if not reasons:
        reasons = ["representative_sample"]
    return {"score": result, "relevance": relevance, "novelty": novelty, "reasons": reasons}


def score_record(record, policy=None):
    """Score evidence only. Annotation/ground-truth fields are never consulted."""
    return _score(record, _policy(policy))


def _uniform_order(records):
    """Dyadic index spacing over stable source/time/id order, independent of scores."""
    def key(record):
        timestamp = record.get("timestamp_s")
        if timestamp is not None and (not isinstance(timestamp, (int, float)) or not math.isfinite(timestamp)):
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


def _fingerprint(record):
    values = record.get("fingerprint") or []
    if not isinstance(values, list):
        raise ValueError("fingerprint must be a flat list")
    for value in values:
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
            raise ValueError("fingerprint must contain finite numbers")
    return values


def _similarity(a, b, scale):
    if not a or len(a) != len(b):
        return 0.0
    distance = sum(abs(x-y) for x, y in zip(a, b)) / len(a)
    return math.exp(-((distance / scale) ** 2))


def select_records(records, budget_bytes, policy=None, method="priority"):
    """Return copies in selection order without exceeding an indivisible byte budget.

    Uniform selection uses dyadically spaced source/time/id order. Priority selection
    maximizes heuristic marginal utility / bytes**byte_cost_exponent; it is a greedy
    approximation, not an optimal knapsack solution. Similarity is updated against
    the selected set with O(N*K*D) work and O(N*D) memory, not an NxN matrix.
    """
    if isinstance(budget_bytes, bool) or not isinstance(budget_bytes, int) or budget_bytes < 0:
        raise ValueError("budget_bytes must be a nonnegative integer")
    if method not in {"priority", "uniform", "detector", "novelty"}:
        raise ValueError("unknown selection method")
    policy = _policy(policy)
    records = [r for r in validate_records(records) if r.get("usable") is not False]
    records.sort(key=lambda r: r["id"])
    uniform = list(_uniform_order(records))
    selected, selected_ids, spent = [], set(), 0
    scores = {} if method == "uniform" else {r["id"]: _score(r, policy, method) for r in records}
    fingerprints = {} if method == "uniform" else {r["id"]: _fingerprint(r) for r in records}
    similarity = {r["id"]: 0.0 for r in records}

    def add(record, lane, marginal=None):
        nonlocal spent
        row = copy.deepcopy(record)
        details = scores.get(record["id"], {"score": None, "relevance": None, "novelty": None, "reasons": []})
        row.update(copy.deepcopy(details))
        if lane in {"coverage", "uniform"}:
            row["reasons"].append("representative_sample")
        elif similarity[record["id"]] > 0.5:
            row["reasons"].append("redundancy_penalty")
        row.update(rank=len(selected)+1, marginal_score=marginal, selection_lane=lane)
        selected.append(row)
        selected_ids.add(record["id"])
        spent += record["payload_bytes"]
        if method == "priority":
            fp = fingerprints[record["id"]]
            for other in records:
                other_id = other["id"]
                similarity[other_id] = max(similarity[other_id], _similarity(fp, fingerprints[other_id], policy["duplicate_distance"]))

    if method == "uniform":
        for record in uniform:
            if spent + record["payload_bytes"] <= budget_bytes:
                add(record, "uniform")
        return selected

    reserve = int(budget_bytes * policy["coverage_fraction"]) if method == "priority" else 0
    for record in uniform:
        if spent + record["payload_bytes"] <= reserve:
            add(record, "coverage", scores[record["id"]]["score"])
    while True:
        best, best_key, marginal = None, None, None
        for record in records:
            key = record["id"]
            if key in selected_ids or spent + record["payload_bytes"] > budget_bytes:
                continue
            value = scores[key]["score"]
            if method == "priority":
                value *= 1 - policy["duplicate_strength"] * similarity[key]
            utility = value / (record["payload_bytes"] ** policy["byte_cost_exponent"])
            candidate_key = (utility, value, -record["payload_bytes"])
            # Sorted IDs plus strict comparison make ties reproducible.
            if best_key is None or candidate_key > best_key:
                best, best_key, marginal = record, candidate_key, value
        if best is None:
            break
        add(best, method, marginal)
    return selected
