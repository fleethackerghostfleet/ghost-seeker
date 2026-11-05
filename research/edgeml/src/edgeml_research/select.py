"""Reference selector for evaluation (CONTRACTS §5b): greedy knapsack on value per byte.

Each open/update event is an item with a ladder of `forms` (fact → crop → thumb → full, cheapest first; two crop
qualities are two steps). An item's cheaper forms are always sent before its expensive ones. The gain of the k-th
form delivered *for the same object* (sighting_id) is `value × ladder_decay[k]` (literature.md §2.4: diminishing
returns per object so bytes spread across contacts before one contact is upgraded), and `max_bytes_per_object`
caps what a single object may consume in the window (so one `full` cannot eat a 72 KB window).
This is evaluation glue, not a scheduler: the Go edge already ranks `priority × ladder_weight / bytes`.
"""
from __future__ import annotations

import heapq
from typing import Iterable, Optional, Sequence

LADDER_ORDER = {"fact": 0, "crop": 1, "thumb": 2, "full": 3}
DEFAULT_DECAY = (1.0, 0.5, 0.25, 0.1)


def _ladder(forms: list[dict]) -> list[dict]:
    """Contract order (fact, crop, thumb, full), then bytes ascending; drop steps that are not strictly larger."""
    lad = sorted((f for f in forms if f.get("bytes", 0) > 0 or f.get("kind") == "fact"),
                 key=lambda f: (LADDER_ORDER.get(f.get("kind"), 9), f.get("bytes", 0)))
    out: list[dict] = []
    for f in lad:
        if out and f["bytes"] <= out[-1]["bytes"] and f["kind"] == out[-1]["kind"]:
            continue
        out.append(f)
    return out


def _decay(k: int, decay: Optional[Sequence[float]]) -> float:
    if not decay:
        return 1.0
    return float(decay[min(k, len(decay) - 1)])


def greedy(events: Iterable[dict], budget_bytes: int, ladder_decay: Optional[Sequence[float]] = DEFAULT_DECAY,
           max_bytes_per_object: Optional[int] = None) -> dict:
    """events: dicts with `event`, `sighting_id`, `value`, `forms`. Returns
    {"selected": [{event_idx, sighting_id, kind, bytes, quality, gain, form}], "bytes_used", "budget_bytes",
     "n_items", "n_items_touched", "n_objects_touched"}."""
    evl = events if isinstance(events, list) else list(events)
    items = []  # (event_idx, sighting_id, value, ladder)
    for i, ev in enumerate(evl):
        if ev.get("event") == "close" or not ev.get("forms"):
            continue
        lad = _ladder(ev["forms"])
        if lad:
            items.append((i, ev.get("sighting_id") or f"ev{i}", float(ev.get("value", 0.5)), lad))

    sent_forms: dict[str, int] = {}   # per object: number of forms already delivered (decay index)
    sent_bytes: dict[str, int] = {}
    heap: list = []

    def push(idx: int, li: int) -> None:
        _i, sid, v, lad = items[idx]
        k = sent_forms.get(sid, 0)
        gain = v * _decay(k, ladder_decay)
        heapq.heappush(heap, (-(gain / max(1, lad[li]["bytes"])), idx, li, k, gain))

    for idx in range(len(items)):
        push(idx, 0)

    used = 0
    chosen: list[dict] = []
    touched_items: set[int] = set()
    while heap:
        _negr, idx, li, k_at_push, gain = heapq.heappop(heap)
        i, sid, v, lad = items[idx]
        if sent_forms.get(sid, 0) != k_at_push:   # another form of this object went out since; re-rate
            push(idx, li)
            continue
        f = lad[li]
        b = int(f["bytes"])
        if used + b > budget_bytes:
            continue  # this and all more expensive forms of the item are dropped
        if max_bytes_per_object is not None and sent_bytes.get(sid, 0) + b > max_bytes_per_object and f["kind"] != "fact":
            continue  # byte cap per object (the fact is always allowed so the contact itself is never silenced)
        used += b
        sent_bytes[sid] = sent_bytes.get(sid, 0) + b
        sent_forms[sid] = sent_forms.get(sid, 0) + 1
        touched_items.add(idx)
        chosen.append({"event_idx": i, "sighting_id": sid, "kind": f["kind"], "bytes": b,
                       "quality": f.get("quality", 0), "gain": float(gain), "form": f})
        if li + 1 < len(lad):
            push(idx, li + 1)
    return {"selected": chosen, "bytes_used": used, "budget_bytes": int(budget_bytes), "n_items": len(items),
            "n_items_touched": len(touched_items), "n_objects_touched": len(sent_forms)}
