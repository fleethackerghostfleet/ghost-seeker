import unittest

from exact_cluster_bootstrap import exact_interval


class ExactClusterBootstrapTests(unittest.TestCase):
    def test_two_group_known_distribution(self):
        # Ordered draws (a,a),(a,b),(b,a),(b,b) have ratios0,.5,.5,1.
        result = exact_interval([0, 1], [1, 1])
        self.assertEqual((result['low'], result['high']), (0.0, 1.0))
        self.assertEqual((result['validmass'], result['groupcount'], result['vectorcount']), (1.0, 2, 3))
        self.assertEqual(result['zero_effect_mass_given_valid_denominator'], .25)

    def test_zero_denominator_draws_conditioned_out(self):
        result = exact_interval([0, 1], [0, 1])
        self.assertEqual((result['low'], result['high']), (1.0, 1.0))
        self.assertEqual((result['validmass'], result['validvectorcount']), (.75, 2))
        self.assertIsNone(exact_interval([0, 0], [0, 0]))
        self.assertIsNone(exact_interval([], []))

    def test_constant_effect_equal_vectors_permutations_and_scale(self):
        result = exact_interval([-1, -2, -3], [2, 4, 6])
        self.assertEqual((result['low'], result['high']), (-.5, -.5))
        self.assertEqual(result, exact_interval([-1, -2, -3], [2, 4, 6]))
        self.assertEqual(result, exact_interval([-3, -1, -2], [6, 2, 4]))
        self.assertEqual(result, exact_interval([-2, -4, -6], [4, 8, 12]))
        result['low'] = 99
        self.assertEqual(exact_interval([-1, -2, -3], [2, 4, 6])['low'], -.5)

    def test_regression_near_monte_carlo_tail_discontinuity(self):
        result = exact_interval([0, 0, -3, 0, 0, -1, -7, 0], [1, 0, 19, 1, 3, 4, 35, 1])
        self.assertEqual(result['vectorcount'], 6435)
        self.assertEqual(result['validvectorcount'], 6434)
        self.assertAlmostEqual(result['low'], -29 / 147)
        self.assertAlmostEqual(result['high'], -1 / 17)
        self.assertAlmostEqual(result['validmass'], 1 - 1 / 8**8)

    def test_bad_inputs_rejected(self):
        for differences, denominators in [([0], []), ([0], [-1]), ([1], [0]), ([True], [1]), ([1.0], [1])]:
            with self.assertRaises(ValueError):
                exact_interval(differences, denominators)


if __name__ == '__main__':
    unittest.main()
