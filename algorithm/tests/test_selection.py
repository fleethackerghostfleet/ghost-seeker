import copy
import json
import unittest

from ghost_priority.policy import rank_records, select_records
from ghost_priority.selection import select_ranked


def record(key, score, size=100, fingerprint=None):
    return {"id": key, "object_relevance": score, "payload_bytes": size,
            "signals": {"visual_interest": 0, "quality": 1},
            "fingerprint": fingerprint or []}


class SelectionTests(unittest.TestCase):
    def test_same_ranking_reused_for_budgets_with_scores_unchanged(self):
        ranked = rank_records([record("a", 1, 500), record("b", .8, 200), record("c", .6, 100)])
        before = copy.deepcopy(ranked)
        for budget in (0, 99, 100, 200, 300, 500, 1000):
            for selected in select_ranked(ranked, budget):
                expected = before[selected["priority_rank"]-1]
                for key in ("id", "rank", "priority_rank", "score", "final_score", "max_similarity", "nearest_prior_id"):
                    self.assertEqual(selected[key], expected[key])
        self.assertEqual(ranked, before)

    def test_equal_sizes_select_top_n_prefix(self):
        ranked = rank_records([record(str(i), 1-i/10) for i in range(5)])
        for budget in (0, 99, 100, 250, 500, 999):
            selected = select_ranked(ranked, budget)
            self.assertEqual([r["id"] for r in selected], [r["id"] for r in ranked[:budget//100]])

    def test_oversized_is_skipped_and_original_ranks_preserved(self):
        ranked = rank_records([record("a", 1, 900), record("b", .8, 200), record("c", .6, 100)])
        selected = select_ranked(ranked, 300)
        self.assertEqual([r["id"] for r in selected], ["b", "c"])
        self.assertEqual([r["priority_rank"] for r in selected], [2, 3])
        self.assertEqual([r["rank"] for r in selected], [2, 3])
        self.assertEqual([r["selection_rank"] for r in selected], [1, 2])
        self.assertEqual([r["id"] for r in select_ranked(ranked, 1200)], ["a", "b", "c"])

    def test_duplicate_penalty_applies_even_when_prior_image_cannot_be_selected(self):
        ranked = rank_records([record("a", 1, 1000, [.1]), record("b", .99, 100, [.1]), record("c", .7, 100, [.9])])
        self.assertEqual([r["id"] for r in ranked], ["a", "c", "b"])
        selected = select_ranked(ranked, 100)
        self.assertEqual(selected[0]["id"], "c")
        self.assertEqual(ranked[-1]["nearest_prior_id"], "a")
        self.assertEqual(ranked[-1]["max_similarity"], 1)

    def test_json_roundtrip_and_compatibility_wrapper(self):
        records = [record("a", .8), record("b", .5)]
        ranked = json.loads(json.dumps(rank_records(records)))
        self.assertEqual(select_ranked(ranked, 100), select_records(records, 100))

    def test_zero_large_and_invalid_budgets(self):
        ranked = rank_records([record("a", .8), record("b", .5)])
        self.assertEqual(select_ranked(ranked, 0), [])
        self.assertEqual(len(select_ranked(ranked, 10**12)), 2)
        for budget in (True, -1, 100.0, float("nan")):
            with self.subTest(budget=budget), self.assertRaises(ValueError):
                select_ranked(ranked, budget)

    def test_invalid_rank_order_alias_and_bytes_fail(self):
        ranked = rank_records([record("a", .8), record("b", .5)])
        bad_cases = [list(reversed(ranked)), [ranked[1]], [ranked[0], ranked[0]]]
        for field, value in (("rank", 2), ("priority_rank", True), ("priority_rank", 1.0), ("payload_bytes", 0), ("payload_bytes", True)):
            bad = copy.deepcopy(ranked)
            bad[0][field] = value
            bad_cases.append(bad)
        for bad in bad_cases:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                select_ranked(bad, 1000)

    def test_unusable_never_selected_even_if_supplied_in_ranked_input(self):
        ranked = rank_records([record("a", .8), record("b", .5)])
        ranked[0]["usable"] = False
        self.assertEqual([r["id"] for r in select_ranked(ranked, 1000)], ["b"])


if __name__ == "__main__":
    unittest.main()
