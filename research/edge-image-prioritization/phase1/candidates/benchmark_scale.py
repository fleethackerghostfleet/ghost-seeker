"""Bounded computational proxy benchmark; not operational accuracy evidence."""
from __future__ import annotations
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import resource
import subprocess
import sys
import time


def greedy_rank(method, descriptors, bases, *, prune=False):
    """Exactly the pinned incremental max-similarity ranking, with optional bound."""
    count=len(descriptors)
    ids=[f'frame-{i:06d}' for i in range(count)]
    pending=list(range(count));maximum=[0.]*count;nearest=[None]*count
    order=[];comparisons=bounds=skipped=0
    started=time.perf_counter();cpu_started=time.process_time()
    while pending:
        best=min(pending,key=lambda i:(-bases[i]*(1-.85*maximum[i]),ids[i]))
        order.append({'id':ids[best],'final_score':bases[best]*(1-.85*maximum[best]),
                      'max_similarity':maximum[best],
                      'nearest_prior_id':None if nearest[best] is None else ids[nearest[best]]})
        pending.remove(best)
        for other in pending:
            if prune:
                bound=method.upper_bound(descriptors[best],descriptors[other]);bounds+=1
                if bound<=maximum[other]:
                    skipped+=1;continue
            value=method.compare(descriptors[best],descriptors[other]);comparisons+=1
            if not math.isfinite(value) or not 0<=value<=1:
                raise ValueError('comparator returned invalid similarity')
            if value>maximum[other]:
                maximum[other]=value;nearest[other]=best
    stats={'wall_seconds':time.perf_counter()-started,'cpu_seconds':time.process_time()-cpu_started,
           'full_comparisons':comparisons,'bound_checks':bounds,'skipped_comparisons':skipped,
           'possible_pairs':count*(count-1)//2}
    return order,stats


def memory_snapshot():
    # Darwin reports ru_maxrss bytes; Linux reports KiB.
    high=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    peak=int(high if sys.platform=='darwin' else high*1024)
    current=None
    try:
        if sys.platform.startswith('linux'):
            pages=int(Path('/proc/self/statm').read_text().split()[1])
            current=pages*os.sysconf('SC_PAGE_SIZE')
        else:
            result=subprocess.run(['ps','-o','rss=','-p',str(os.getpid())],capture_output=True,text=True,check=True)
            current=int(result.stdout.strip())*1024
    except (OSError,ValueError,subprocess.SubprocessError):
        pass
    return {'current_rss_bytes':current,'process_peak_rss_bytes':peak}


def summaries(values):
    import numpy as np
    return {'n':len(values),'p50_ms':float(np.percentile(values,50)),
            'p95_ms':float(np.percentile(values,95)),'observed_max_ms':float(max(values))}


def configure_threads(requested):
    # This runs before loading numpy/OpenCV in a fresh worker process.
    for key in ('OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS','VECLIB_MAXIMUM_THREADS','NUMEXPR_NUM_THREADS'):
        os.environ[key]=str(requested)
    import cv2
    cv2.setNumThreads(requested);cv2.ocl.setUseOpenCL(False)
    after_request=cv2.getNumThreads()
    fallback=after_request>requested
    if fallback:
        cv2.setNumThreads(0)
    return {'requested_cap':requested,'opencv_after_positive_request':after_request,
            'opencv_effective_reported':cv2.getNumThreads(),'opencv_serial_fallback':fallback,
            'opencv_backend':[line.strip() for line in cv2.getBuildInformation().splitlines() if 'Parallel framework' in line],
            'limitation':'Reported OpenCV threads and numerical-library environment caps are not an independently observed process-wide concurrency bound.'}


def generated_frames(count,seed):
    """Textured horizon/water proxies with fixed camera motion and sparse patches."""
    import cv2
    import numpy as np
    rng=np.random.default_rng(seed)
    h,w=384,640;y,x=np.mgrid[:h,:w]
    sea=np.stack([95+.03*x+12*np.sin(y/11),125+.015*x+8*np.sin(y/15),140+.01*x],axis=-1)
    sky=np.stack([175+.01*x,165+.02*x,145+.02*x],axis=-1)
    scene=np.where((y<178)[...,None],sky,sea)
    texture=cv2.GaussianBlur(rng.normal(0,4,(h,w,3)).astype(np.float32),(5,5),1)
    scene=np.clip(scene+texture,0,255).astype(np.uint8)
    cv2.rectangle(scene,(285,171),(324,184),(30,35,40),-1)
    for i in range(count):
        sx=(i%13)-6;sy=(i//7)%3-1
        frame=cv2.warpAffine(scene,np.float32([[1,0,sx],[0,1,sy]]),(w,h),borderMode=cv2.BORDER_REFLECT)
        if i%11==0:
            px=100+(i*17)%430;py=165+(i%5)
            cv2.rectangle(frame,(px,py),(px+7,py+7),(20,20,20),-1)
        # Fixed deterministic illumination variation; no semantic labels.
        frame=np.clip(frame.astype(np.int16)+(i%7)-3,0,255).astype(np.uint8)
        yield frame


def write_checkpoint(path,data):
    temporary=path.with_suffix('.tmp')
    temporary.write_text(json.dumps(data,indent=2)+'\n')
    temporary.replace(path)


def worker(args):
    threads=configure_threads(args.threads)
    import cv2
    import numpy as np
    from candidate_methods import make_method,descriptor_nbytes
    method=make_method(args.worker)
    out=Path(args.out);out.mkdir(parents=True,exist_ok=True)
    report_path=out/(args.worker+'.json')
    result={'status':'running','method':args.worker,'config':method.config,
            'scope':'Generated computational proxies; no operational accuracy or target-device certification.',
            'environment':{'machine':platform.machine(),'platform':platform.platform(),
                'python':platform.python_version(),'numpy':np.__version__,'opencv':cv2.__version__,
                'logical_cpus':os.cpu_count(),'threads':threads},
            'seed':args.seed,'source_shape_bgr':[384,640,3],
            'source_sha256':hashlib.sha256(Path(__file__).with_name('candidate_methods.py').read_bytes()).hexdigest(),
            'ranking':[],'memory':{'before_prepare':memory_snapshot()}}
    descriptors=[];prep_ms=[]
    for frame in generated_frames(max(args.sizes),args.seed):
        started=time.perf_counter_ns();descriptors.append(method.prepare(frame))
        prep_ms.append((time.perf_counter_ns()-started)/1e6)
    result['preparation']=summaries(prep_ms)
    result['preparation']['first_call_ms']=prep_ms[0]
    result['preparation']['warm_after_3']=summaries(prep_ms[3:] if len(prep_ms)>3 else prep_ms)
    result['descriptor_payload_bytes_each']=descriptor_nbytes(descriptors[0])
    result['memory']['after_all_descriptors']=memory_snapshot()
    # Comparison distribution, including identity and near-duplicate pairs.
    comparison_ms=[];bound_ms=[]
    rng=np.random.default_rng(args.seed+1)
    for i in range(args.timing_repetitions):
        a=int(rng.integers(len(descriptors)));b=a if i%5==0 else int(rng.integers(len(descriptors)))
        started=time.perf_counter_ns();method.compare(descriptors[a],descriptors[b])
        comparison_ms.append((time.perf_counter_ns()-started)/1e6)
        started=time.perf_counter_ns();method.upper_bound(descriptors[a],descriptors[b])
        bound_ms.append((time.perf_counter_ns()-started)/1e6)
    result['comparison']=summaries(comparison_ms)
    result['upper_bound']=summaries(bound_ms)
    result['comparison']['maximum_note']='Observed maximum in this sample; not a proven worst-case execution-time bound.'
    bases=np.random.default_rng(args.seed+2).uniform(.25,.9,len(descriptors)).tolist()
    result['base_score_note']='Fixed-seed positive pseudorandom values to exercise ranking; not model or ground-truth relevance scores.'
    write_checkpoint(report_path,result)
    for n in args.sizes:
        unpruned,plain=greedy_rank(method,descriptors[:n],bases[:n],prune=False)
        pruned,fast=greedy_rank(method,descriptors[:n],bases[:n],prune=True)
        identical=unpruned==pruned
        result['ranking'].append({'n':n,'unpruned':plain,'pruned':fast,
            'descriptor_prepare_seconds':sum(prep_ms[:n])/1000,
            'measured_prepare_plus_unpruned_rank_seconds':sum(prep_ms[:n])/1000+plain['wall_seconds'],
            'measured_prepare_plus_pruned_rank_seconds':sum(prep_ms[:n])/1000+fast['wall_seconds'],
            'exact_order_scores_and_nearest_parity':identical,
            'rank_sha256':hashlib.sha256(json.dumps(unpruned,sort_keys=True).encode()).hexdigest(),
            'memory_after':memory_snapshot()})
        write_checkpoint(report_path,result)
        if not identical:
            raise AssertionError('Pruning changed ranking, scores, or nearest-prior identities')
    # Throughput projections deliberately use no pruning benefit: workload-dependent.
    per_pair=result['comparison']['p50_ms']/1000
    prep=result['preparation']['warm_after_3']['p50_ms']/1000
    projections=[]
    by_n={row['n']:row for row in result['ranking']}
    for fps in (1,2,5,30):
        n=60*fps;pairs=n*(n-1)//2
        item={'input_fps':fps,'window_seconds':60,'frames_per_window':n,'possible_pairs':pairs,
              'estimated_descriptor_payload_bytes':n*result['descriptor_payload_bytes_each'],
              'estimated_prepare_seconds':n*prep,'estimated_compare_seconds_no_pruning':pairs*per_pair,
              'estimate_note':'Arithmetic extrapolation from median primitive timings; excludes decode/I/O/scheduler overhead, variability and target-hardware differences.'}
        item['estimated_compute_seconds_no_pruning']=item['estimated_prepare_seconds']+item['estimated_compare_seconds_no_pruning']
        item['estimated_compute_to_window_ratio']=item['estimated_compute_seconds_no_pruning']/60
        if n in by_n:
            item['measured_rank_unpruned_seconds']=by_n[n]['unpruned']['wall_seconds']
            item['measured_rank_pruned_seconds']=by_n[n]['pruned']['wall_seconds']
            item['measured_prepare_plus_rank_unpruned_seconds']=by_n[n]['measured_prepare_plus_unpruned_rank_seconds']
            item['measured_prepare_plus_rank_pruned_seconds']=by_n[n]['measured_prepare_plus_pruned_rank_seconds']
        projections.append(item)
    result['capacity_projections']=projections
    result['status']='complete';result['memory']['final']=memory_snapshot()
    write_checkpoint(report_path,result)
    print(json.dumps({'method':args.worker,'status':'complete','report':str(report_path)}),flush=True)
    return 0


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out',required=True)
    parser.add_argument('--threads',type=int,default=4)
    parser.add_argument('--sizes',type=int,nargs='+',default=[30,60,120,300])
    parser.add_argument('--methods',nargs='+',default=['tiled_phash','local_color','registered_residual'])
    parser.add_argument('--seed',type=int,default=7321)
    parser.add_argument('--timing-repetitions',type=int,default=120)
    parser.add_argument('--timeout-seconds',type=int,default=600,help='Total parent wall-time allowance across method workers')
    parser.add_argument('--worker',choices=['tiled_phash','local_color','registered_residual'],help=argparse.SUPPRESS)
    args=parser.parse_args()
    if args.threads<1 or min(args.sizes)<4 or max(args.sizes)>300 or args.timing_repetitions<1 or args.timeout_seconds<1:
        parser.error('threads/repetitions/timeout must be positive; each size must be 4..300')
    args.sizes=sorted(set(args.sizes))
    allowed={'tiled_phash','local_color','registered_residual'}
    if not set(args.methods)<=allowed:
        parser.error('unknown method')
    if args.worker:
        return worker(args)
    out=Path(args.out).resolve()
    out.mkdir(parents=True,exist_ok=False)
    deadline=time.monotonic()+args.timeout_seconds
    results=[]
    for name in args.methods:
        remaining=deadline-time.monotonic()
        if remaining<=0:
            results.append({'method':name,'status':'skipped_total_timeout'});continue
        command=[sys.executable,str(Path(__file__).resolve()),'--worker',name,'--out',str(out),
                 '--threads',str(args.threads),'--seed',str(args.seed),
                 '--timing-repetitions',str(args.timing_repetitions),'--sizes',*[str(n) for n in args.sizes]]
        started=time.perf_counter()
        try:
            process=subprocess.run(command,timeout=remaining,capture_output=True,text=True)
            (out/(name+'.log')).write_text(process.stdout+process.stderr)
            status='complete' if process.returncode==0 else 'failed'
            results.append({'method':name,'status':status,'exit_code':process.returncode,'worker_wall_seconds':time.perf_counter()-started})
        except subprocess.TimeoutExpired as exc:
            stdout=exc.stdout.decode(errors='replace') if isinstance(exc.stdout,bytes) else exc.stdout or ''
            stderr=exc.stderr.decode(errors='replace') if isinstance(exc.stderr,bytes) else exc.stderr or ''
            (out/(name+'.log')).write_text(stdout+stderr+'\nWorker terminated at total wall-time allowance.\n')
            results.append({'method':name,'status':'timeout','worker_wall_seconds':time.perf_counter()-started})
        except KeyboardInterrupt:
            results.append({'method':name,'status':'interrupted','worker_wall_seconds':time.perf_counter()-started})
            print(json.dumps(results[-1]),flush=True)
            break
        print(json.dumps(results[-1]),flush=True)
    summary={'scope':'Computational proxy benchmark; inspect per-method limitations and effective threads.',
             'source_sha256':hashlib.sha256(Path(__file__).with_name('candidate_methods.py').read_bytes()).hexdigest(),
             'requested_threads':args.threads,'sizes':args.sizes,'total_timeout_seconds':args.timeout_seconds,
             'workers':results}
    write_checkpoint(out/'summary.json',summary)
    return 0 if all(row['status']=='complete' for row in results) else 1


if __name__=='__main__':
    sys.exit(main())
