> These reviewed notes describe the private experiment archive. This Git package contains portable code and condensed evidence; raw run directories mentioned below are intentionally external. See the package README for current commands and file mappings.

# Causal transport experiment

`link_simulator.py` models **completed image receipt within an explicit observation horizon**. It is a small deterministic research model, not the repository's Go transport implementation and not a real-network validation.

```python
from link_simulator import simulate

result = simulate(
    packets, link_intervals, horizon_s=42.0,
    overhead_bytes=64, propagation_s=0.25,
    ttl_s=60.0, policy="priority", finish_tail_s=0.0,
)
```

Packets contain unique `id`, `arrival_s`, positive integer `bytes`, numerical `priority`, and optional `dependency_ids`. Additional metadata is copied to result rows. The integration supplies frame and form identifiers separately. `arrival_s` means that encoding and any batch wait have finished; upstream processing delay must be included there by the caller if it is to be modeled.

The link is a list of nonoverlapping `{start_s,end_s,bps}` intervals. Times are seconds, bitrate is bits/second, intervals are `[start,end)`, and gaps have zero capacity. Values must be finite and nonnegative. The simulator sorts the intervals but rejects overlaps. Capacity in an interval is `bps × duration / 8`; no capacity before an arrival is banked or borrowed.

At a positive-capacity instant the highest-priority available packet starts. Equal priorities break by arrival time and packet ID. FIFO is the alternative control. Priority is static, so the integration must document how it was computed from information available at the packet's arrival. The simulator does not access annotations or infer object identities. Once a packet starts, it is nonpreemptive. Rate changes and outages pause/resume that same packet. A more important image arriving during an existing transfer cannot interrupt it.

Every packet costs its exact encoded payload bytes plus one configured overhead allowance. The default is **64 bytes per packet**, an explicit simplifying assumption. An image is received only after its entire byte stream has serialized and the configured propagation interval has elapsed. Unrelated packets can serialize during earlier packets' propagation. Dependencies wait for complete modeled receipt, not serialization. Duplicate, absent, or cyclic dependency declarations are rejected.

The default has no post-capture tail. A packet with `received_s=None` is not an image received within the observation horizon. `serialized_s` and `expected_received_s` distinguish a completed serialization still propagating from a partial serialization. All transmitted bytes—including bytes of an incomplete image—count against link capacity. The `segments` list allows an independent interval-level audit. `accounting.wire_bytes_sent` is a continuous byte-equivalent quantity and can be fractional when a horizon cuts through a byte; it is not an application-delivery byte count. `received_payload_bytes` counts only complete payloads received in time.

TTL is a latest-start limit anchored to **arrival_s**, as agreed with the integration. A start exactly at arrival+TTL is allowed. TTL does not abort a transfer already in progress. `stale_at_receipt` means receipt later than that arrival-based deadline; it does not establish age since a vessel/event first appeared. The integration must calculate capture-to-receipt age separately if needed. Unstarted packets are reported as expired, queued, or dependency-blocked.

`finish_tail_s` extends the observation/link interval without admitting new packets whose arrival is after the original capture horizon. Arrivals exactly at the capture horizon can start only if tail time and capacity exist. Caller-supplied link intervals must also cover any desired tail.

## What this model omits

The production Go code uses a different form ladder and explicit requests/responses, framing, acknowledgments, retries, and link simulation. This model does not reproduce those rules. It assumes error-free resumable transport, known piecewise link service, fixed propagation, zero scheduling/compute overhead, an unlimited pending queue, and no receiver decode failure. It does not include TCP behavior, retransmission loss, ACK loss, modem framing, competing traffic, or finite memory. The fixed overhead and reliability assumptions can be optimistic. Results are **simulated receipt and annotation-geometric visibility**, not operational utility, recognizable content, or actual operator receipt.

## Validation

Run:

```bash
python3 -m unittest discover -s phase2/transport -p 'test_*.py' -v
```

Thirteen tests cover late arrivals with no banked capacity; zero-rate expiry; outage resume and nonpreemption; choice after an outage; framing overhead; complete serialization versus receipt; partial-wire accounting; dependency receipt gating; unrelated-packet propagation overlap; latest-start expiry and inclusive deadline; exact-horizon/tail admission; actual short-clip capacity; deterministic priority/FIFO ties; and malformed dependencies/schedules. The last test additionally checks 80 random schedules against a separately implemented discrete one-byte-per-second oracle and audits each interval's capacity.
