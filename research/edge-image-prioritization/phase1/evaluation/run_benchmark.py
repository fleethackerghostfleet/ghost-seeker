#!/usr/bin/env python3
"""Offline positive-annotation coverage benchmark; no cloud I/O or deployments."""
import argparse, copy, csv, hashlib, importlib.util, json, math, os, platform, resource, sys, time
from collections import defaultdict
from pathlib import Path

BPS = [0, 9600, 64000, 256000, 2000000]
BUILTINS = {'color', 'phash', 'color-phash', 'no_similarity', 'uniform'}
DEFAULT_METHODS = ['color','phash','color-phash','no_similarity','uniform','tiled_phash','local_color','registered_residual']

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def dump(path, value):
    Path(path).write_text(json.dumps(value, indent=2, allow_nan=False)+'\n')

def load_jsonl(path):
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]

def load_candidates(path):
    spec=importlib.util.spec_from_file_location('study_candidates',path)
    mod=importlib.util.module_from_spec(spec); sys.modules[spec.name]=mod; spec.loader.exec_module(mod)
    return mod

def check_split_groups(index):
    groups=defaultdict(set)
    for row in index:
        groups[row.get('upstream') or row.get('source_group') or row['seq_id']].add(row['split'])
    bad={g:sorted(s) for g,s in groups.items() if len(s)>1}
    if bad: raise ValueError(f'Source recording spans splits: {bad}')

def read_gt(path):
    result=defaultdict(list)
    for line in Path(path).read_text().splitlines():
        if not line.strip(): continue
        vals=[float(x) for x in line.split(',')]
        if len(vals)<8: raise ValueError(f'Invalid MOT row: {line}')
        frame,track,x,y,w,h,conf,cls=vals[:8]
        if not all(math.isfinite(v) for v in vals): raise ValueError('Nonfinite GT')
        if conf<=0 or w<=0 or h<=0: continue
        result[int(frame)].append({'track':str(int(track)),'class_id':str(int(cls)),
                                  'box':[x,y,w,h], 'visibility':vals[8] if len(vals)>8 else None})
    return dict(result)

def rank_candidate(records, method, descriptors):
    """Same greedy rule and strict tie ordering as pinned rank_records."""
    from ghost_priority.policy import score_record, _ranked_copy
    pending=sorted((r for r in records if r.get('usable') is not False),key=lambda r:r['id'])
    scores={r['id']:score_record(r) for r in pending}
    similarities={r['id']:0. for r in pending}; nearest={r['id']:None for r in pending}
    ranked=[]; compares=0; skipped=0
    while pending:
        def key(r): return (-scores[r['id']]['score']*(1-.85*similarities[r['id']]), r['id'])
        best=min(pending,key=key); bid=best['id']; score=-key(best)[0]
        row=_ranked_copy(best,scores[bid],len(ranked)+1,'priority',score,similarities[bid],nearest[bid])
        row['similarity_engine']=method.name; ranked.append(row); pending.remove(best)
        for other in pending:
            oid=other['id']
            if hasattr(method,'upper_bound') and method.upper_bound(descriptors[bid],descriptors[oid]) <= similarities[oid]:
                skipped+=1; continue
            val=float(method.compare(descriptors[bid],descriptors[oid])); compares+=1
            if not math.isfinite(val) or not 0<=val<=1: raise ValueError('Invalid candidate similarity')
            if val>similarities[oid]: similarities[oid]=val; nearest[oid]=bid
    return ranked, {'comparisons':compares,'upper_bound_skips':skipped}

