"""Deterministic causal payload transport model for private research.

This is not the production Go transport, a network emulator, or a receipt claim
about a real link. Packets are indivisible images after complete serialization;
bytes already serialized during a partial transfer still consume capacity.
"""
from __future__ import annotations

import argparse
import copy
import json
import math
from pathlib import Path

EPS = 1e-10


def _number(value, name, *, nonnegative=True):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ValueError(f"{name} must be a finite number")
    if nonnegative and value < 0:
        raise ValueError(f"{name} must be nonnegative")
    return float(value)


def _prepare(packets, link, horizon_s, finish_tail_s, overhead_bytes, propagation_s, ttl_s, policy):
    horizon = _number(horizon_s, "horizon_s")
    tail = _number(finish_tail_s, "finish_tail_s")
    propagation = _number(propagation_s, "propagation_s")
    if isinstance(overhead_bytes, bool) or not isinstance(overhead_bytes, int) or overhead_bytes < 0:
        raise ValueError("overhead_bytes must be a nonnegative integer")
    ttl = None if ttl_s is None else _number(ttl_s, "ttl_s")
    if policy not in {"priority", "fifo"}:
        raise ValueError("policy must be 'priority' or 'fifo'")
    prepared = []
    ids = set()
    for original in packets:
        if not isinstance(original, dict):
            raise ValueError("packets must be objects")
        p = copy.deepcopy(original)
        if not isinstance(p.get("id"), str) or not p["id"] or p["id"] in ids:
            raise ValueError("packet IDs must be nonempty unique strings")
        ids.add(p["id"])
        p["arrival_s"] = _number(p.get("arrival_s"), "arrival_s")
        p["priority"] = _number(p.get("priority", 0), "priority", nonnegative=False)
        if isinstance(p.get("bytes"), bool) or not isinstance(p.get("bytes"), int) or p["bytes"] <= 0:
            raise ValueError("packet bytes must be a positive integer")
        deps = p.get("dependency_ids", [])
        if not isinstance(deps, list) or any(not isinstance(d, str) or not d for d in deps) or len(deps) != len(set(deps)):
            raise ValueError("dependency_ids must be unique strings in a list")
        p["dependency_ids"] = deps
        p["latest_start_s"] = None if ttl is None else p["arrival_s"] + ttl
        p["wire_bytes"] = p["bytes"] + overhead_bytes
        prepared.append(p)
    by_id = {p["id"]: p for p in prepared}
    for p in prepared:
        if any(d not in ids for d in p["dependency_ids"]):
            raise ValueError(f"unknown dependency for {p['id']}")
    # Iterative topological traversal avoids recursion limits on a long ladder.
    incoming = {p["id"]: len(p["dependency_ids"]) for p in prepared}
    children = {p["id"]: [] for p in prepared}
    for p in prepared:
        for d in p["dependency_ids"]:
            children[d].append(p["id"])
    ready = [pid for pid, count in incoming.items() if count == 0]
    visited = 0
    while ready:
        pid = ready.pop()
        visited += 1
        for child in children[pid]:
            incoming[child] -= 1
            if incoming[child] == 0:
                ready.append(child)
    if visited != len(prepared):
        raise ValueError("dependency cycle")
    intervals = []
    for s in link:
        if not isinstance(s, dict):
            raise ValueError("link intervals must be objects")
        lo = _number(s.get("start_s"), "link.start_s")
        hi = _number(s.get("end_s"), "link.end_s")
        bps = _number(s.get("bps"), "link.bps")
        if hi <= lo:
            raise ValueError("link intervals must have positive duration")
        intervals.append({"start_s": lo, "end_s": hi, "bps": bps})
    intervals.sort(key=lambda x: (x["start_s"], x["end_s"]))
    for previous, current in zip(intervals, intervals[1:]):
        if current["start_s"] < previous["end_s"]:
            raise ValueError("link intervals must not overlap")
    return prepared, intervals, horizon, horizon + tail, propagation, ttl


