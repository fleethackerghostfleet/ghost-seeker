import importlib.util,json,os,sys,tempfile,unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[4]
HERE=Path(__file__).resolve().parent
DEFAULT_BASE=ROOT/'algorithm'
sys.path.insert(0,os.environ.get('GHOST_BASELINE_PATH',str(DEFAULT_BASE)))
sys.path.insert(0,str(Path(__file__).resolve().parent))
from run_benchmark import rank_candidate, metric_sets, read_gt, check_split_groups
from ghost_priority.policy import rank_records
from ghost_priority.similarity import CachedSimilarity
from ghost_priority.selection import select_ranked

class Wrapper:
    name='wrapped-baseline'
    def __init__(self): self.engine=CachedSimilarity()
    def compare(self,a,b): return self.engine.compare(a,b)['score']

class BoundedWrapper(Wrapper):
    def upper_bound(self,a,b): return self.compare(a,b)

def record(i,signal=.5):
    return {'id':str(i),'source':'s','timestamp_s':i,'frame_index':i+1,'usable':True,'signals':{'visual_interest':signal,'quality':.8},'object_relevance':None,'detections':None,'fingerprint':[i%3/10]*192,'phash':{'version':'phash64-dct-v1','hex':f'{i%4:016x}','analysis_size':320},'payload_bytes':100,'width':100,'height':100,'payload_width':50,'payload_height':50}

class BenchTests(unittest.TestCase):
    def test_canonical_rank_parity_and_pruning(self):
        records=[record(i, .5 if i%2 else .8) for i in range(12)]
        original=rank_records(records)
        for m in (Wrapper(),BoundedWrapper()):
            descriptors={r['id']:m.engine.prepare(r) for r in records}
            ranked,_=rank_candidate(records,m,descriptors)
            for a,b in zip(original,ranked):
                for k in ('id','priority_rank','base_score','final_score','max_similarity','nearest_prior_id'):
                    self.assertEqual(a[k],b[k],k)
            for budget in (0,50,100,250,5000):
                self.assertEqual([r['id'] for r in select_ranked(original,budget)],[r['id'] for r in select_ranked(ranked,budget)])
    def test_annotation_fields_do_not_change_ranking(self):
        records=[record(i) for i in range(5)]
        orig=[r['id'] for r in rank_records(records)]
        for i,r in enumerate(records): r.update(gt_tracks=[999],interesting=i%2==0,annotation_label='not-model-input')
        self.assertEqual(orig,[r['id'] for r in rank_records(records)])
    def test_unsampled_positives_remain_in_denominator(self):
        rows=[record(0),record(2)]
        gt={1:[{'track':'a','class_id':'1','box':[1,1,10,10]}],2:[{'track':'b','class_id':'2','box':[1,1,20,20]}],3:[{'track':'a','class_id':'1','box':[1,1,10,10]}]}
        met=metric_sets(rows,[rows[0]],gt,0,3,1,{'a':1,'b':2},{'1':'boat','2':'person'})
        self.assertEqual(met['all_positive_tracks'],['a','b']);self.assertEqual(met['sampled_positive_tracks'],['a'])
        self.assertEqual(met['positive_track_window_coverage'],.5)
        self.assertEqual(met['sampled_positive_track_window_coverage'],1)
        empty=metric_sets(rows,[],{},0,3,1,{},{});self.assertIsNone(empty['positive_track_window_coverage'])
    def test_ignored_gt_not_positive(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'gt';p.write_text('1,1,0,0,10,10,0,1,1\n2,2,0,0,10,10,1,1,1\n')
            self.assertEqual(list(read_gt(p)),[2])
    def test_recording_split_leakage_rejected(self):
        with self.assertRaises(ValueError):check_split_groups([{'seq_id':'a','upstream':'same','split':'dev'},{'seq_id':'b','upstream':'same','split':'test'}])
        check_split_groups([{'seq_id':'a','upstream':'same','split':'test'},{'seq_id':'b','upstream':'same','split':'test'}])

if __name__=='__main__':unittest.main()
