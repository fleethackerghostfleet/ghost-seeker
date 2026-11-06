import copy

import pytest

from edgeml.eval import delivery


def event(sid="image", frame=1, t_s=0.0, kind="crop", region=None, size=10):
    form = {"kind": kind, "bytes": size}
    if region is not None:
        form["region"] = region
    return {"event": "open", "sighting_id": sid, "frame_idx": frame,
            "t_s": t_s, "value": 1.0, "forms": [form]}


@pytest.mark.parametrize("region,box,expected", [
    ([0, 0, 5, 10], [0, 0, 10, 10], True),
    ([0, 0, 4.999, 10], [0, 0, 10, 10], False),
    ([20, 20, 5, 5], [0, 0, 10, 10], False),
    (None, [95, 0, 10, 10], True),
    (None, [96, 0, 10, 10], False),
    (None, [0, 0, 0, 10], False),
])
def test_image_coverage_requires_at_least_half_the_gt_box(region, box, expected):
    assert delivery._covers(region, box, 100, 100) is expected


def test_budget_accounts_for_images_facts_and_their_unique_union_without_mutation():
    events = [event(region=[0, 0, 10, 10]), event("fact", kind="fact", size=2)]
    gt = {1: [(1, [0, 0, 10, 10])], 2: [(1, [40, 0, 10, 10])]}
    before = copy.deepcopy((events, gt))
    result = delivery.delivered_vs_budget(events, {"fact": 2}, gt, 100, 100,
                                          budgets={"none": 0, "fact": 2, "both": 12})
    both = result["budgets"]["both"]
    assert both == {"budget_bytes_per_window": 12, "objects_delivered_image": 1,
                    "objects_delivered_fact": 1, "objects_delivered_any": 2,
                    "frac_image": 0.5, "frac_fact": 0.5, "bytes_used": 12,
                    "bytes_per_delivered_object": 12.0, "n_forms_sent": 2}
    fact = result["budgets"]["fact"]
    assert fact["objects_delivered_image"] == 0
    assert fact["objects_delivered_fact"] == fact["objects_delivered_any"] == 1
    assert fact["bytes_used"] == 2 and fact["bytes_per_delivered_object"] is None
    assert result["budgets"]["none"]["n_forms_sent"] == 0
    assert result["n_gt_objects"] == 2 and result["n_windows"] == 1
    assert (events, gt) == before


def test_window_boundaries_reset_byte_budgets_but_do_not_double_count_objects():
    events = [event(t_s=0), event(t_s=59.99), event(t_s=60),
              {**event(t_s=5), "event": "close"}, {"t_s": 6, "forms": []}]
    result = delivery.delivered_vs_budget(events, {}, {1: [(1, [0, 0, 10, 10])]},
                                          100, 100, budgets={"link": 10}, duration_s=60)
    report = result["budgets"]["link"]
    assert result["n_windows"] == 2
    assert report["bytes_used"] == 20 and report["n_forms_sent"] == 2
    assert report["objects_delivered_image"] == report["objects_delivered_any"] == 1
    assert report["bytes_per_delivered_object"] == 20


@pytest.mark.parametrize("frame,labels,expected", [
    (2, [1, 3], 1), (1, [2], 1), (0, [1], 1), (2, [2], 1),
    (2, [3], 0), (2, [1], 0), (2, [4], 0), (2, [], 0),
])
def test_only_proven_one_frame_gt_gaps_are_tolerated(frame, labels, expected):
    result = delivery.delivered_vs_budget([event(frame=frame)], {},
                                          {1: [(fr, [0, 0, 10, 10]) for fr in labels]},
                                          100, 100, budgets={"link": 10})
    assert result["budgets"]["link"]["objects_delivered_image"] == expected


def test_best_frame_and_zero_object_id_are_used_and_unmatched_facts_are_ignored():
    image = {**event(frame=99), "best_frame_idx": 2}
    events = [image, event("matched", kind="fact", size=1), event("unknown", kind="fact", size=1)]
    result = delivery.delivered_vs_budget(events, {"matched": 0}, {0: [(2, [0, 0, 10, 10])]},
                                          100, 100, budgets={"link": 12})
    report = result["budgets"]["link"]
    assert report["objects_delivered_image"] == report["objects_delivered_fact"] == 1
    assert report["objects_delivered_any"] == 1 and report["n_forms_sent"] == 3


def test_empty_delivery_uses_default_budgets_and_has_no_undefined_ratios():
    result = delivery.delivered_vs_budget([], {}, {}, 100, 100)
    assert result["n_windows"] == 1 and result["n_gt_objects"] == 0
    assert set(result["budgets"]) == set(delivery.BUDGETS_60S)
    for report in result["budgets"].values():
        assert report["bytes_used"] == report["n_forms_sent"] == 0
        assert report["frac_image"] is report["frac_fact"] is report["bytes_per_delivered_object"] is None


def test_selector_options_are_forwarded_and_selector_errors_propagate(monkeypatch):
    calls = []

    def select(events, budget, **kwargs):
        calls.append((events, budget, kwargs))
        raise ValueError("invalid selector configuration")

    monkeypatch.setattr(delivery, "greedy", select)
    events = [event()]
    options = {"ladder_decay": (1, 0.2), "max_bytes_per_object": 15}
    with pytest.raises(ValueError, match="invalid selector configuration"):
        delivery.delivered_vs_budget(events, {}, {}, 100, 100, budgets={"link": 10}, selector_kw=options)
    assert calls == [(events, 10, options)]


def test_pooling_sums_counts_and_bytes_and_weights_fractions_by_gt_population():
    first = delivery.delivered_vs_budget([event()], {}, {1: [(1, [0, 0, 10, 10])]},
                                         100, 100, budgets={"a": 10})
    second = delivery.delivered_vs_budget([event(kind="fact", size=2)], {"image": 2},
                                          {2: [(1, [0, 0, 10, 10])], 3: []},
                                          100, 100, budgets={"a": 10, "b": 2})
    before = copy.deepcopy((first, second))
    pooled = delivery.pool_delivery({"first": first, "second": second})
    assert pooled["n_gt_objects"] == 3
    assert list(pooled["budgets"]) == ["a", "b"]
    assert pooled["budgets"]["a"] == {
        "objects_delivered_image": 1, "objects_delivered_fact": 1, "objects_delivered_any": 2,
        "frac_image": 1 / 3, "frac_fact": 1 / 3, "frac_any": 2 / 3,
        "bytes_used": 12, "bytes_per_delivered_object": 12.0,
    }
    assert pooled["budgets"]["b"]["objects_delivered_fact"] == 1
    assert pooled["budgets"]["b"]["bytes_per_delivered_object"] is None
    assert (first, second) == before
    assert delivery.pool_delivery({}) == {"budgets": {}, "n_gt_objects": 0}
    empty = delivery.delivered_vs_budget([], {}, {}, 100, 100, budgets={"empty": 1})
    report = delivery.pool_delivery({"empty": empty})["budgets"]["empty"]
    assert report["frac_image"] is report["frac_fact"] is report["frac_any"] is None
