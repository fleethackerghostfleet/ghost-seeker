"""Portable synthetic integration checks for the frozen experiment CLI."""
import copy,json,os,subprocess,sys,tempfile,unittest
from pathlib import Path
HERE=Path(__file__).resolve().parent
LOCAL_BASE=Path(__file__).resolve().parents[4]/'algorithm'
BASELINE=Path(os.environ.get('GHOST_BASELINE_PATH',str(HERE.parent/'algorithm' if (HERE.parent/'algorithm').is_dir() else LOCAL_BASE)))
CANDIDATE=Path(os.environ.get('GHOST_CANDIDATE_MODULE',str(HERE.parent/'candidates/candidate_methods.py')))

class LockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import cv2,numpy as np
        cls.temp=tempfile.TemporaryDirectory();cls.base=Path(cls.temp.name)
        cls.data=cls.base/'data';seq=cls.data/'synthetic';(seq/'img1').mkdir(parents=True);(seq/'gt').mkdir()
        image=np.dstack([np.tile(np.arange(40,dtype=np.uint8)*5,(30,1))]*3)
        for f in (1,6):cv2.imwrite(str(seq/'img1'/f'{f:06d}.jpg'),image)
        (seq/'gt/gt.txt').write_text('1,1,4,4,8,8,1,1,1\n6,1,4,4,8,8,1,1,1\n')
        (seq/'gt/labels.json').write_text(json.dumps({'classes':{'1':'synthetic-marker'},'label_source':'synthetic'}))
        (cls.data/'index.jsonl').write_text(json.dumps({'seq_id':'synthetic','split':'dev','fps':5,'n_frames':6,'upstream':'synthetic-only'})+'\n')
        cls.out=cls.base/'dev'
        r=subprocess.run(cls.command(cls.out),capture_output=True,text=True)
        if r.returncode:raise RuntimeError(r.stdout+r.stderr)
        cls.lock=json.loads((cls.out/'lock.json').read_text())
    @classmethod
    def tearDownClass(cls):cls.temp.cleanup()
    @classmethod
    def command(cls,out,lock=None):
        cmd=[sys.executable,str(HERE/'run_benchmark.py'),'--data-root',str(cls.data),
          '--baseline-path',str(BASELINE),'--candidate-module',str(CANDIDATE),
          '--split','test' if lock else 'dev','--threads','0','--out',str(out),'--methods','color,phash,color-phash,no_similarity,uniform']
        if lock:cmd+=['--lock',str(lock)]
        return cmd
    def test_changed_lock_values_rejected_before_test_access(self):
        for key in ('harness_sha256','candidate_sha256','index_sha256','baseline_source_sha256','model_sha256','sample_stride','payload_size','requested_threads','candidate_configs'):
            with self.subTest(key=key),tempfile.TemporaryDirectory() as d:
                changed=copy.deepcopy(self.lock);value=changed[key]
                changed[key]=({'changed':'value'} if isinstance(value,dict) else value+1 if isinstance(value,int) else 'different')
                lock=Path(d)/'lock.json';lock.write_text(json.dumps(changed));out=Path(d)/'result'
                r=subprocess.run(self.command(out,lock),capture_output=True,text=True)
                self.assertNotEqual(r.returncode,0);self.assertIn('Configuration/code mismatch',r.stderr);self.assertFalse(out.exists())
    def test_no_lock_rejected(self):
        r=subprocess.run([sys.executable,str(HERE/'run_benchmark.py'),'--data-root','does-not-exist','--baseline-path','does-not-exist','--candidate-module','does-not-exist','--split','test','--out','unused'],capture_output=True,text=True)
        self.assertNotEqual(r.returncode,0);self.assertIn('requires --lock',r.stderr)
    def test_first_annotation_capture_across_pool_boundary(self):
        import cv2,numpy as np
        with tempfile.TemporaryDirectory() as d:
            base=Path(d);data=base/'data';seq=data/'boundary';(seq/'img1').mkdir(parents=True);(seq/'gt').mkdir()
            image=np.dstack([np.tile(np.arange(40,dtype=np.uint8)*5,(30,1))]*3)
            for f in range(1,305,5):cv2.imwrite(str(seq/'img1'/f'{f:06d}.jpg'),image)
            (seq/'gt/gt.txt').write_text('300,1,4,4,8,8,1,1,1\n301,1,4,4,8,8,1,1,1\n')
            (seq/'gt/labels.json').write_text(json.dumps({'1':'synthetic-marker','label_source':'synthetic'}))
            (data/'index.jsonl').write_text(json.dumps({'seq_id':'boundary','split':'dev','fps':5,'n_frames':304,'upstream':'synthetic-boundary'})+'\n')
            out=base/'result';cmd=self.command(out);cmd[cmd.index('--data-root')+1]=str(data);cmd[cmd.index('--methods')+1]='uniform'
            r=subprocess.run(cmd,capture_output=True,text=True);self.assertEqual(r.returncode,0,r.stdout+r.stderr)
            selections=[json.loads(line) for line in (out/'selections.jsonl').read_text().splitlines()]
            first=next(row for row in selections if row['window']==0 and row['bandwidth_bps']==9600)
            self.assertEqual(first['first_annotation_events'],['1'])
            self.assertEqual(first['selected_first_annotation_events_2s'],['1'])
            self.assertEqual(first['sampled_first_annotation_events_2s'],['1'])
            self.assertEqual(first['track_classes'],{'1':['synthetic-marker']})

if __name__=='__main__':unittest.main()
