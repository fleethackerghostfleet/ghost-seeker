"""Controlled diagnostic probes and local timings; no semantic accuracy claim."""
from pathlib import Path
import argparse
import json
import os
import platform
import time

for key in ('OMP_NUM_THREADS','OPENBLAS_NUM_THREADS','MKL_NUM_THREADS','VECLIB_MAXIMUM_THREADS'):
    os.environ.setdefault(key,'4')
import cv2
import numpy as np
from candidate_methods import get_methods,make_method,descriptor_nbytes
from test_candidate_methods import scene


def time_calls(fn,reps):
    for _ in range(3): fn()
    timings=[]
    for _ in range(reps):
        start=time.perf_counter_ns();fn();timings.append((time.perf_counter_ns()-start)/1e6)
    return {'p50_ms':float(np.percentile(timings,50)),
            'p95_ms':float(np.percentile(timings,95)), 'repetitions':reps}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out',default=str(Path(__file__).parent/'synthetic-audit.json'))
    args=parser.parse_args()
    # macOS GCD ignores positive thread counts; 0 disables parallel regions.
    cv2.setNumThreads(0);cv2.ocl.setUseOpenCL(False)
    base=scene();rng=np.random.default_rng(423)
    pairs=[('identical','nuisance',base.copy())]
    for size in (2,4,8,16,32):
        changed=base.copy();changed[175:175+size,450:450+size]=0
        pairs.append((f'black_marker_{size}px','controlled_local_change',changed))
    for delta in (-20,20):
        pairs.append((f'brightness_{delta}','nuisance',np.clip(base.astype(np.int16)+delta,0,255).astype(np.uint8)))
    for sigma in (1,2,4):
        pairs.append((f'noise_sigma_{sigma}','nuisance',np.clip(base+rng.normal(0,sigma,base.shape),0,255).astype(np.uint8)))
    for shift in (2,5,10,40):
        changed=cv2.warpAffine(base,np.float32([[1,0,shift],[0,1,0]]),(640,384),borderMode=cv2.BORDER_REFLECT)
        pairs.append((f'translation_{shift}px','nuisance',changed))
    for quality in (50,80):
        ok,encoded=cv2.imencode('.jpg',base,[cv2.IMWRITE_JPEG_QUALITY,quality]);assert ok
        pairs.append((f'jpeg_q{quality}','nuisance',cv2.imdecode(encoded,cv2.IMREAD_COLOR)))
    contrast=np.clip((base.astype(np.float32)-128)*1.10+128,0,255).astype(np.uint8)
    pairs.append(('contrast_1.1','nuisance',contrast))
    edge=base.copy();edge[:10,-8:]=0
    pairs.append(('edge_marker_8x10','controlled_local_change',edge))
    rng_texture=np.random.default_rng(17).integers(0,256,base.shape,dtype=np.uint8)
    pairs.append(('unrelated_noise_image','controlled_large_change',rng_texture))
    methods=get_methods()
    methods['tiled_phash_margin_ablation']=make_method('tiled_phash',hash_margin=1e-6)
    methods['local_color_320']=make_method('local_color',analysis_size=320)
    methods['registered_residual_320']=make_method('registered_residual',analysis_size=320)
    result={'protocol':'controlled probes; labels describe generated transforms, not real-world semantics',
            'seed':423,'source_image':'deterministic 640x384 synthetic horizon scene',
            'environment':{'machine':platform.machine(),'python':platform.python_version(),
                           'opencv':cv2.__version__,'numpy':np.__version__,
                           'opencv_threads_requested':0,'opencv_threads_effective':cv2.getNumThreads(),
                           'opencv_parallel_backend':[line.strip() for line in cv2.getBuildInformation().splitlines() if 'Parallel framework' in line]},
            'methods':{},'pairs':[],'symmetry_audit':[]}
    for label,method in methods.items():
        desc=method.prepare(base)
        second=method.prepare(pairs[4][2])
        result['methods'][label]={'config':method.config,'descriptor_payload_bytes':descriptor_nbytes(desc),
            'prepare':time_calls(lambda:method.prepare(base),60),
            'compare':time_calls(lambda:method.compare(desc,second),100)}
        for pair_name,kind,changed in pairs:
            other=method.prepare(changed)
            detail=method.compare_details(desc,other)
            reverse=method.compare(other,desc)
            result['pairs'].append({'method':label,'probe':pair_name,'kind':kind,**detail})
            if abs(detail['score']-reverse)>1e-9:
                result['symmetry_audit'].append({'method':label,'probe':pair_name,'forward':detail['score'],'reverse':reverse})
    # Broader generated-pair symmetry audit covers shifted/rotated textured frames.
    registration=make_method('registered_residual')
    max_error=0.
    for seed in range(10):
        texture=np.random.default_rng(seed).integers(0,256,(160,240,3),dtype=np.uint8)
        texture=cv2.GaussianBlur(texture,(5,5),1.2)
        shifted=cv2.warpAffine(texture,np.float32([[1,0,seed-4],[0,1,seed%3-1]]),(240,160),borderMode=cv2.BORDER_REFLECT)
        a,b=registration.prepare(texture),registration.prepare(shifted)
        max_error=max(max_error,abs(registration.compare(a,b)-registration.compare(b,a)))
    result['registered_random_shift_max_symmetry_error']=max_error
    destination=Path(args.out);destination.parent.mkdir(parents=True,exist_ok=True)
    destination.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({'output':str(destination),'methods':len(methods),'probe_comparisons':len(result['pairs']),
                      'symmetry_failures':len(result['symmetry_audit']),'random_shift_max_symmetry_error':max_error},indent=2))


if __name__=='__main__':main()
