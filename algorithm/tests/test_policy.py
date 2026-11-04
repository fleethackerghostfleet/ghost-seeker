import copy
import json
import tempfile
import unittest
from pathlib import Path

from ghost_priority.policy import DEFAULT_POLICY, load_policy, score_record, select_records


def record(key, novelty=0.5, size=100, fingerprint=None, detections=None):
    return {"id": key, "source": "clip", "payload_bytes": size,
            "signals": {"novelty": novelty, "quality": 1.0},
            "detections": detections, "fingerprint": fingerprint or [], "usable": True}


class PolicyTests(unittest.TestCase):
    def test_unknown_visual_content_can_be_selected_without_detection(self):
        rows = [record("unknown", novelty=1), record("known", novelty=0, detections=[{"label": "boat", "confidence": .8}])]
        self.assertEqual(select_records(rows, 100, {"coverage_fraction": 0})[0]["id"], "unknown")
        self.assertIsNone(score_record(rows[0])["relevance"])
        self.assertEqual(score_record(record("none", detections=[]))["relevance"], 0)

    def test_class_override_changes_ranking_without_mutating_evidence(self):
        rows = [record("novel", novelty=.8), record("boat", novelty=0, detections=[{"label": "boat", "confidence": .8}])]
        before = copy.deepcopy(rows)
        result = select_records(rows, 100, {"class_weights": {"boat": 1}, "coverage_fraction": 0})
        self.assertEqual(result[0]["id"], "boat")
        self.assertEqual(rows, before)
        self.assertNotIn("rank", rows[0])

    def test_ground_truth_and_unknown_extra_fields_never_change_score(self):
        row = record("x")
        expected = score_record(row)
        row.update(labels=["boat"], ground_truth={"interesting": True}, interesting=True,
                   annotation={"confidence": 1, "label": "boat"})
        self.assertEqual(score_record(row), expected)

    def test_duplicate_penalty_is_relative_to_selected_set(self):
        rows = [record("a", 1, fingerprint=[.1, .1]), record("b", .99, fingerprint=[.1, .1]),
                record("c", .8, fingerprint=[.9, .9])]
        result = select_records(rows, 200, {"coverage_fraction": 0})
        self.assertEqual([r["id"] for r in result], ["a", "c"])

    def test_budget_never_exceeded_for_all_methods(self):
        rows = [record(str(i), i/9, size=71+i*19, detections=[]) for i in range(10)]
        for method in ("priority", "uniform", "novelty", "detector"):
            for budget in (0, 1, 70, 71, 231, 501, 9999):
                with self.subTest(method=method, budget=budget):
                    selected = select_records(rows, budget, method=method)
                    self.assertLessEqual(sum(r["payload_bytes"] for r in selected), budget)
                    self.assertEqual(len({r["id"] for r in selected}), len(selected))

    def test_coverage_reserve_falls_back_when_no_whole_item_fits(self):
        rows = [record("a", .2), record("b", 1)]
        selected = select_records(rows, 100)
        self.assertEqual(selected[0]["id"], "b")
        self.assertEqual(selected[0]["selection_lane"], "priority")

    def test_uniform_is_deterministic_and_ignores_evidence(self):
        rows = [record(str(i), i/9) for i in range(10)]
        first = [r["id"] for r in select_records(rows, 300, method="uniform")]
        for row in rows:
            row["signals"]["novelty"] = 1 - row["signals"]["novelty"]
        second = [r["id"] for r in select_records(list(reversed(rows)), 300, method="uniform")]
        self.assertEqual(first, second)

    def test_unusable_records_excluded_everywhere(self):
        rows = [record("a", 1, detections=[]), record("b", .2, detections=[])]
        rows[0]["usable"] = False
        for method in ("priority", "uniform", "novelty", "detector"):
            self.assertEqual([r["id"] for r in select_records(rows, 1000, method=method)], ["b"])

    def test_detector_requires_evaluation_and_ignores_novelty(self):
        with self.assertRaisesRegex(ValueError, "evaluated detections"):
            select_records([record("x")], 100, method="detector")
        rows = [record("a", 0, detections=[{"label": "boat", "confidence": .8}]),
                record("b", 1, detections=[{"label": "boat", "confidence": .3}])]
        self.assertEqual(select_records(rows, 100, method="detector")[0]["id"], "a")

    def test_invalid_configuration_and_records_fail(self):
        for overrides in ({"novelty_weight": float("nan")}, {"coverage_fraction": -1},
                          {"duplicate_distance": 0}, {"class_weights": {"boat": 2}}, {"typo": 1}):
            with self.subTest(overrides=overrides), self.assertRaises(ValueError):
                score_record(record("x"), overrides)
        for rows in ([record("x"), record("x")], [record("x", size=0)], [record("x", size=True)]):
            with self.assertRaises(ValueError):
                select_records(rows, 100)

    def test_file_overrides_do_not_modify_defaults(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "policy.json"
            path.write_text(json.dumps({"class_weights": {"boat": .9}}))
            result = load_policy(path)
        self.assertEqual(result["class_weights"], {"boat": .9})
        result["class_weights"]["boat"] = 0
        self.assertEqual(DEFAULT_POLICY["class_weights"], {})


if __name__ == "__main__":
    unittest.main()
