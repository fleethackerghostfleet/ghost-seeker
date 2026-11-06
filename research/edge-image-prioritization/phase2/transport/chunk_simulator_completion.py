"""Research ablation: completion-biased redispatch at bounded payload chunks.

This deliberately differs from both nonpreemptive Python v1 and the existing
Go scheduler. It does not implement per-chunk acknowledgments or retransmission.
"""
from __future__ import annotations

import math

from link_simulator import EPS, _prepare


def simulate(packets, link, horizon_s, *, chunk_payload_bytes=8192, finish_tail_s=0.0,
             overhead_bytes=64, propagation_s=0.25, ttl_s=60.0, policy='priority'):
    if isinstance(chunk_payload_bytes, bool) or not isinstance(chunk_payload_bytes, int) or chunk_payload_bytes < 1:
        raise ValueError('chunk_payload_bytes must be a positive integer')
    rows, intervals, horizon, cutoff, propagation, ttl = _prepare(
        packets, link, horizon_s, finish_tail_s, overhead_bytes, propagation_s, ttl_s, policy)
    for p in rows:
        p['total_chunks'] = math.ceil(p['bytes']/chunk_payload_bytes)
        p['wire_bytes'] = p['bytes']+p['total_chunks']*overhead_bytes
    remaining = {p['id']: p for p in rows if p['arrival_s'] <= horizon}
    excluded = [dict(p, status='after_capture_horizon') for p in rows if p['arrival_s'] > horizon]
    transmissions = {}
    offsets = {p['id']: 0 for p in rows}
    chunks = []
    receipts = {}
    expired = []
    clock = 0.0

    def expire(at):
        for pid, p in list(remaining.items()):
            # Once started, TTL does not discard a partially sent image.
            if pid not in transmissions and p['latest_start_s'] is not None and p['latest_start_s'] < at-EPS:
                expired.append(dict(p, status='expired', expired_at_s=p['latest_start_s']))
                del remaining[pid]

    def available(p, at):
        return p['arrival_s'] <= at+EPS and all(d in receipts and receipts[d] <= at+EPS for d in p['dependency_ids'])

    def rate_at(at):
        for interval in intervals:
            if interval['start_s'] <= at < interval['end_s']:
                return interval['bps']/8
        return 0

    while clock < cutoff-EPS and remaining:
        expire(clock)
        eligible = [p for p in remaining.values() if available(p, clock)]
        if eligible and rate_at(clock) > 0:
            key = (lambda p: (-p['priority'] * (p['bytes']+64)/(p['bytes']-offsets[p['id']]+64), p['arrival_s'], p['id'])) if policy=='priority' else (
                lambda p: (p['arrival_s'], p['id']))
            packet = min(eligible, key=key)
            pid = packet['id']
            offset = offsets[pid]
            payload_size = min(chunk_payload_bytes, packet['bytes']-offset)
            wire_size = payload_size+overhead_bytes
            if pid not in transmissions:
                transmissions[pid] = dict(packet, start_s=clock, serialized_s=None, expected_received_s=None,
                    received_s=None, wire_bytes_sent=0.0, wire_bytes_remaining=float(packet['wire_bytes']),
                    segments=[], stale_at_receipt=False, status='partial', chunk_indices=[],
                    payload_bytes_completed_chunks=0)
            tx = transmissions[pid]
            chunk = dict(index=len(chunks), packet_id=pid, payload_offset=offset, payload_bytes=payload_size,
                         overhead_bytes=overhead_bytes, wire_bytes=wire_size, start_s=clock,
                         serialized_s=None, wire_bytes_sent=0.0, segments=[])
            unserved = float(wire_size)
            for interval in intervals:
                lo = max(clock, interval['start_s'])
                hi = min(cutoff, interval['end_s'])
                rate = interval['bps']/8
                if lo >= hi or rate <= 0:
                    continue
                capacity = (hi-lo)*rate
                if unserved <= capacity+EPS:
                    amount = unserved
                    end = min(hi, lo+amount/rate)
                    chunk['segments'].append(dict(start_s=lo, end_s=end, bytes=amount, bps=interval['bps']))
                    chunk['serialized_s'] = end
                    unserved = 0.0
                    clock = end
                    break
                chunk['segments'].append(dict(start_s=lo, end_s=hi, bytes=capacity, bps=interval['bps']))
                unserved -= capacity
                clock = hi
            chunk['wire_bytes_sent'] = wire_size-unserved
            chunk['wire_bytes_remaining'] = unserved
            # Model headers before each chunk's payload, including a partial header.
            chunk['header_bytes_sent'] = min(overhead_bytes, chunk['wire_bytes_sent'])
            chunk['payload_bytes_sent'] = max(0.0, chunk['wire_bytes_sent']-overhead_bytes)
            chunks.append(chunk)
            tx['chunk_indices'].append(chunk['index'])
            tx['segments'].extend(chunk['segments'])
            tx['wire_bytes_sent'] += chunk['wire_bytes_sent']
            tx['wire_bytes_remaining'] -= chunk['wire_bytes_sent']
            if chunk['serialized_s'] is None:
                clock = cutoff
                break
            offsets[pid] += payload_size
            tx['payload_bytes_completed_chunks'] += payload_size
            if offsets[pid] == packet['bytes']:
                tx['serialized_s'] = clock
                expected = clock+propagation
                receipts[pid] = expected
                tx['expected_received_s'] = expected
                if expected <= cutoff+EPS:
                    tx['received_s'] = expected
                    tx['status'] = 'received'
                    tx['stale_at_receipt'] = packet['latest_start_s'] is not None and expected > packet['latest_start_s']+EPS
                else:
                    tx['status'] = 'in_propagation'
                del remaining[pid]
        else:
            events = [p['arrival_s'] for p in remaining.values() if p['arrival_s'] > clock+EPS]
            events += [receipts[d] for p in remaining.values() for d in p['dependency_ids']
                       if d in receipts and receipts[d] > clock+EPS]
            events += [i['start_s'] for i in intervals if i['bps'] > 0 and i['start_s'] > clock+EPS]
            if not events:
                break
            clock = min(events)
    expire(cutoff)
    pending = []
    for pid, p in remaining.items():
        if pid in transmissions:
            continue
        unmet = [d for d in p['dependency_ids'] if d not in receipts or receipts[d] > cutoff+EPS]
        pending.append(dict(p, status='dependency_blocked' if unmet else 'queued', unmet_dependency_ids=unmet))
    txs = list(transmissions.values())
    total_capacity = sum(max(0.0,min(cutoff,i['end_s'])-max(0.0,i['start_s']))*i['bps']/8 for i in intervals)
    capture_capacity = sum(max(0.0,min(horizon,i['end_s'])-max(0.0,i['start_s']))*i['bps']/8 for i in intervals)
    wire_sent = sum(c['wire_bytes_sent'] for c in chunks)
    if wire_sent > total_capacity+max(1e-7,total_capacity*1e-12):
        raise AssertionError('wire bytes exceed interval capacity')
    received = [p for p in txs if p['received_s'] is not None]
    completed = [p for p in txs if p['serialized_s'] is not None]
    return {
        'config':dict(horizon_s=horizon,finish_tail_s=cutoff-horizon,observation_end_s=cutoff,
                      overhead_bytes=overhead_bytes,overhead_mode='per_chunk',chunk_payload_bytes=chunk_payload_bytes,
                      propagation_s=propagation,ttl_s=ttl,ttl_anchor='arrival_s_first_chunk',policy=policy,
                      priority_mode='original_priority_times_original_plus64_over_locally_remaining_plus64',link_intervals=intervals,
                      model='locally_serialized_completion_bias_v1',interval_semantics='[start,end)'),
        'transmissions':txs,'chunks':chunks,'pending':pending,'expired':expired,'excluded':excluded,
        'accounting':dict(capacity_bytes=total_capacity,capture_horizon_capacity_bytes=capture_capacity,
                          wire_bytes_sent=wire_sent,unused_capacity_bytes=max(0.0,total_capacity-wire_sent),
                          utilization=wire_sent/total_capacity if total_capacity else None,
                          input_packets=len(rows),admitted_packets=len(rows)-len(excluded),started_packets=len(txs),
                          serialized_packets=len(completed),received_packets=len(received),pending_packets=len(pending),
                          expired_packets=len(expired),excluded_packets=len(excluded),
                          received_payload_bytes=sum(p['bytes'] for p in received),
                          serialized_wire_bytes=sum(p['wire_bytes'] for p in completed),
                          partial_wire_bytes=sum(p['wire_bytes_sent'] for p in txs if p['serialized_s'] is None),
                          stale_received_packets=sum(p['stale_at_receipt'] for p in received),
                          chunks_started=len(chunks),chunks_serialized=sum(c['serialized_s'] is not None for c in chunks),
                          header_bytes_sent=sum(c['header_bytes_sent'] for c in chunks),
                          application_bytes_sent=sum(c['payload_bytes_sent'] for c in chunks),capacity_check_passed=True)
    }
