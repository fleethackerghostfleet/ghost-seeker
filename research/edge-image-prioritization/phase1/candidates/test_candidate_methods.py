"""Behavioral tests for candidate contracts and controlled nuisance/change cases."""
import unittest
from pathlib import Path
import sys
import cv2
import numpy as np

from candidate_methods import get_methods,make_method,descriptor_nbytes


def scene():
    h,w=384,640
    y,x=np.mgrid[:h,:w]
    bgr=np.stack([100+.03*x+15*np.sin(y/13),130+.02*x+5*np.sin(y/11),150+.02*x],axis=-1).astype(np.uint8)
    bgr[:180]=np.stack([170+.02*x[:180],160+.03*x[:180],140+.02*x[:180]],axis=-1).astype(np.uint8)
    cv2.rectangle(bgr,(300,170),(345,184),(35,40,45),-1)
    return bgr


class CandidateTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cv2.setNumThreads(0)
        cls.image=scene()

    def test_identity_no_mutation_and_cache_immutable(self):
        for name,m in get_methods().items():
            original=self.image.copy()
            a=m.prepare(original)
            self.assertEqual(m.compare(a,a),1.,name)
            np.testing.assert_array_equal(original,self.image)
            self.assertFalse(a.local.flags.writeable)
            self.assertGreater(descriptor_nbytes(a),0)

    def test_invalid_images(self):
        for m in get_methods().values():
            for image in (None, np.zeros((3,3),np.uint8),np.zeros((2,2,3),np.float32),np.zeros((0,2,3),np.uint8)):
                with self.assertRaises(ValueError):
                    m.prepare(image)

    def test_config_and_descriptor_mismatch(self):
        a=make_method('local_color')
        b=make_method('local_color',local_size=64)
        with self.assertRaises(ValueError):
            a.compare(a.prepare(self.image),b.prepare(self.image))
        c=a.config;c['residual_scale']=9
        self.assertEqual(a.config['residual_scale'],.04)
        for kwargs in ({'tail_fraction':0},{'residual_scale':float('nan')},{'global_gate':1},{'bogus':1}):
            with self.assertRaises(ValueError):
                make_method('local_color',**kwargs)

    def test_small_local_marker_vetoes_global_similarity(self):
        changed=self.image.copy()
        cv2.rectangle(changed,(450,175),(458,183),(0,0,0),-1)
        for name,m in get_methods().items():
            out=m.compare_details(m.prepare(self.image),m.prepare(changed))
            self.assertGreater(out['global_similarity'],.9,name)
            self.assertLess(out['score'],out['global_similarity']*.5,name)

    def test_capped_by_baseline_and_finite(self):
        rng=np.random.default_rng(12)
        images=[self.image,np.clip(self.image.astype(np.int16)+20,0,255).astype(np.uint8),
                np.clip(self.image.astype(float)+rng.normal(0,2,self.image.shape),0,255).astype(np.uint8)]
        for name,m in get_methods().items():
            a=m.prepare(images[0])
            for image in images:
                out=m.compare_details(a,m.prepare(image))
                self.assertTrue(np.isfinite(out['score']),name)
                self.assertGreaterEqual(out['score'],0.)
                self.assertLessEqual(out['score'],out['global_similarity'])

    def test_global_descriptor_reproduces_baseline(self):
        sys.path.insert(0,str(Path(__file__).resolve().parents[4]/'algorithm'))
        from ghost_priority.similarity_features import describe
        color,phash=describe(self.image)
        a=make_method('local_color').prepare(self.image)
        self.assertEqual(tuple(color),a.color)
        self.assertEqual(int(phash['hex'],16),a.phash)

    def test_flat_frames_and_unrelated_aspect(self):
        for name,m in get_methods().items():
            a=m.prepare(np.full((32,64,3),120,np.uint8))
            b=m.prepare(np.full((64,32,3),120,np.uint8))
            self.assertEqual(m.compare(a,a),1.,name)
            self.assertEqual(m.compare(a,b),0.,name)
            if name=='registered_residual':
                self.assertFalse(m.compare_details(a,a)['registered'])

    def test_brightness_offset_does_not_claim_global_invariance(self):
        shifted=np.clip(self.image.astype(np.int16)+20,0,255).astype(np.uint8)
        for name in ('local_color','registered_residual'):
            m=make_method(name)
            out=m.compare_details(m.prepare(self.image),m.prepare(shifted))
            self.assertGreater(out['local_similarity'],.99)
            self.assertLess(out['score'],.5)

    def test_translation_registration_improves_local_residual(self):
        shifted=cv2.warpAffine(self.image,np.float32([[1,0,10],[0,1,3]]),(640,384),borderMode=cv2.BORDER_REFLECT)
        m=make_method('registered_residual')
        out=m.compare_details(m.prepare(self.image),m.prepare(shifted))
        self.assertTrue(out['registered'])
        self.assertEqual(out['shift_xy'],[-2,-1])
        self.assertLess(out['used_tail_residual'],out['raw_tail_residual']*.1)

    def test_large_shift_is_rejected(self):
        shifted=cv2.warpAffine(self.image,np.float32([[1,0,50],[0,1,24]]),(640,384),borderMode=cv2.BORDER_REFLECT)
        m=make_method('registered_residual',max_shift_fraction=.005)
        out=m.compare_details(m.prepare(self.image),m.prepare(shifted))
        self.assertFalse(out['registered'])
        self.assertEqual(out['raw_tail_residual'],out['used_tail_residual'])

    def test_repeatability_and_reverse_comparison(self):
        changed=self.image.copy();changed[160:168,450:458]=0
        for name,m in get_methods().items():
            a,b=m.prepare(self.image),m.prepare(changed)
            self.assertEqual(m.compare(a,b),m.compare(a,b),name)
            self.assertAlmostEqual(m.compare(a,b),m.compare(b,a),places=6,msg=name)


if __name__=='__main__':
    unittest.main()
