import copy
import unittest
from run_study import choose_detail, geometry_match, make_link, make_packets, evaluate


def sample(rid='a', timestamp=0):
    forms=[]
    for name,size,n in [('full_1280_q80',1280,10000),('full_1280_q50',1280,8000),('overview_640_q80',640,5000),('overview_320_q50',320,1000)]:
        forms.append(dict(form_id=name,kind='overview' if 'overview' in name else 'full',bytes=n,
                          roi=[0,0,1280,720],payload_width=size,payload_height=round(size*720/1280)))
    return dict(id=rid,source='s',timestamp_s=timestamp,frame_index=1+int(timestamp*5),width=1280,height=720,
        usable=True,payload_bytes=10000,signals={'visual_interest':.8,'quality':.8},
        detections=[],fingerprint=[.2]*192,phash={'version':'phash64-dct-v1','hex':'0'*16,'analysis_size':320},forms=forms)


class ExperimentTest(unittest.TestCase):
    def test_clipped_boxes_and_area_fraction(self):
        f=dict(roi=[0,0,100,100],payload_width=100,payload_height=100)
        self.assertTrue(geometry_match([-10,0,20,20],f,100,100,10))
        self.assertFalse(geometry_match([95,0,20,20],dict(f,roi=[0,0,97,100]),120,100,0))
        self.assertFalse(geometry_match([130,0,10,10],f,100,100,0))

    def test_pixels_not_upscaled_crop_magic(self):
        f=dict(roi=[10,10,20,20],payload_width=20,payload_height=20)
        self.assertTrue(geometry_match([10,10,10,10],f,100,100,8))
        self.assertFalse(geometry_match([10,10,10,10],f,100,100,16))
        self.assertFalse(geometry_match([10,10,100,100],f,200,200,0))

    def test_no_detection_fallback_and_predicted_detail(self):
        r=sample()
        self.assertEqual(choose_detail(r)['form_id'],'overview_640_q80')
        r['detections']=[dict(bbox_xywh=[0,0,32,32])]
        self.assertEqual(choose_detail(r)['form_id'],'overview_640_q80')
        r['detections']=[dict(bbox_xywh=[0,0,2,2])]
        self.assertEqual(choose_detail(r)['form_id'],'full_1280_q50')

    def test_packets_do_not_use_later_batch_or_phase1_ranks(self):
        first=sample();later=sample('later',20)
        p=make_packets([first,later],'full_priority',5,30)
        changed=copy.deepcopy(later);changed['signals']['visual_interest']=0;changed['priority_rank']=1
        p2=make_packets([first,changed],'full_priority',5,30)
        self.assertEqual(p[0],p2[0])
        self.assertEqual(p[0]['arrival_s'],5)
        self.assertEqual(p[1]['arrival_s'],25)

    def test_labels_do_not_affect_packet_creation(self):
        r=sample(); base=make_packets([r],'progressive_density',1,30)
        r.update(gt_tracks=['injected'],gt_box=[0,0,1,1],gt_priority=1e9)
        self.assertEqual(base,make_packets([r],'progressive_density',1,30))

    def test_upgrade_requires_own_overview(self):
        p=make_packets([sample()],'progressive_density',1,30)
        self.assertEqual(p[0]['dependency_ids'],[])
        self.assertEqual(p[1]['dependency_ids'],[p[0]['id']])

    def test_capacity_uses_partial_actual_duration(self):
        link=make_link(9.2,9600,'constant')
        self.assertAlmostEqual(sum((r['end_s']-r['start_s'])*r['bps']/8 for r in link),11040)
        off=make_link(60,9600,'outage20to40')
        self.assertAlmostEqual(sum((r['end_s']-r['start_s'])*r['bps']/8 for r in off),48000)

    def test_receipt_after_horizon_and_captured_age(self):
        p=make_packets([sample()],'full_priority',1,10)
        gt={1:[dict(track='1',box=[0,0,32,32])]}
        sim={'transmissions':[dict(id=p[0]['id'],received_s=10.1)]}
        self.assertEqual(evaluate(p,sim,gt,5,10)[0]['received_tracks'],[])
        sim['transmissions'][0]['received_s']=9
        metric=evaluate(p,sim,gt,5,10)[0]
        self.assertEqual(metric['first_receipt_delay_s'],{'1':9.})
        self.assertEqual(metric['packet_capture_age_s'],[9.])

if __name__=='__main__':unittest.main()
