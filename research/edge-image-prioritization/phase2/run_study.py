"""Development-only causal payload study. Labels enter only after simulation."""
import argparse
from collections import defaultdict
import hashlib
import json
import math
from pathlib import Path
import statistics
import sys
import time

HERE = Path(__file__).resolve().parent
PACKAGE = HERE.parent
REPO = PACKAGE.parents[1]
BASELINE = REPO / 'algorithm'
sys.path[:0] = [str(BASELINE), str(HERE/'transport'), str(PACKAGE/'phase1/evaluation')]

METHODS = ['full_fifo', 'full_priority', 'full_density', 'q50_density',
           'overview640_density', 'overview320_density', 'predicted_detail16', 'progressive_density']
RATES = [9600, 64000, 256000, 2000000]
SCHEDULES = ['constant', 'outage20to40', 'bursty', 'bursty_offset']
CONFIG = dict(methods=METHODS, rates=RATES, schedules=SCHEDULES,
              batch_seconds=[1, 5], ttl_seconds=60, overhead_bytes=64,
              propagation_seconds=.25, finish_tail_seconds=0,
              detail_thresholds=[0, 8, 16, 32], containment_fraction=.9,
              scope='development exploratory; previous test set not reused',
              progressive_weights={'overview':1., 'crop':.5, 'full':.1})

