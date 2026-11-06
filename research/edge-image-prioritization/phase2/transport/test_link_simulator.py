"""Behavioral transport checks, including analytic and independent discrete cases."""
import math
import random
import unittest

from link_simulator import simulate


def packet(pid, size, arrival=0, priority=1, deps=None):
    return {"id": pid, "frame_id": pid, "source": "s", "arrival_s": arrival,
            "bytes": size, "priority": priority, "kind": "image", "dependency_ids": deps or []}


def link(bps, duration, start=0):
    return [{"start_s": start, "end_s": duration, "bps": bps}]


class LinkTests(unittest.TestCase):
    def run_link(self, packets, intervals, horizon, **kwargs):
        options = {"overhead_bytes": 0, "propagation_s": 0, "ttl_s": None}
        options.update(kwargs)
        return simulate(packets, intervals, horizon, **options)

    def test_no_credit_before_arrival(self):
        r = self.run_link([packet("late", 100, 9)], link(80, 10), 10)
        self.assertEqual(r["accounting"]["wire_bytes_sent"], 10)
        self.assertEqual(r["accounting"]["capacity_bytes"], 100)
        self.assertIsNone(r["transmissions"][0]["received_s"])
        self.assertEqual(r["transmissions"][0]["start_s"], 9)

    def test_zero_capacity_expires_without_start(self):
        r = self.run_link([packet("p", 1)], link(0, 10), 10, ttl_s=3)
        self.assertEqual(r["transmissions"], [])
        self.assertEqual(r["expired"][0]["id"], "p")
        self.assertEqual(r["accounting"]["wire_bytes_sent"], 0)

    def test_outage_resumes_same_packet_without_preemption(self):
        schedule = [{"start_s": 0, "end_s": 1, "bps": 80}, {"start_s": 5, "end_s": 10, "bps": 80}]
        r = self.run_link([packet("slow", 20), packet("urgent", 10, 2, 100)], schedule, 10)
        self.assertEqual([p["id"] for p in r["transmissions"]], ["slow", "urgent"])
        self.assertEqual([p["received_s"] for p in r["transmissions"]], [6, 7])
        self.assertEqual(len(r["transmissions"][0]["segments"]), 2)

    def test_packet_choice_at_outage_recovery(self):
        r = self.run_link([packet("low", 10), packet("high", 10, 3, 2)], link(80, 10, 5), 10)
        self.assertEqual([p["id"] for p in r["transmissions"]], ["high", "low"])
        self.assertEqual(r["transmissions"][0]["start_s"], 5)

    def test_overhead_partial_and_propagation_cutoff(self):
        r = self.run_link([packet("a", 10), packet("b", 10)], link(80, 3), 3,
                          overhead_bytes=5, propagation_s=1)
        self.assertEqual(r["transmissions"][0]["received_s"], 2.5)
        self.assertIsNone(r["transmissions"][1]["received_s"])
        self.assertEqual(r["transmissions"][1]["status"], "in_propagation")
        self.assertEqual(r["transmissions"][1]["expected_received_s"], 4)
        self.assertEqual(r["accounting"]["wire_bytes_sent"], 30)
        r = self.run_link([packet("a", 10)], link(80, 1), 1, overhead_bytes=5)
        self.assertEqual(r["accounting"]["partial_wire_bytes"], 10)
        self.assertEqual(r["accounting"]["received_packets"], 0)

    def test_dependencies_wait_for_receipt_but_unrelated_packets_do_not(self):
        r = self.run_link([packet("a", 10, priority=5), packet("b", 10, priority=10, deps=["a"]),
                           packet("c", 10, priority=1)], link(80, 10), 10, propagation_s=2)
        rows = {p["id"]: p for p in r["transmissions"]}
        self.assertEqual(rows["a"]["start_s"], 0)
        self.assertEqual(rows["c"]["start_s"], 1)
        self.assertEqual(rows["b"]["start_s"], 3)
        self.assertEqual(rows["b"]["received_s"], 6)

    def test_start_deadline_does_not_abort_inflight(self):
        r = self.run_link([packet("a", 100), packet("b", 10)], link(80, 15), 15, ttl_s=2)
        self.assertEqual(r["transmissions"][0]["received_s"], 10)
        self.assertTrue(r["transmissions"][0]["stale_at_receipt"])
        self.assertEqual(r["expired"][0]["id"], "b")

    def test_deadline_is_inclusive_at_start(self):
        r = self.run_link([packet("a", 1)], link(80, 5, 2), 5, ttl_s=2)
        self.assertEqual(r["transmissions"][0]["start_s"], 2)

    def test_horizon_and_tail_arrival_rules(self):
        packets = [packet("at", 10, 5), packet("after", 10, 6)]
        r = self.run_link(packets, link(80, 10), 5)
        self.assertEqual(r["transmissions"], [])
        r = self.run_link(packets, link(80, 10), 5, finish_tail_s=5)
        self.assertEqual([p["id"] for p in r["transmissions"]], ["at"])
        self.assertEqual(r["transmissions"][0]["received_s"], 6)
        self.assertEqual(r["excluded"][0]["id"], "after")

    def test_short_horizon_does_not_receive_full_minute_credit(self):
        r = self.run_link([packet("a", 72000)], link(9600, 9), 9)
        self.assertEqual(r["accounting"]["capacity_bytes"], 10800)
        self.assertEqual(r["accounting"]["wire_bytes_sent"], 10800)
        self.assertEqual(r["accounting"]["received_packets"], 0)

    def test_ties_and_fifo(self):
        ps = [packet("b", 1), packet("a", 1), packet("c", 1, priority=2)]
        for shuffled in [ps, list(reversed(ps))]:
            r = self.run_link(shuffled, link(80, 1), 1)
            self.assertEqual([p["id"] for p in r["transmissions"]], ["c", "a", "b"])
        r = self.run_link(ps, link(80, 1), 1, policy="fifo")
        self.assertEqual([p["id"] for p in r["transmissions"]], ["a", "b", "c"])

    def test_bad_dependencies_and_schedule(self):
        for ps in [[packet("a", 1, deps=["missing"])],
                   [packet("a", 1, deps=["b"]), packet("b", 1, deps=["a"])]]:
            with self.assertRaises(ValueError):
                self.run_link(ps, link(80, 1), 1)
        with self.assertRaises(ValueError):
            self.run_link([packet("a", 1)], link(80, 2)+link(80, 3, 1), 3)

    def test_independent_discrete_byte_oracle(self):
        # One byte per positive one-second slot. Integral packet sizes and
        # arrivals yield exact integer endpoints, independently replayable.
        rng = random.Random(20261105)
        for case in range(80):
            slots = [rng.randrange(2) for _ in range(30)]
            ps = [packet(str(i), rng.randint(1, 8), rng.randint(0, 25), rng.randint(0, 5)) for i in range(8)]
            intervals = [{"start_s": i, "end_s": i+1, "bps": 8*x} for i, x in enumerate(slots)]
            r = self.run_link(ps, intervals, 30)
            pending = {p["id"]: p for p in ps}
            active = None
            remaining = 0
            expected = []
            bytes_sent = 0
            for t, available in enumerate(slots):
                if not available:
                    continue
                if active is None:
                    eligible = [p for p in pending.values() if p["arrival_s"] <= t]
                    if not eligible:
                        continue
                    active = min(eligible, key=lambda p: (-p["priority"], p["arrival_s"], p["id"]))
                    del pending[active["id"]]
                    remaining = active["bytes"]
                    expected.append({"id": active["id"], "start_s": t, "received_s": None})
                remaining -= 1
                bytes_sent += 1
                if remaining == 0:
                    expected[-1]["received_s"] = t+1
                    active = None
            got = [{k: p[k] for k in ("id", "start_s", "received_s")} for p in r["transmissions"]]
            self.assertEqual(got, expected, case)
            self.assertEqual(r["accounting"]["wire_bytes_sent"], bytes_sent)
            self.assertLessEqual(bytes_sent, sum(slots))
            # Independent per-interval conservation (not just total bytes).
            for interval in intervals:
                consumed = 0
                for row in r["transmissions"]:
                    for seg in row["segments"]:
                        overlap = max(0, min(seg["end_s"], interval["end_s"])-max(seg["start_s"], interval["start_s"]))
                        consumed += overlap * seg["bps"] / 8
                self.assertLessEqual(consumed, interval["bps"]/8 + 1e-10)


if __name__ == "__main__":
    unittest.main()
