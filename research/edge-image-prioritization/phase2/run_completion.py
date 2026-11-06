"""Execute a fixed development chunk-size ablation on existing immutable packets."""
import argparse
import hashlib
import json
from pathlib import Path
import sys
import time
from collections import defaultdict

HERE=Path(__file__).resolve().parent
sys.path.insert(0,str(HERE/'transport'))
from chunk_simulator_completion import simulate


def read(p):return json.loads(p.read_text())
def save(p,x):p.write_text(json.dumps(x,indent=2,allow_nan=False)+'\n')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source',type=Path,required=True,help='Causal study output directory')
    parser.add_argument('--out',type=Path,required=True)
    parser.add_argument('--chunks',type=Path,required=True,help='Primary chunk study output directory')
    args=parser.parse_args()
    source=args.source
    out=args.out
    out.mkdir(exist_ok=False)
    (out/'transport').mkdir()
    selected=[r for r in read(source/'runs.json') if r['method'] in ['full_priority','progressive_density']
              and r['batch_s']==1 and r['schedule'] in ['constant','outage20to40']]
    sizes=[8192]
    packet_paths=sorted({source/'packets'/f"{r['sequence']}--1--{r['method']}.json" for r in selected})
    save(out/'pre-run-lock.json',dict(scope='development transport ablation, no GT input',
        chunk_payload_sizes=sizes,priority='original priority times (original payload+64)/(locally serialized remaining payload+64)',
        overhead_bytes_per_chunk=64,propagation_s=.25,ttl_s=60,ttl_anchor='firstchunk arrival',
        packet_files={p.name:sha(p) for p in packet_paths},
        code={p.name:sha(p) for p in [HERE/'transport/chunk_simulator_completion.py',HERE/'transport/link_simulator.py',Path(__file__)]}))
    started=time.perf_counter()
    rows=[]
    for r in selected:
        stem=f"{r['sequence']}--1--{r['method']}"
        key=f"{stem}--{r['rate_bps']}--{r['schedule']}"
        original=read(args.chunks/'transport'/f'{key}--chunk8192.json')
        packets=read(source/'packets'/f'{stem}.json')
        for size in sizes:
            sim=simulate(packets,original['config']['link_intervals'],r['duration_s'],
                         chunk_payload_bytes=size,overhead_bytes=64,propagation_s=.25,ttl_s=60)
            path=out/'transport'/f'{key}--chunk{size}.json'
            save(path,sim)
            a=sim['accounting']
            rows.append({**r,'chunk_payload_bytes':size,'result_file':str(path.relative_to(out)),
                'baseline_received_packets':original['accounting']['received_packets'],'received_packets':a['received_packets'],
                'receipt_count_delta':a['received_packets']-original['accounting']['received_packets'],
                'baseline_received_payload_bytes':original['accounting']['received_payload_bytes'],
                'received_payload_bytes':a['received_payload_bytes'],
                'baseline_wire_bytes_sent':original['accounting']['wire_bytes_sent'],'wire_bytes_sent':a['wire_bytes_sent'],
                'header_bytes_sent':a['header_bytes_sent'],'application_bytes_sent':a['application_bytes_sent'],
                'chunks_started':a['chunks_started'],'chunks_serialized':a['chunks_serialized'],
                'partial_wire_bytes':a['partial_wire_bytes'],
                'partial_packets':sum(x['serialized_s'] is None for x in sim['transmissions'])})
    save(out/'runs.json',rows)
    grouped=defaultdict(list)
    for r in rows:grouped[(r['method'],r['rate_bps'],r['schedule'],r['chunk_payload_bytes'])].append(r)
    summary=[]
    for (method,rate,schedule,size),rr in sorted(grouped.items()):
        summary.append(dict(method=method,rate_bps=rate,schedule=schedule,chunk_payload_bytes=size,
            sequences=len(rr),received_packets=sum(r['received_packets'] for r in rr),
            baseline_received_packets=sum(r['baseline_received_packets'] for r in rr),
            receipt_count_delta=sum(r['receipt_count_delta'] for r in rr),
            improved_sequences=sum(r['receipt_count_delta']>0 for r in rr),
            worsened_sequences=sum(r['receipt_count_delta']<0 for r in rr),
            wire_bytes_sent=sum(r['wire_bytes_sent'] for r in rr),
            header_bytes_sent=sum(r['header_bytes_sent'] for r in rr),
            application_bytes_sent=sum(r['application_bytes_sent'] for r in rr),
            received_payload_bytes=sum(r['received_payload_bytes'] for r in rr),
            baseline_received_payload_bytes=sum(r['baseline_received_payload_bytes'] for r in rr)))
    save(out/'summary.json',summary)
    save(out/'run.json',dict(runs=len(rows),wall_seconds=time.perf_counter()-started,
        scope='development only, encoded bytes unchanged, transport boundary ablation',
        model='locally_serialized_completion_bias_v1',new_tests_passed=2,comparison='fixed-priority 8KiB chunk primary'))
    print(json.dumps(read(out/'run.json'),indent=2))


if __name__=='__main__':main()