def sha(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def save(p, obj): Path(p).write_text(json.dumps(obj, indent=2, allow_nan=False)+'\n')

def verify_inputs(data, formsdir, cache, baseline):
    index=[json.loads(x) for x in (data/'index.jsonl').read_text().splitlines() if x.strip()]
    dev={r['seq_id']:r for r in index if r['split']=='dev'}
    provenance=json.loads((cache/'provenance.json').read_text())
    old={r['id']:r['sha256'] for r in provenance['inputs'] if 'id' in r}
    annotations={r['sequence']:r for r in provenance['inputs'] if 'sequence' in r}
    assert sha(data/'index.jsonl')==provenance['index_sha256']
    records=json.loads((formsdir/'records-with-forms.json').read_text())
    manifest=json.loads((formsdir/'manifest.json').read_text())
    assert sha(cache/'records.json')==manifest['records_sha256']
    original_records={r['id']:r for r in json.loads((cache/'records.json').read_text())}
    baseline_lock=json.loads((cache/'lock.json').read_text())
    baseline_hashes={str(p.relative_to(baseline)):sha(p) for p in sorted((baseline/'ghost_priority').rglob('*.py'))}
    assert baseline_hashes==baseline_lock['baseline_source_sha256'], 'Baseline scorer changed'
    frozen=[]
    for r in records:
        assert r['source'] in dev, 'Only dev sources permitted'
        original=original_records[r['id']]
        assert {k:r[k] for k in original}==original, 'Cached evidence changed during encoding'
        assert all(k in original or k=='forms' or k.startswith('representation_') for k in r), 'Unexpected evidence field'
        p=data/r['source']/'img1'/f"{r['frame_index']:06d}.jpg"
        assert sha(p)==old[r['id']], 'Cached evidence/image mismatch'
        for f in r['forms']:
            p=formsdir/f['payload_path']
            assert p.stat().st_size==f['bytes'] and sha(p)==f['sha256']
    assert len(records)==len(old) and len({r['id'] for r in records})==len(records)
    for sid in dev:
        for name,key in [('gt.txt','gt_sha256'),('labels.json','labels_sha256')]:
            p=data/sid/'gt'/name
            assert sha(p)==annotations[sid][key], 'Annotation changed since first study'
            frozen.append({'path':str(p.relative_to(data)), 'sha256':sha(p)})
    return records,dev,dict(config=CONFIG, index_sha256=sha(data/'index.jsonl'),
        cached_records_sha256=sha(cache/'records.json'), forms_sha256=sha(formsdir/'records-with-forms.json'),
        annotations=frozen, baseline_sources=baseline_hashes,
        source_files={str(p.relative_to(PACKAGE)):sha(p) for p in sorted(HERE.rglob('*.py'))})

def make_link(duration, rate, schedule):
    out=[]
    for t in range(math.ceil(duration)):
        end=min(t+1,duration)
        factor=1
        if schedule=='outage20to40': factor=0 if 20<=t%60<40 else 1
        elif schedule=='bursty': factor=2 if t%10<5 else 0
        elif schedule=='bursty_offset': factor=2 if t%10>=5 else 0
        out.append(dict(start_s=float(t),end_s=float(end),bps=float(rate*factor)))
    return out

def choose_detail(record):
    forms=[f for f in record['forms'] if f['kind']!='crop']
    dets=record.get('detections') or []
    if not dets: return next(f for f in forms if f['form_id']=='overview_640_q80')
    acceptable=[f for f in forms if all(min(d['bbox_xywh'][2]*f['payload_width']/record['width'],
                 d['bbox_xywh'][3]*f['payload_height']/record['height'])>=16 for d in dets)]
    # This proxy cannot make a sub-16px source object recognizable.
    return min(acceptable,key=lambda f:(f['bytes'],f['form_id'])) if acceptable else next(f for f in forms if f['form_id']=='full_1280_q50')

def make_packets(records, method, batch_s, duration):
    from ghost_priority.policy import rank_records
    grouped=defaultdict(list)
    for r in records:
        if r.get('usable') is not False: grouped[int(r['timestamp_s']//batch_s)].append(r)
    packets=[]
    for bi,batch in sorted(grouped.items()):
        available=min((bi+1)*batch_s,duration)
        ranked=rank_records(batch)
        for r in ranked:
            forms={f['form_id']:f for f in r['forms']}
            if method in ['full_fifo','full_priority','full_density']: selected=[(forms['full_1280_q80'],1.,[])]
            elif method=='q50_density': selected=[(forms['full_1280_q50'],1.,[])]
            elif method=='overview640_density': selected=[(forms['overview_640_q80'],1.,[])]
            elif method=='overview320_density': selected=[(forms['overview_320_q50'],1.,[])]
            elif method=='predicted_detail16': selected=[(choose_detail(r),1.,[])]
            elif method=='progressive_density':
                base=forms['overview_320_q50']; baseid=r['id']+'/'+base['form_id']
                selected=[(base,1.,[])]+[(f,.5,[baseid]) for f in r['forms'] if f['kind']=='crop']
                selected.append((forms['full_1280_q50'],.1,[baseid]))
            else: raise ValueError(method)
            for form,weight,deps in selected:
                priority=r['final_score']*weight
                if method not in ['full_priority','full_fifo']: priority/=form['bytes']+CONFIG['overhead_bytes']
                packets.append(dict(id=r['id']+'/'+form['form_id'],frame_id=r['id'],source=r['source'],
                    arrival_s=float(available), capture_s=float(r['timestamp_s']), bytes=form['bytes'],
                    priority=priority, kind=form['kind'], dependency_ids=deps, form=form,
                    frame_index=r['frame_index'], image_width=r['width'],image_height=r['height'],
                    original_priority_rank=r['priority_rank']))
    return packets

def geometry_match(box, form, width, height, min_side):
    x,y,w,h=box
    # Score only the annotated area visible in the source image.
    x0=max(0.,x); y0=max(0.,y); x1=min(float(width),x+w); y1=min(float(height),y+h)
    if x1<=x0 or y1<=y0:return False
    rx,ry,rw,rh=form['roi']
    ix=max(0.,min(x1,rx+rw)-max(x0,rx)); iy=max(0.,min(y1,ry+rh)-max(y0,ry))
    contained=ix*iy/((x1-x0)*(y1-y0))
    short=min(ix*form['payload_width']/rw,iy*form['payload_height']/rh)
    return contained+1e-12>=CONFIG['containment_fraction'] and short+1e-12>=min_side

def evaluate(packets, simulation, gt, fps, duration):
    lookup={p['id']:p for p in packets}; first={}
    for frame,rows in sorted(gt.items()):
        for row in rows:first.setdefault(row['track'],(frame-1)/fps)
    output=[]
    for threshold in CONFIG['detail_thresholds']:
        received=defaultdict(list); possible=set(); ages=[]
        for p in packets:
            for a in gt.get(p['frame_index'],[]):
                if geometry_match(a['box'],p['form'],p['image_width'],p['image_height'],threshold): possible.add(a['track'])
        for tx in simulation['transmissions']:
            receipt=tx.get('received_s')
            if receipt is None or receipt>duration+1e-9:continue
            p=lookup[tx['id']];ages.append(receipt-p['capture_s'])
            for a in gt.get(p['frame_index'],[]):
                if geometry_match(a['box'],p['form'],p['image_width'],p['image_height'],threshold):
                    received[a['track']].append(dict(receipt_s=receipt,capture_s=p['capture_s'],packet_id=p['id']))
        delays={track:min(x['receipt_s'] for x in values)-first[track] for track,values in received.items()}
        output.append(dict(min_side_px=threshold,all_tracks=sorted(first),form_possible_tracks=sorted(possible),
            received_tracks=sorted(received),first_receipt_delay_s=delays,
            receipt_appearances=sum(len(v) for v in received.values()),
            receipts_within_s={str(t):sorted(k for k,v in delays.items() if v<=t) for t in [1,5,10,30,60]},
            matched_receipts=dict(received),packet_capture_age_s=ages))
    return output

def aggregate(rows):
    grouped=defaultdict(list)
    for r in rows:
        for status in ['all_status',r['gt_status']]:
            key=(r['method'],r['batch_s'],r['rate_bps'],r['schedule'],r['min_side_px'],status)
            grouped[key].append(r)
    out=[]
    for key,rr in sorted(grouped.items()):
        method,batch,rate,schedule,detail,status=key
        delays=[v for r in rr for v in r['first_receipt_delay_s'].values()]
        out.append(dict(method=method,batch_s=batch,rate_bps=rate,schedule=schedule,min_side_px=detail,status=status,
            sequences=len(rr),positive_sequences=sum(bool(r['all_tracks']) for r in rr),
            tracks_denominator=sum(len(r['all_tracks']) for r in rr),
            form_possible_tracks=sum(len(r['form_possible_tracks']) for r in rr),
            received_tracks=sum(len(r['received_tracks']) for r in rr),
            median_first_receipt_delay_s=statistics.median(delays) if delays else None,
            received_within_10s=sum(len(r['receipts_within_s']['10']) for r in rr),
            received_within_30s=sum(len(r['receipts_within_s']['30']) for r in rr),
            received_packets=sum(r['received_packets'] for r in rr),
            wire_bytes_sent=sum(r['wire_bytes_sent'] for r in rr),
            link_capacity_bytes=sum(r['link_capacity_bytes'] for r in rr)))
    return out

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out',required=True)
    parser.add_argument('--data-root',required=True,type=Path)
    parser.add_argument('--forms-dir',required=True,type=Path)
    parser.add_argument('--cache-dir',required=True,type=Path,help='Matching phase1 dev benchmark directory with records/provenance')
    parser.add_argument('--baseline-path',type=Path,default=BASELINE)
    args=parser.parse_args()
    sys.path.insert(0,str(args.baseline_path.resolve()))
    from link_simulator import simulate
    from run_benchmark import read_gt
    out=Path(args.out);out.mkdir(parents=True,exist_ok=False)
    started=time.perf_counter();records,index,lock=verify_inputs(args.data_root,args.forms_dir,args.cache_dir,args.baseline_path)
    save(out/'pre-run-lock.json',lock)
    rows=[]; runs=[]; packets_dir=out/'packets';packets_dir.mkdir();txdir=out/'transport';txdir.mkdir()
    for sid,seq in index.items():
        seqrecords=[r for r in records if r['source']==sid]; duration=seq['n_frames']/seq['fps']
        # Generate all input packets without annotations. Then simulate; GT enters scoring only.
        for batch in CONFIG['batch_seconds']:
            for method in METHODS:
                packets=make_packets(seqrecords,method,batch,duration)
                save(packets_dir/f'{sid}--{batch}--{method}.json',packets)
                for rate in RATES:
                    for schedule in SCHEDULES:
                        link=make_link(duration,rate,schedule)
                        sim=simulate(packets,link,duration,overhead_bytes=64,propagation_s=.25,ttl_s=60,
                                     policy='fifo' if method=='full_fifo' else 'priority')
                        key=f'{sid}--{batch}--{method}--{rate}--{schedule}'
                        save(txdir/(key+'.json'),sim)
                        gt=read_gt(args.data_root/sid/'gt/gt.txt')
                        evaluated=evaluate(packets,sim,gt,seq['fps'],duration)
                        run=dict(sequence=sid,source_group=seq.get('upstream',sid),gt_status=seq.get('gt_status'),
                            method=method,batch_s=batch,rate_bps=rate,schedule=schedule,duration_s=duration,
                            received_packets=sum(x.get('received_s') is not None and x['received_s']<=duration+1e-9 for x in sim['transmissions']),
                            wire_bytes_sent=sim['accounting']['wire_bytes_sent'],
                            link_capacity_bytes=sim['accounting']['capacity_bytes'])
                        runs.append(run)
                        rows.extend({**run,**e} for e in evaluated)
        print(sid,'complete',flush=True)
    save(out/'metrics.json',rows);save(out/'runs.json',runs);save(out/'summary.json',aggregate(rows))
    save(out/'run.json',dict(runs=len(runs),rows=len(rows),wall_seconds=time.perf_counter()-started,
                            data_scope='development only',config=CONFIG))

if __name__=='__main__':main()