def simulate(packets, link, horizon_s, *, finish_tail_s=0.0, overhead_bytes=64,
             propagation_s=0.25, ttl_s=60.0, policy="priority"):
    """Serialize available packets without banking capacity or using future input.

    ``arrival_s`` is when all upstream processing/batching is done. TTL limits
    latest transmission *start*, anchored to arrival_s; active transfers are not
    aborted at TTL. Dependency IDs become eligible after modeled receipt. The
    sender can serialize unrelated packets while earlier ones propagate.

    Link intervals are [start,end), bps are bits/second, gaps mean zero rate.
    Arrival exactly at horizon is eligible only when a positive finish tail can
    carry it. Tail never admits arrivals later than the capture horizon.
    """
    rows, intervals, horizon, cutoff, propagation, ttl = _prepare(
        packets, link, horizon_s, finish_tail_s, overhead_bytes, propagation_s, ttl_s, policy)
    pending = {p["id"]: p for p in rows if p["arrival_s"] <= horizon}
    excluded = [dict(p, status="after_capture_horizon") for p in rows if p["arrival_s"] > horizon]
    transmissions = []
    receipts = {}  # Expected receipt times; eligibility compares against clock.
    expired = []
    wire_sent = 0.0
    t = 0.0

    def rate_at(at):
        for interval in intervals:
            if interval["start_s"] <= at < interval["end_s"]:
                return interval["bps"] / 8.0
        return 0.0

    def expire(at):
        for pid, p in list(pending.items()):
            if p["latest_start_s"] is not None and p["latest_start_s"] < at - EPS:
                expired.append(dict(p, status="expired", expired_at_s=p["latest_start_s"]))
                del pending[pid]

    def eligible(p, at):
        return p["arrival_s"] <= at + EPS and all(
            d in receipts and receipts[d] <= at + EPS for d in p["dependency_ids"])

    while t < cutoff - EPS and pending:
        expire(t)
        if not pending:
            break
        available = [p for p in pending.values() if eligible(p, t)]
        if available and rate_at(t) > 0:
            if policy == "priority":
                packet = min(available, key=lambda p: (-p["priority"], p["arrival_s"], p["id"]))
            else:
                packet = min(available, key=lambda p: (p["arrival_s"], p["id"]))
            del pending[packet["id"]]
            start = t
            remaining = float(packet["wire_bytes"])
            segments = []
            serialized = None
            # Transmission is nonpreemptive. Outages pause this same packet.
            for interval in intervals:
                lo = max(t, interval["start_s"])
                hi = min(cutoff, interval["end_s"])
                rate = interval["bps"] / 8.0
                if lo >= hi or rate <= 0:
                    continue
                capacity = (hi - lo) * rate
                if remaining <= capacity + EPS:
                    sent = remaining
                    end = min(hi, lo + sent / rate)
                    segments.append({"start_s": lo, "end_s": end, "bytes": sent, "bps": interval["bps"]})
                    serialized = end
                    remaining = 0.0
                    t = end
                    break
                segments.append({"start_s": lo, "end_s": hi, "bytes": capacity, "bps": interval["bps"]})
                remaining -= capacity
                t = hi
            sent_total = packet["wire_bytes"] - remaining
            wire_sent += sent_total
            expected = None if serialized is None else serialized + propagation
            received = expected if expected is not None and expected <= cutoff + EPS else None
            if expected is not None:
                receipts[packet["id"]] = expected
            stale = received is not None and packet["latest_start_s"] is not None and received > packet["latest_start_s"] + EPS
            transmissions.append(dict(packet, start_s=start, serialized_s=serialized,
                                      expected_received_s=expected, received_s=received,
                                      wire_bytes_sent=sent_total, wire_bytes_remaining=remaining,
                                      segments=segments, stale_at_receipt=stale,
                                      status="received" if received is not None else (
                                          "in_propagation" if serialized is not None else "partial")))
            if serialized is None:
                t = cutoff
                break
        else:
            events = [p["arrival_s"] for p in pending.values() if p["arrival_s"] > t + EPS]
            events.extend(receipts[d] for p in pending.values() for d in p["dependency_ids"]
                          if d in receipts and receipts[d] > t + EPS)
            events.extend(i["start_s"] for i in intervals if i["bps"] > 0 and i["start_s"] > t + EPS)
            if not events:
                break
            t = min(events)
    expire(cutoff)
    unstarted = []
    for p in sorted(pending.values(), key=lambda p: (p["arrival_s"], p["id"])):
        missing = [d for d in p["dependency_ids"] if d not in receipts or receipts[d] > cutoff + EPS]
        unstarted.append(dict(p, status="dependency_blocked" if missing else "queued", unmet_dependency_ids=missing))

    capacity = sum(max(0.0, min(cutoff, i["end_s"]) - max(0.0, i["start_s"])) * i["bps"] / 8
                   for i in intervals)
    capture_capacity = sum(max(0.0, min(horizon, i["end_s"]) - max(0.0, i["start_s"])) * i["bps"] / 8
                           for i in intervals)
    received = [p for p in transmissions if p["received_s"] is not None]
    serialized_rows = [p for p in transmissions if p["serialized_s"] is not None]
    # Direct capacity check, plus segments independently auditable by caller.
    tolerance = max(1e-7, capacity * 1e-12)
    if wire_sent > capacity + tolerance:
        raise AssertionError("serialized bytes exceeded available capacity")
    if sum(p["wire_bytes_sent"] for p in transmissions) > capacity + tolerance:
        raise AssertionError("transmission accounting exceeded capacity")
    return {
        "config": {"horizon_s": horizon, "finish_tail_s": cutoff-horizon, "observation_end_s": cutoff,
                   "overhead_bytes": overhead_bytes, "propagation_s": propagation, "ttl_s": ttl,
                   "policy": policy, "ttl_anchor": "arrival_s", "link_intervals": intervals,
                   "model": "causal_nonpreemptive_resumable_v1", "interval_semantics": "[start,end)"},
        "transmissions": transmissions, "pending": unstarted,
        "expired": sorted(expired, key=lambda p: (p["expired_at_s"], p["id"])),
        "excluded": excluded,
        "accounting": {
            "capacity_bytes": capacity, "capture_horizon_capacity_bytes": capture_capacity,
            "wire_bytes_sent": wire_sent, "unused_capacity_bytes": max(0.0, capacity-wire_sent),
            "utilization": wire_sent/capacity if capacity else None,
            "input_packets": len(rows), "admitted_packets": len(rows)-len(excluded),
            "started_packets": len(transmissions), "serialized_packets": len(serialized_rows),
            "received_packets": len(received), "pending_packets": len(unstarted),
            "expired_packets": len(expired), "excluded_packets": len(excluded),
            "received_payload_bytes": sum(p["bytes"] for p in received),
            "serialized_wire_bytes": sum(p["wire_bytes"] for p in serialized_rows),
            "partial_wire_bytes": sum(p["wire_bytes_sent"] for p in transmissions if p["serialized_s"] is None),
            "stale_received_packets": sum(p["stale_at_receipt"] for p in received),
            "capacity_check_passed": True,
        },
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--packets", required=True)
    parser.add_argument("--link", required=True)
    parser.add_argument("--horizon-s", type=float, required=True)
    parser.add_argument("--finish-tail-s", type=float, default=0.0)
    parser.add_argument("--overhead-bytes", type=int, default=64)
    parser.add_argument("--propagation-s", type=float, default=0.25)
    parser.add_argument("--ttl-s", type=float, default=60.0)
    parser.add_argument("--policy", choices=["priority", "fifo"], default="priority")
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    output = Path(args.out)
    if output.exists():
        raise ValueError("output must be new")
    result = simulate(json.loads(Path(args.packets).read_text()), json.loads(Path(args.link).read_text()),
                      args.horizon_s, finish_tail_s=args.finish_tail_s, overhead_bytes=args.overhead_bytes,
                      propagation_s=args.propagation_s, ttl_s=args.ttl_s, policy=args.policy)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, indent=2, allow_nan=False)+"\n")


if __name__ == "__main__":
    main()