def metric_sets(records, selected, gt, start_s, end_s, fps, first_frames, classes):
    frames=set(r['frame_index'] for r in records if r.get('usable') is not False)
    selected_frames={r['frame_index'] for r in selected}
    full_frames={f for f in gt if start_s <= (f-1)/fps < end_s}
    all_tracks={a['track'] for f in full_frames for a in gt[f]}
    sampled_tracks={a['track'] for f in frames for a in gt.get(f,[])}
    selected_tracks={a['track'] for f in selected_frames for a in gt.get(f,[])}
    sizes=defaultdict(list); native=defaultdict(set)
    for f in full_frames:
        for a in gt[f]:
            sizes[a['track']].append(a['box'][2]*a['box'][3]); native[a['track']].add(classes.get(a['class_id'],a['class_id']))
    small={t for t,v in sizes.items() if sorted(v)[len(v)//2] < 32*32}
    first_events={t for t,f in first_frames.items() if start_s <= (f-1)/fps < end_s}
    near_first={t for t in first_events if any(t in {a['track'] for a in gt.get(f,[])} and 0<=(f-first_frames[t])/fps<=2 for f in selected_frames)}
    sampled_near_first={t for t in first_events if any(t in {a['track'] for a in gt.get(f,[])} and 0<=(f-first_frames[t])/fps<=2 for f in frames)}
    geom=set()
    for r in selected:
        scale=min(r['payload_width']/r['width'],r['payload_height']/r['height'])
        geom.update(a['track'] for a in gt.get(r['frame_index'],[]) if min(a['box'][2:])*scale>=4)
    result={'all_positive_tracks':sorted(all_tracks),'sampled_positive_tracks':sorted(sampled_tracks),
            'selected_positive_tracks':sorted(selected_tracks),'small_positive_tracks':sorted(small),
            'selected_small_positive_tracks':sorted(selected_tracks & small),
            'first_annotation_events':sorted(first_events),'sampled_first_annotation_events_2s':sorted(sampled_near_first),
            'selected_first_annotation_events_2s':sorted(near_first),'selected_tracks_box_min_side_ge4px':sorted(geom),
            'track_classes':{t:sorted(v) for t,v in native.items()}}
    for label,den,num in [('positive_track_window_coverage',all_tracks,selected_tracks),
                          ('sampled_positive_track_window_coverage',sampled_tracks,selected_tracks),
                          ('small_positive_track_window_coverage',small,selected_tracks & small),
                          ('retrospective_first_annotation_2s_coverage',first_events,near_first)]:
        result[label]=len(num)/len(den) if den else None
    return result

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--data-root',required=True); p.add_argument('--baseline-path',required=True)
    p.add_argument('--candidate-module',required=True); p.add_argument('--split',choices=['dev','test'],required=True)
    p.add_argument('--out',required=True); p.add_argument('--lock'); p.add_argument('--methods',default=','.join(DEFAULT_METHODS))
    p.add_argument('--sample-stride',type=int,default=5); p.add_argument('--threads',type=int,default=4)
    p.add_argument('--analysis-size',type=int,default=320); p.add_argument('--payload-size',type=int,default=1280)
    p.add_argument('--jpeg-quality',type=int,default=80); p.add_argument('--window-seconds',type=float,default=60.)
    p.add_argument('--sequence',action='append'); p.add_argument('--model-path')
    args=p.parse_args()
    if args.sample_stride<1 or args.window_seconds<=0: p.error('Invalid sample/window setting')
    if args.analysis_size != 320: p.error('Frozen candidates use the baseline global descriptor at analysis-size320; alternative preprocessing requires a new study')
    if args.split=='test' and not args.lock: p.error('Held-out evaluation requires --lock from the fixed development run')
    sys.path.insert(0,str(Path(args.baseline_path).resolve()))
    import cv2, numpy as np
    from ghost_priority.analysis import Analyzer
    from ghost_priority.policy import rank_records
    from ghost_priority.selection import select_ranked
    data=Path(args.data_root).resolve(); out=Path(args.out).resolve()
    if out.exists(): raise ValueError('Output directory must be new')
    mod=load_candidates(Path(args.candidate_module).resolve())
    names=args.methods.split(','); candidates={n:mod.make_method(n) for n in names if n not in BUILTINS}
    settings={'methods':names,'candidate_configs':{n:m.config for n,m in candidates.items()},
              'candidate_sha256':sha(args.candidate_module),'harness_sha256':sha(__file__),'index_sha256':sha(data/'index.jsonl'),'requested_threads':args.threads,'sample_stride':args.sample_stride,
              'analysis_size':args.analysis_size,'payload_size':args.payload_size,'jpeg_quality':args.jpeg_quality,
              'window_seconds':args.window_seconds,'budget_bps':BPS,'partial_window_budget':'full_60s_allowance',
              'duplicate_strength':.85,'model_sha256':sha(args.model_path) if args.model_path else None,
              'baseline_source_sha256':{str(f.relative_to(Path(args.baseline_path))):sha(f) for f in sorted((Path(args.baseline_path)/'ghost_priority').rglob('*.py'))}}
    if args.lock:
        expected=json.loads(Path(args.lock).read_text())
        if expected!=settings: raise ValueError('Configuration/code mismatch against lock file')
    index=load_jsonl(data/'index.jsonl'); check_split_groups(index)
    seqs=[r for r in index if r['split']==args.split and (not args.sequence or r['seq_id'] in args.sequence)]
    if not seqs: raise ValueError('No sequences selected')
    if args.split=='test' and args.sequence: raise ValueError('Test run must include every indexed test sequence')
    out.mkdir(parents=True); (out/'payloads').mkdir(); dump(out/'lock.json',settings)
    analyzer=Analyzer(args.model_path,width=args.analysis_size,threads=args.threads)
    cv2.ocl.setUseOpenCL(False)
    started=time.perf_counter(); rows=[]; provenance=[]; records_all=[]; timings=[]
    rank_file=(out/'rankings.jsonl').open('w'); select_file=(out/'selections.jsonl').open('w')
    for seq in seqs:
        sid=seq['seq_id']; fps=float(seq['fps']); n=int(seq['n_frames']); seqdir=data/sid
        print(f'Analyzing {sid}',flush=True)
        seq_records=[]; seq_rows=[]; descriptors={name:{} for name in candidates}
        for f in range(1,n+1,args.sample_stride):
            path=seqdir/'img1'/f'{f:06d}.jpg'; tick=time.perf_counter(); bgr=cv2.imread(str(path)); read_ms=(time.perf_counter()-tick)*1000
            if bgr is None: raise ValueError(f'Missing or unreadable sampled frame {path}')
            tick=time.perf_counter(); evidence=analyzer.analyze(bgr); analysis_ms=(time.perf_counter()-tick)*1000
            tick=time.perf_counter(); h,w=bgr.shape[:2]; scale=min(1.,args.payload_size/max(h,w))
            payload=cv2.resize(bgr,(max(1,round(w*scale)),max(1,round(h*scale))),interpolation=cv2.INTER_AREA) if scale<1 else bgr
            ok,encoded=cv2.imencode('.jpg',payload,[cv2.IMWRITE_JPEG_QUALITY,args.jpeg_quality])
            if not ok: raise ValueError('JPEG encoding failed')
            encode_ms=(time.perf_counter()-tick)*1000
            rid=f'{sid}:{f:06d}'; filename=hashlib.sha256(rid.encode()).hexdigest()[:24]+'.jpg'
            (out/'payloads'/filename).write_bytes(encoded.tobytes())
            row={**evidence,'id':rid,'source':sid,'frame_index':f,'timestamp_s':(f-1)/fps,
                 'width':w,'height':h,'payload_width':payload.shape[1],'payload_height':payload.shape[0],
                 'payload_bytes':int(encoded.nbytes),'payload_path':'payloads/'+filename}
            seq_records.append(row); records_all.append(row)
            provenance.append({'id':rid,'input_path':str(path),'sha256':sha(path)})
            timings.extend({'sequence':sid,'id':rid,'stage':stage,'ms':value} for stage,value in [('read_decode',read_ms),('baseline_analysis',analysis_ms),('payload_encode',encode_ms),('baseline_read_analyze_encode',read_ms+analysis_ms+encode_ms)])
            for name,m in candidates.items():
                tick=time.perf_counter(); descriptors[name][rid]=m.prepare(bgr)
                timings.append({'sequence':sid,'id':rid,'stage':'prepare_'+name,'ms':(time.perf_counter()-tick)*1000})
        # Annotations are read only after image-only evidence and descriptors are complete.
        gt=read_gt(seqdir/'gt/gt.txt'); labelinfo=json.loads((seqdir/'gt/labels.json').read_text())
        classes=labelinfo.get('classes') or {k:v for k,v in labelinfo.items() if str(k).isdigit() and isinstance(v,str)}; first_frames={}
        for f in sorted(gt):
            for a in gt[f]: first_frames.setdefault(a['track'],f)
        provenance.append({'sequence':sid,'gt_sha256':sha(seqdir/'gt/gt.txt'),'labels_sha256':sha(seqdir/'gt/labels.json'),
                           'source_group':seq.get('upstream') or sid,'metadata':seq,'labelinfo':labelinfo})
        pools=defaultdict(list)
        for r in seq_records: pools[int(r['timestamp_s']//args.window_seconds)].append(r)
        for wi in range(math.ceil((n/fps)/args.window_seconds)):
            pool=pools[wi]
            start=wi*args.window_seconds; end=min((wi+1)*args.window_seconds,n/fps)
            for name in names:
                tick=time.perf_counter(); counts={}
                if name in candidates: ranked,counts=rank_candidate(pool,candidates[name],descriptors[name])
                elif name=='uniform': ranked=rank_records(pool,method='uniform')
                elif name=='no_similarity': ranked=rank_records(pool,policy={'duplicate_strength':0})
                else: ranked=rank_records(pool,policy={'similarity_method':name})
                rank_ms=(time.perf_counter()-tick)*1000
                for r in ranked:
                    rank_file.write(json.dumps({'sequence':sid,'window':wi,'method':name,**{k:r.get(k) for k in ['id','priority_rank','base_score','final_score','max_similarity','nearest_prior_id','payload_bytes']}})+'\n')
                for bps in BPS:
                    budget=int(bps*args.window_seconds/8); tick=time.perf_counter(); selected=select_ranked(ranked,budget)
                    selection_ms=(time.perf_counter()-tick)*1000; spent=sum(r['payload_bytes'] for r in selected)
                    assert spent<=budget
                    met=metric_sets(pool,selected,gt,start,end,fps,first_frames,classes)
                    result={'sequence':sid,'source_group':seq.get('upstream') or sid,'split':args.split,'window':wi,
                            'window_start_s':start,'available_duration_s':end-start,'budget_duration_s':args.window_seconds,
                            'method':name,'bandwidth_bps':bps,'budget_bytes':budget,'selected_bytes':spent,
                            'eligible_images':len(ranked),'selected_images':len(selected),'ranking_ms':rank_ms,'selection_ms':selection_ms,
                            'selected_ids':[r['id'] for r in selected],**counts,**met}
                    seq_rows.append(result)
            rank_file.flush()
        # First-annotation proxy can be satisfied across a 60s boundary.
        selected_frames_by_run=defaultdict(set)
        for row in seq_rows:
            selected_frames_by_run[(row['method'],row['bandwidth_bps'])].update(int(rid.rsplit(':',1)[1]) for rid in row['selected_ids'])
        sampled_frames={r['frame_index'] for r in seq_records if r.get('usable') is not False}
        for row in seq_rows:
            ev=set(row['first_annotation_events']); selected_frames=selected_frames_by_run[(row['method'],row['bandwidth_bps'])]
            def captured(frames):
                return sorted(t for t in ev if any(t in {a['track'] for a in gt.get(f,[])} and 0<=(f-first_frames[t])/fps<=2 for f in frames))
            row['selected_first_annotation_events_2s']=captured(selected_frames)
            row['sampled_first_annotation_events_2s']=captured(sampled_frames)
            row['retrospective_first_annotation_2s_coverage']=len(row['selected_first_annotation_events_2s'])/len(ev) if ev else None
            select_file.write(json.dumps(row,allow_nan=False)+'\n')
        rows.extend(seq_rows); select_file.flush()
        print(f'Finished {sid}: {len(seq_records)} sampled frames',flush=True)
    rank_file.close(); select_file.close()
    dump(out/'provenance.json',{'index_sha256':sha(data/'index.jsonl'),'inputs':provenance})
    dump(out/'records.json',records_all); dump(out/'timings.json',timings)
    summary=[]
    for name in names:
        for bps in BPS:
            subset=[r for r in rows if r['method']==name and r['bandwidth_bps']==bps]
            entry={'method':name,'bandwidth_bps':bps,'windows':len(subset),'selected_images':sum(r['selected_images'] for r in subset),
                   'selected_bytes':sum(r['selected_bytes'] for r in subset)}
            for label,num,den in [('track_window','selected_positive_tracks','all_positive_tracks'),
                                  ('sampled_track_window','selected_positive_tracks','sampled_positive_tracks'),
                                  ('small_track_window','selected_small_positive_tracks','small_positive_tracks'),
                                  ('first_annotation_2s','selected_first_annotation_events_2s','first_annotation_events')]:
                nn=sum(len(r[num]) for r in subset); dd=sum(len(r[den]) for r in subset)
                entry[label+'_numerator']=nn;entry[label+'_denominator']=dd;entry[label+'_coverage']=nn/dd if dd else None
            whole_all={(r['sequence'],t) for r in subset for t in r['all_positive_tracks']}
            whole_sel={(r['sequence'],t) for r in subset for t in r['selected_positive_tracks']}
            entry.update(whole_track_numerator=len(whole_sel),whole_track_denominator=len(whole_all),
                         whole_track_coverage=len(whole_sel)/len(whole_all) if whole_all else None)
            summary.append(entry)
    dump(out/'summary.json',summary)
    with (out/'summary.csv').open('w') as f:
        writer=csv.DictWriter(f,fieldnames=list(summary[0]));writer.writeheader();writer.writerows(summary)
    rss=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss/(1024**2 if sys.platform=='darwin' else 1024)
    dump(out/'run.json',{'config':settings,'split':args.split,'sequences':len(seqs),'recording_groups':len({r.get('upstream') or r['seq_id'] for r in seqs}),
         'frames':len(records_all),'wall_seconds':time.perf_counter()-started,'peak_process_rss_mib':rss,
         'python':sys.version,'platform':platform.platform(),'numpy':np.__version__,'opencv':cv2.__version__,
         'opencv_requested_threads':args.threads,'opencv_effective_threads':cv2.getNumThreads(),'providers':analyzer.provider_names,
         'caveats':['Positive annotation coverage, not complete true-object recall or mission value.',
         'First-annotation 2s coverage is retrospective frame coverage in an offline pool, not report latency.',
         'Every pool, including partial/short clips, receives the full nominal window byte allowance.',
         'Whole-JPEG selection; oversized frames are skipped. Labels never enter inference or ranking.',
         'Box-size-in-payload is only geometry; human-readable object visibility is not established.',
         'Peak RSS is the entire sequential study process, not isolated per-method memory.']})
    print(json.dumps({'output':str(out),'frames':len(records_all),'summary_rows':len(summary),'wall_seconds':time.perf_counter()-started}),flush=True)

if __name__=='__main__': main()
