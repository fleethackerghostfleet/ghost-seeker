"""Coverage evaluation on explicit review labels; never infer missing negatives.

Class metrics measure whether labelled images were transmitted, not detection AP,
localization accuracy, or recognition accuracy. Event recall covers only supplied
positive event IDs, not events that were never annotated.
"""
from __future__ import annotations

from collections import Counter, defaultdict

from .policy import validate_records


def _ratio(num, den):
    return num / den if den else None


def evaluate_selection(records, selected, labels=None):
    records, selected = validate_records(records), validate_records(selected)
    by_id = {r["id"]: r for r in records}
    chosen = {r["id"] for r in selected}
    for row in selected:
        if row["id"] not in by_id:
            raise ValueError(f"selected id is not in candidate records: {row['id']}")
        if row["payload_bytes"] != by_id[row["id"]]["payload_bytes"]:
            raise ValueError("selected and candidate payload sizes must match")
    pool_bytes = sum(r["payload_bytes"] for r in records)
    selected_bytes = sum(r["payload_bytes"] for r in selected)
    metrics = {
        "candidate_count": len(records), "selected_count": len(selected),
        "candidate_bytes": pool_bytes, "selected_bytes": selected_bytes,
        "byte_fraction": _ratio(selected_bytes, pool_bytes),
        "image_fraction": _ratio(len(selected), len(records)),
        "selected_by_source": dict(Counter(str(r.get("source", "")) for r in selected)),
        "reviewed_interesting_image_recall": None,
        "interesting_image_recall": None,
        "interesting_image_precision_reviewed": None,
        "interesting_event_recall": None,
        "reviewed_images": 0, "interesting_images": 0,
        "selected_reviewed_images": 0, "selected_interesting_images": 0,
        "selected_review_fraction": None,
        "label_rows_outside_pool": 0, "per_class": {},
        "label_status": "not_provided" if labels is None else "provided",
    }
    if labels is None:
        return metrics
    if not isinstance(labels, dict):
        raise ValueError("labels must map image IDs to review objects")
    reviewed, positive, event_members, classes = set(), set(), defaultdict(set), defaultdict(set)
    for image_id, label in labels.items():
        if not isinstance(label, dict):
            raise ValueError("each label must be an object")
        interest = label.get("interesting")
        if interest is not None and not isinstance(interest, bool):
            raise ValueError("interesting must be a boolean or null")
        names = label.get("classes", [])
        if not isinstance(names, list) or any(not isinstance(n, str) or not n for n in names):
            raise ValueError("classes must be a list of nonempty strings")
        event = label.get("event_id")
        if event is not None and (not isinstance(event, str) or not event):
            raise ValueError("event_id must be a nonempty string or null")
        if image_id not in by_id:
            metrics["label_rows_outside_pool"] += 1
            continue
        if interest is not None:
            reviewed.add(image_id)
        if interest is True:
            positive.add(image_id)
            if event is not None:
                event_members[event].add(image_id)
        for name in names:
            classes[name].add(image_id)
    selected_reviewed = reviewed & chosen
    selected_positive = positive & chosen
    recall = _ratio(len(selected_positive), len(positive))
    metrics.update({
        "reviewed_interesting_image_recall": recall,
        "interesting_image_recall": recall,
        "interesting_image_precision_reviewed": _ratio(len(selected_positive), len(selected_reviewed)),
        "reviewed_images": len(reviewed), "interesting_images": len(positive),
        "selected_reviewed_images": len(selected_reviewed),
        "selected_interesting_images": len(selected_positive),
        "selected_review_fraction": _ratio(len(selected_reviewed), len(chosen)),
        "interesting_events": len(event_members),
        "selected_interesting_events": sum(bool(members & chosen) for members in event_members.values()),
        "interesting_event_recall": _ratio(sum(bool(members & chosen) for members in event_members.values()), len(event_members)),
        "per_class": {name: {"available_labelled_images": len(ids), "selected_labelled_images": len(ids & chosen), "image_recall": _ratio(len(ids & chosen), len(ids))} for name, ids in sorted(classes.items())},
    })
    return metrics
