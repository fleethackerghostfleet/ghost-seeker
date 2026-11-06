> These reviewed notes describe the private experiment archive. This Git package contains portable code and condensed evidence; raw run directories mentioned below are intentionally external. See the package README for current commands and file mappings.

# Completion-bias follow-up

The fixed-priority chunk experiment exposed fragmentation: switching at chunk boundaries sometimes left more whole images incomplete. The next experiment changes **one declared policy rule** at the 8 KiB boundary:

`effective_priority = original_priority × (original_payload_bytes + 64) / (remaining_payload_bytes + 64)`

Remaining bytes are computed from **locally serialized complete chunks**, not acknowledged offsets. Priorities are recalculated only when a chunk ends. Original packet bytes, encoded content, arrivals, dependency rules, latest-start TTL, link traces, header charges and horizons stay fixed. This is a completion preference applied to the prior policy; it is not a new inference or image ranking model and has no annotation access.

Executed **144 runs**: nine development sequences × two policies × four rates × two link schedules. Results are in `results-completion/`; their baseline counts refer to the **primary fixed-priority 8 KiB runs**, not to nonpreemptive whole-packet v1.

| Constant link / policy | Fixed 8 KiB receipts | Completion-biased receipts |
|---|---:|---:|
| 9.6 kbps / full priority | 0 | 0 |
| 9.6 kbps / progressive | 48 | 50 |
| 64 kbps / full priority | 12 | 17 |
| 64 kbps / progressive | 276 | 288 |
| 256 kbps / full priority | 74 | 83 |
| 256 kbps / progressive | 414 | 421 |
| 2 Mbps / full priority | 259 | 259 |
| 2 Mbps / progressive | 633 | 633 |

These are summed **complete form receipts**, potentially multiple forms from one frame. No sequence has fewer complete form receipts in this grid, but this does not establish preservation of more unique annotated objects or useful detail. Root integration separately evaluates those proxies. Completion bias can favor finishing existing low-value content over a newly arrived important image; priority values here are uncalibrated heuristics.

Two exact fixtures document both sides of the tradeoff. A 100-byte image has sent 90 bytes when a higher-original-priority 200-byte image arrives. With a 10.5-second horizon, fixed priority leaves both incomplete; completion bias finishes the older image. If the new image is instead only 10 bytes, fixed priority delivers that new image in time, while completion bias makes it miss the horizon. These fixtures use 10-byte chunks, an 80-bps link and zero overhead/propagation for arithmetic clarity. The policy's +64 smoothing constant is unchanged. They illustrate a real policy tradeoff without any claim about image content.

The actual Go scheduler already scores over **remaining unacknowledged bytes**, uses a different fidelity ladder, and tracks acknowledgments. This Python follow-up uses serialized offsets, fixed link service, and no ACK loss, retransmission, or per-chunk ACK waits. It therefore isolates a plausible mechanism but is not an equivalence test or evidence to replace the Go scheduler.

Independent audit verified **144 runs, 16,946 chunks, 19,656 segments, 3,460 started forms and 3,266 complete receipts**. It reconstructs the boosted priority at every dispatch from saved offsets, verifies contiguous chunks and exact header/data accounting, and checks receipt/capacity constraints without importing either simulator. All 18 source packet hashes match. Two new behavioral tests and four saved fixture logs accompany the result. Primary chunk code/results remained unchanged.
