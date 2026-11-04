package shorebridge

import (
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func view(id string, ms int64, tags map[string]string, forms map[pb.FormKind]bool) *pb.ItemView {
	v := &pb.ItemView{Item: &pb.Item{Id: id, CapturedAtUnixMs: ms, Features: &pb.Features{Tags: tags,
		Detections: []*pb.Detection{{Label: "submarine", Confidence: 0.8}}}}, Score: &pb.Score{Priority: 0.9}}
	for k, done := range forms {
		v.Item.Forms = append(v.Item.Forms, &pb.Form{Kind: k, Bytes: 100})
		v.FormStates = append(v.FormStates, &pb.FormState{Kind: k, Bytes: 100, AckedThrough: 40, Complete: done})
	}
	return v
}

func TestGroupBySighting(t *testing.T) {
	views := []*pb.ItemView{
		view("a", 1000, map[string]string{"sighting_id": "s1"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_THUMB: true}),
		view("b", 2000, map[string]string{"sighting_id": "s1", "subtype": "SSBN"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_CROP: true, pb.FormKind_FORM_KIND_FULL: false}),
		view("c", 1500, nil, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true}),
	}
	views[2].Item.Features.Unknownness = 0.9
	objs, cnt := Group(views, []string{"submarine"})
	if cnt.Objects != 2 || cnt.ObjectsWithImage != 1 || cnt.Items != 3 || cnt.Grouped != 2 {
		t.Fatalf("counts = %+v", cnt)
	}
	if objs[0].SightingID != "s1" || objs[0].Frames != 2 || objs[0].Subtype != "SSBN" {
		t.Fatalf("object = %+v", objs[0])
	}
	if objs[0].Best.ID != "b" || objs[0].Best.ImageForm != "crop" {
		t.Fatalf("best = %+v", objs[0].Best)
	}
	if st := objs[0].Best.Forms["full"].State; st != "partial" {
		t.Fatalf("full state = %s", st)
	}
	if objs[1].Key != "item:c" || objs[1].HasImage || objs[1].Best.Forms["thumb"].State != "none" {
		t.Fatalf("untagged = %+v", objs[1])
	}
	if !objs[0].Suppressed {
		t.Fatal("policy suppress list should mark the object suppressed")
	}
	if objs[1].Suppressed || !objs[1].Unknown {
		t.Fatal("unknown contacts are never suppressed by a label")
	}
	if objs[0].Priority != "P1" {
		t.Fatalf("priority = %s", objs[0].Priority)
	}
}
