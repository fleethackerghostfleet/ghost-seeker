"""A completion preference can help completion and delay an urgent small form."""
import unittest

from chunk_simulator import simulate as fixed
from chunk_simulator_completion import simulate as completion


def fixture(later_bytes):
    return [dict(id='nearly_complete',bytes=100,arrival_s=0,priority=1),
            dict(id='later_high_priority',bytes=later_bytes,arrival_s=9,priority=2)]


def run(fn, packets):
    return fn(packets,[dict(start_s=0,end_s=10.5,bps=80)],10.5,
              chunk_payload_bytes=10,overhead_bytes=0,propagation_s=0,ttl_s=None)


class CompletionBiasTests(unittest.TestCase):
    def test_finishes_a_nearly_complete_image_instead_of_two_partials(self):
        a=run(fixed,fixture(200));b=run(completion,fixture(200))
        self.assertEqual(a['accounting']['received_packets'],0)
        self.assertEqual(b['accounting']['received_packets'],1)
        self.assertEqual(b['transmissions'][0]['received_s'],10)
        self.assertEqual(b['chunks'][9]['packet_id'],'nearly_complete')

    def test_completion_bias_can_starve_a_new_small_priority_packet(self):
        a=run(fixed,fixture(10));b=run(completion,fixture(10))
        got_a={p['id']:p['received_s'] for p in a['transmissions']}
        got_b={p['id']:p['received_s'] for p in b['transmissions']}
        self.assertEqual(got_a['later_high_priority'],10)
        self.assertIsNone(got_b['later_high_priority'])
        self.assertEqual(got_b['nearly_complete'],10)


if __name__=='__main__':unittest.main()
