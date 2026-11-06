> These reviewed notes describe the private experiment archive. This Git package contains portable code and condensed evidence; raw run directories mentioned below are intentionally external. See the package README for current commands and file mappings.

# Chunk-boundary transport ablation

Executed **432 development simulations** over the same immutable encoded packets as causal v1: nine sequences × two policies × four bitrates × two schedules × three payload chunk sizes. Only batch-1-second `full_priority` and `progressive_density` are included. Schedules are constant rate and the existing 20–40-second outage trace. Encoded JPEG bytes, arrivals, dependencies, priorities, and clip horizons are unchanged.

The intervention is redispatch after each **1,024-, 8,192-, or 32,768-byte payload chunk**, charging **64 extra wire bytes per chunk**. An active chunk resumes across an outage; a higher-priority packet can take the link at the next chunk boundary. Priorities remain the original fixed packet values, including the original whole-form value/byte denominator where that policy used one. This primary experiment has no remaining-byte completion boost.

JPEG receipt requires all chunks plus 0.25-second propagation by the original horizon. Partial chunks/JPEGs consume capacity without counting as image receipt. Dependencies wait for whole-image receipt. TTL limits only the first chunk's start; a partially sent JPEG remains resumable afterward. Headers precede each chunk's payload, so partial headers consume capacity. There are no retransmissions, per-chunk ACK waits, receiver failures, finite buffers, or losses. This is an optimistic reliable-byte transport model.

## Receipt counts and overhead

Counts below sum complete **form receipts over nine development sequences** under constant rate. Progressive policies can deliver several forms of one frame; these are not unique objects, distinct frames, or operator value. Root integration separately scores geometric content.

| Policy / rate | Whole-packet v1 | 1 KiB chunks | 8 KiB chunks | 32 KiB chunks |
|---|---:|---:|---:|---:|
| Full priority / 9.6 kbps | 1 | 0 | 0 | 0 |
| Full priority / 64 kbps | 16 | 11 | 12 | 11 |
| Full priority / 256 kbps | 83 | 71 | 74 | 73 |
| Full priority / 2 Mbps | 259 | 259 | 259 | 259 |
| Progressive / 9.6 kbps | 50 | 36 | 48 | 50 |
| Progressive / 64 kbps | 252 | 274 | 276 | 266 |
| Progressive / 256 kbps | 411 | 406 | 414 | 411 |
| Progressive / 2 Mbps | 633 | 633 | 633 | 633 |

Preemption does not uniformly improve completion. Later packets can interrupt earlier ones and leave several JPEGs incomplete. Additional headers also reduce payload capacity. At 64 kbps, progressive runs spend about **6.26%, 1.15%, and 0.89%** of sent bytes on modeled headers for 1, 8, and 32 KiB chunks. At 9.6 kbps, 1 KiB chunks reduce complete form receipts from 50 to 36.

The audited head-of-line example improves with sufficiently small chunks. In `A_ship_in_the_distance--1--progressive_density--64000--constant`, the frame-16 overview arriving at 4 s remains unreceived under whole-packet v1. It arrives at **5.146375 s with 1 KiB chunks** and **6.042375 s with 8 KiB chunks**. It remains unreceived with 32 KiB chunks. This is a modeled queue effect, not established operational usefulness.

## Existing Go behavior differs

Read-only inspection of pinned `edge/scheduler/scheduler.go` confirms that Go **already uses chunks**, resumes acknowledged offsets, and rebuilds plans. Its default maximum chunk is 64 KiB; its priority denominator is remaining unacknowledged bytes. `edge/link/sim.go` charges protobuf request/response sizes plus 64 bytes each, models RTT/loss, and can lose an exchange when a window closes. These differ materially from both Python models.

The whole-JPEG blocking case is an assumption exposed in Python v1, **not a newly demonstrated production defect**. This ablation identifies transport sensitivities and motivates replay through the actual Go scheduler/link/shore path. It does not justify changing production chunk size. Completing partly sent forms versus switching to new images is a separate policy choice; Go's remaining-byte score already addresses one side of that tradeoff.

## Verification

Eight tests pass, including **40 randomized parity cases** against frozen whole-packet v1 when each image fits in one chunk with one header. Other tests cover interleaving, overhead, partial headers, first-start TTL, whole-image dependencies, outage resume, and no future credit.

An independent saved-event audit imports no simulator. It verified **432 runs, 145,425 chunks, 153,559 segments, 10,594 started forms and 9,502 complete receipts**. It reconstructs offsets/dispatch priority, checks header/data totals and interval capacity, verifies metadata and all 18 packet-file hashes, and reconciles receipt times. No discrepancy was found.

`results/pre-run-lock.json` freezes code/packet hashes. `results/transport/` contains events; `runs.json` maps runs to files; `summary.json` groups receipt counts/overhead. `audit.json` records the audit. Annotation geometry is evaluated separately.
