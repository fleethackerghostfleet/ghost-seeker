import copy
import json
import tempfile
import unittest
from pathlib import Path

from ghost_priority.policy import DEFAULT_POLICY, load_policy, rank_records, score_record, select_records


def record(key, novelty=0.5, size=100, fingerprint=None, detections=None):
    return {"id": key, "source": "clip", "payload_bytes": size,
            "signals": {"novelty": novelty, "quality": 1.0},
            "detections": detections, "fingerprint": fingerprint or [], "usable": True}


class PolicyTests(unittest.TestCase):
    def test_unknown_visual_content_can_be_selected_without_detection(self):
        rows = [record("unknown", novelty=1), record("known", novelty=0, detections=[{"label": "boat", "confidence": .8}])]
        self.assertEqual(select_records(rows, 100)[0]["id"], "unknown")
        self.assertIsNone(score_record(rows[0])["relevance"])
        self.assertEqual(score_record(record("none", detections=[]))["relevance"], 0)

    def test_class_override_changes_ranking_without_mutating_evidence(self):
        rows = [record("novel", novelty=.8), record("boat", novelty=0, detections=[{"label": "boat", "confidence": .8}])]
        before = copy.deepcopy(rows)
        result = select_records(rows, 100, {"class_weights": {"boat": 1}})
        self.assertEqual(result[0]["id"], "boat")
        self.assertEqual(rows, before)
        self.assertNotIn("rank", rows[0])

    def test_ground_truth_and_unknown_extra_fields_never_change_score(self):
        row = record("x")
        expected = score_record(row)
        row.update(labels=["boat"], ground_truth={"interesting": True}, interesting=True,
                   annotation={"confidence": 1, "label": "boat"})
        self.assertEqual(score_record(row), expected)

    def test_duplicate_penalty_is_relative_to_earlier_ranked_entries(self):
        rows = [record("a", 1, fingerprint=[.1, .1]), record("b", .99, fingerprint=[.1, .1]),
                record("c", .8, fingerprint=[.9, .9])]
        result = rank_records(rows)
        self.assertEqual([r["id"] for r in result], ["a", "c", "b"])
        self.assertEqual(result[-1]["nearest_prior_id"], "a")
        self.assertEqual(result[-1]["max_similarity"], 1)
        self.assertLess(result[-1]["final_score"], result[-1]["base_score"])
        self.assertEqual(result[-1]["marginal_score"], result[-1]["final_score"])

    def test_budget_never_exceeded_for_all_methods(self):
        rows = [record(str(i), i/9, size=71+i*19, detections=[]) for i in range(10)]
        for method in ("priority", "uniform", "novelty", "detector"):
            for budget in (0, 1, 70, 71, 231, 501, 9999):
                with self.subTest(method=method, budget=budget):
                    selected = select_records(rows, budget, method=method)
                    self.assertLessEqual(sum(r["payload_bytes"] for r in selected), budget)
                    self.assertEqual(len({r["id"] for r in selected}), len(selected))

    def test_full_ranking_size_independence_and_id_ties(self):
        rows = [record("a", .9, size=9000, fingerprint=[.2]),
                record("b", .9, size=1, fingerprint=[.2]),
                record("c", .8, size=100, fingerprint=[.9])]
        first = rank_records(rows)
        rows[0]["payload_bytes"], rows[1]["payload_bytes"] = 1, 999999
        second = rank_records(list(reversed(rows)))
        fields = ("id", "priority_rank", "rank", "base_score", "final_score", "max_similarity", "nearest_prior_id")
        self.assertEqual([{k: r[k] for k in fields} for r in first], [{k: r[k] for k in fields} for r in second])
        self.assertEqual(first[0]["id"], "a")

    def test_ranking_does_not_require_payload_accounting(self):
        rows = [record("a"), record("b")]
        for row in rows:
            del row["payload_bytes"]
        self.assertEqual([r["id"] for r in rank_records(rows)], ["a", "b"])

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
        with self.assertRaisesRegex(ValueError, "evaluated object evidence"):
            select_records([record("x")], 100, method="detector")
        rows = [record("a", 0, detections=[{"label": "boat", "confidence": .8}]),
                record("b", 1, detections=[{"label": "boat", "confidence": .3}])]
        self.assertEqual(select_records(rows, 100, method="detector")[0]["id"], "a")

    def test_direct_object_relevance_is_already_weighted_and_zero_is_evaluated(self):
        row = record("x", novelty=0, detections=[{"label": "boat", "confidence": 1}])
        row["object_relevance"] = .25
        self.assertEqual(score_record(row, {"class_weights": {"boat": 1}})["relevance"], .25)
        row["object_relevance"] = 0
        self.assertEqual(rank_records([row], method="detector")[0]["score"], 0)
        row["object_relevance"] = None
        self.assertEqual(score_record(row)["relevance"], DEFAULT_POLICY["default_object_weight"])
        # Display detections are not a second relevance provider when direct evidence exists.
        row["object_relevance"], row["detections"] = .7, [{"display_only": True}]
        self.assertEqual(score_record(row)["relevance"], .7)

    def test_invalid_direct_object_evidence_rejected(self):
        for value in (True, "0.5", float("nan"), float("inf"), -1, 1.01):
            with self.subTest(value=value), self.assertRaises(ValueError):
                score_record({**record("x"), "object_relevance": value})

    def test_canonical_visual_interest_and_legacy_cache_compatibility(self):
        old = record("x", novelty=.7)
        new = copy.deepcopy(old)
        new["signals"]["visual_interest"] = new["signals"].pop("novelty")
        self.assertEqual(score_record(old), score_record(new))
        new["signals"]["novelty"] = .1
        self.assertEqual(score_record(new)["visual_interest"], .7)
        self.assertEqual(score_record(new)["novelty"], .7)

    def test_legacy_policy_alias_and_conflicts(self):
        row = record("x")
        self.assertEqual(score_record(row, {"novelty_weight": .3}), score_record(row, {"visual_interest_weight": .3}))
        self.assertEqual(score_record(row, {"novelty_weight": .3, "visual_interest_weight": .3}), score_record(row, {"visual_interest_weight": .3}))
        with self.assertRaisesRegex(ValueError, "conflicting"):
            score_record(row, {"novelty_weight": .3, "visual_interest_weight": .4})
        for removed in ("byte_cost_exponent", "coverage_fraction"):
            with self.subTest(removed=removed), self.assertRaisesRegex(ValueError, "removed policy keys"):
                score_record(row, {removed: 0})

    def test_detector_and_visual_only_methods_ignore_quality_and_size(self):
        rows = [record("a", .7, size=9999, detections=[]), record("b", .6, size=1, detections=[])]
        rows[0]["signals"]["quality"] = 0
        self.assertEqual([r["id"] for r in rank_records(rows, method="novelty")], ["a", "b"])
        rows[0]["object_relevance"], rows[1]["object_relevance"] = .8, .7
        self.assertEqual([r["id"] for r in rank_records(rows, method="detector")], ["a", "b"])

    def test_reranking_does_not_retain_old_selection_claims(self):
        row = record("x")
        row.update(selection_rank=99, selection_lane="coverage", priority_rank=77)
        result = rank_records([row])[0]
        self.assertNotIn("selection_rank", result)
        self.assertNotIn("selection_lane", result)
        self.assertEqual(result["priority_rank"], 1)
        self.assertEqual(row["selection_rank"], 99)

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
