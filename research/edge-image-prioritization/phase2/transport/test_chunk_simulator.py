import random
import unittest

from chunk_simulator import simulate
from link_simulator import simulate as whole


def packet(pid,size,arrival=0,priority=1,deps=None):
    return dict(id=pid,bytes=size,arrival_s=arrival,priority=priority,dependency_ids=deps or [])


class ChunkTests(unittest.TestCase):
    def run_sim(self, packets, horizon=20, rate=80, **kwargs):
        options=dict(chunk_payload_bytes=10,overhead_bytes=0,propagation_s=0,ttl_s=None)
        options.update(kwargs)
        return simulate(packets,[dict(start_s=0,end_s=horizon,bps=rate)],horizon,**options)

    def test_new_priority_waits_only_until_chunk_end(self):
        r=self.run_sim([packet('a',40),packet('b',10,1.5,2)])
        self.assertEqual([c['packet_id'] for c in r['chunks']],['a','a','b','a','a'])
        tx={p['id']:p for p in r['transmissions']}
        self.assertEqual(tx['a']['received_s'],5)
        self.assertEqual(tx['b']['start_s'],2)
        self.assertEqual(tx['b']['received_s'],3)

    def test_header_per_chunk_and_exact_payload(self):
        r=self.run_sim([packet('a',25)],overhead_bytes=2)
        self.assertEqual([c['payload_bytes'] for c in r['chunks']],[10,10,5])
        self.assertEqual(r['accounting']['header_bytes_sent'],6)
        self.assertEqual(r['accounting']['application_bytes_sent'],25)
        self.assertAlmostEqual(r['transmissions'][0]['received_s'],3.1)
        self.assertEqual(r['transmissions'][0]['wire_bytes'],31)

    def test_partial_header_is_not_application_or_receipt(self):
        r=self.run_sim([packet('a',10)],horizon=.1,overhead_bytes=64)
        self.assertEqual(r['accounting']['wire_bytes_sent'],1)
        self.assertEqual(r['accounting']['header_bytes_sent'],1)
        self.assertEqual(r['accounting']['application_bytes_sent'],0)
        self.assertIsNone(r['transmissions'][0]['received_s'])

    def test_ttl_only_gates_first_chunk(self):
        r=self.run_sim([packet('a',40),packet('b',10,1.5,2),packet('c',10,0,.1)],ttl_s=1)
        self.assertEqual({p['id'] for p in r['transmissions']},{'a','b'})
        self.assertEqual([p['id'] for p in r['expired']],['c'])
        self.assertTrue(next(p for p in r['transmissions'] if p['id']=='a')['stale_at_receipt'])

    def test_dependency_waits_for_whole_image_receipt(self):
        r=self.run_sim([packet('a',20),packet('b',10,priority=5,deps=['a'])],propagation_s=1)
        tx={p['id']:p for p in r['transmissions']}
        self.assertEqual(tx['a']['received_s'],3)
        self.assertEqual(tx['b']['start_s'],3)
        self.assertEqual(tx['b']['received_s'],5)

    def test_active_chunk_resumes_across_outage(self):
        r=simulate([packet('a',20),packet('b',10,1,2)],
            [dict(start_s=0,end_s=.5,bps=80),dict(start_s=5,end_s=10,bps=80)],10,
            chunk_payload_bytes=10,overhead_bytes=0,propagation_s=0)
        self.assertEqual([c['packet_id'] for c in r['chunks']],['a','b','a'])
        self.assertEqual(r['chunks'][0]['serialized_s'],5.5)
        self.assertEqual(r['chunks'][1]['start_s'],5.5)

    def test_whole_packet_parity_for_single_chunk(self):
        rng=random.Random(7301)
        for _ in range(40):
            ps=[packet(str(i),rng.randint(1,500),rng.randint(0,10),rng.random()) for i in range(8)]
            schedule=[dict(start_s=i,end_s=i+1,bps=800 if rng.random()>.2 else 0) for i in range(20)]
            a=whole(ps,schedule,20,ttl_s=5,propagation_s=.25,overhead_bytes=64)
            b=simulate(ps,schedule,20,ttl_s=5,propagation_s=.25,overhead_bytes=64,chunk_payload_bytes=1000)
            self.assertEqual(len(a['transmissions']),len(b['transmissions']))
            for x,y in zip(a['transmissions'],b['transmissions']):
                for key in ['id','start_s','serialized_s','received_s','wire_bytes_sent','wire_bytes','status']:
                    self.assertEqual(x[key],y[key])
            self.assertEqual(a['accounting']['wire_bytes_sent'],b['accounting']['wire_bytes_sent'])

    def test_no_credit_future_or_partial_image_receipt(self):
        r=self.run_sim([packet('a',50,9)],horizon=10)
        self.assertEqual(r['accounting']['wire_bytes_sent'],10)
        self.assertEqual(r['accounting']['received_packets'],0)
        self.assertEqual(r['transmissions'][0]['payload_bytes_completed_chunks'],10)


if __name__=='__main__':unittest.main()
