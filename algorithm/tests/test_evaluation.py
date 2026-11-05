import unittest

from ghost_priority.evaluation import evaluate_selection


def row(key):
    return {"id": key, "source": "clip", "payload_bytes": 100}


class EvaluationTests(unittest.TestCase):
    def test_missing_labels_do_not_become_negative_examples(self):
        rows = [row(x) for x in ("positive", "negative", "unreviewed")]
        labels = {"positive": {"interesting": True}, "negative": {"interesting": False}}
        result = evaluate_selection(rows, [rows[0], rows[2]], labels)
        self.assertEqual(result["interesting_image_recall"], 1)
        self.assertEqual(result["reviewed_interesting_image_recall"], 1)
        self.assertEqual(result["interesting_image_precision_reviewed"], 1)
        self.assertEqual(result["selected_review_fraction"], .5)
        self.assertEqual(result["selected_reviewed_images"], 1)

    def test_no_positive_denominator_returns_null(self):
        result = evaluate_selection([row("x")], [], {"x": {"interesting": False}})
        self.assertIsNone(result["interesting_image_recall"])
        self.assertIsNone(result["interesting_event_recall"])
        self.assertIsNone(result["interesting_image_precision_reviewed"])

    def test_class_coverage_does_not_require_interest_annotation(self):
        rows = [row("a"), row("b")]
        result = evaluate_selection(rows, [rows[0]], {"a": {"classes": ["boat"]}, "b": {"classes": ["boat"]}})
        self.assertEqual(result["per_class"]["boat"]["image_recall"], .5)
        self.assertEqual(result["reviewed_images"], 0)
        self.assertIsNone(result["interesting_image_recall"])

    def test_events_count_once_even_if_multiple_frames_selected(self):
        rows = [row(x) for x in "abc"]
        labels = {"a": {"interesting": True, "event_id": "one"},
                  "b": {"interesting": True, "event_id": "one"},
                  "c": {"interesting": True, "event_id": "two"}}
        result = evaluate_selection(rows, rows[:2], labels)
        self.assertEqual(result["interesting_event_recall"], .5)
        self.assertEqual(result["interesting_events"], 2)
        self.assertEqual(result["selected_interesting_events"], 1)

    def test_unrelated_labels_are_counted_but_not_in_denominators(self):
        result = evaluate_selection([row("x")], [row("x")], {"outside": {"interesting": True}})
        self.assertEqual(result["label_rows_outside_pool"], 1)
        self.assertIsNone(result["interesting_image_recall"])

    def test_no_labels_no_accuracy_claim(self):
        result = evaluate_selection([row("x")], [row("x")])
        self.assertEqual(result["label_status"], "not_provided")
        self.assertEqual(result["selected_bytes"], 100)
        self.assertIsNone(result["interesting_image_recall"])

    def test_unknown_selection_duplicate_ids_and_mismatched_bytes_fail(self):
        for selected in ([row("other")], [row("x"), row("x")], [{"id": "x", "payload_bytes": 20}]):
            with self.subTest(selected=selected), self.assertRaises(ValueError):
                evaluate_selection([row("x")], selected)

    def test_invalid_boolean_labels_fail_instead_of_coercing_strings(self):
        with self.assertRaises(ValueError):
            evaluate_selection([row("x")], [], {"x": {"interesting": "false"}})


if __name__ == "__main__":
    unittest.main()
