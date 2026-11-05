import unittest
import numpy as np
from benchmark_scale import greedy_rank,generated_frames
from candidate_methods import get_methods


class MatrixMethod:
    def __init__(self,matrix,slack=0.):self.matrix,self.slack=matrix,slack
    def compare(self,a,b):return float(self.matrix[a,b])
    def upper_bound(self,a,b):return min(1.,float(self.matrix[a,b])+self.slack)


class PruningTests(unittest.TestCase):
    def test_random_scores_exact_parity(self):
        for seed in range(12):
            rng=np.random.default_rng(seed)
            values=rng.uniform(0,1,(35,35));matrix=(values+values.T)/2
            bases=rng.uniform(.1,.9,35).tolist()
            for slack in (0.,.2,1.):
                method=MatrixMethod(matrix,slack)
                plain,a=greedy_rank(method,list(range(35)),bases)
                pruned,b=greedy_rank(method,list(range(35)),bases,prune=True)
                self.assertEqual(plain,pruned)
                self.assertEqual(b['full_comparisons']+b['skipped_comparisons'],a['possible_pairs'])

    def test_ties_and_zero_scores_keep_nearest_identity(self):
        matrix=np.ones((12,12))*.7
        method=MatrixMethod(matrix)
        plain,a=greedy_rank(method,list(range(12)),[.5]*12)
        pruned,b=greedy_rank(method,list(range(12)),[.5]*12,prune=True)
        self.assertEqual(plain,pruned)
        self.assertEqual(plain[1]['nearest_prior_id'],'frame-000000')
        self.assertGreater(b['skipped_comparisons'],0)
        matrix.fill(0)
        zero,a=greedy_rank(method,list(range(12)),[0.]*12,prune=True)
        self.assertEqual(a['full_comparisons'],0)
        self.assertEqual([row['id'] for row in zero],[f'frame-{i:06d}' for i in range(12)])

    def test_real_candidate_bounds_and_parity(self):
        frames=list(generated_frames(10,7321));bases=np.linspace(.2,.9,10).tolist()
        for name,method in get_methods().items():
            descriptors=[method.prepare(frame) for frame in frames]
            for a,b in zip(descriptors,descriptors[1:]):
                self.assertLessEqual(method.compare(a,b),method.upper_bound(a,b),name)
            plain,a=greedy_rank(method,descriptors,bases)
            pruned,b=greedy_rank(method,descriptors,bases,prune=True)
            self.assertEqual(plain,pruned,name)


if __name__=='__main__':unittest.main()
