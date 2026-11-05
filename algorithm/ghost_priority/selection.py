"""Apply a byte allowance to a full ranking without rescoring evidence.

Payloads are indivisible. Oversized records are skipped, not deleted or re-ranked;
the caller retains the complete ranking/evidence for later scheduling. This module
does not provide a persistent queue, transport pacing, or acknowledgement handling.
"""
from __future__ import annotations

import copy

from .policy import validate_records


def select_ranked(ranked_records, budget_bytes):
    """Walk a consecutive complete ranking, selecting each whole payload that fits.

    priority_rank and its rank alias remain unchanged. selection_rank records
    position within this subset. Budget cannot change scores or similarity.
    No image-analysis dependencies are imported.
    """
    if isinstance(budget_bytes, bool) or not isinstance(budget_bytes, int) or budget_bytes < 0:
        raise ValueError("budget_bytes must be a nonnegative integer")
    records = validate_records(ranked_records)
    for expected, record in enumerate(records, 1):
        priority = record.get("priority_rank")
        if isinstance(priority, bool) or not isinstance(priority, int) or priority != expected:
            raise ValueError("ranked_records must be in consecutive priority_rank order starting at 1")
        alias = record.get("rank", priority)
        if isinstance(alias, bool) or not isinstance(alias, int) or alias != priority:
            raise ValueError("rank must equal priority_rank when supplied")
    selected, spent = [], 0
    for record in records:
        if record.get("usable") is False:
            continue
        size = record["payload_bytes"]
        if spent + size <= budget_bytes:
            row = copy.deepcopy(record)
            row["selection_rank"] = len(selected) + 1
            selected.append(row)
            spent += size
    return selected
