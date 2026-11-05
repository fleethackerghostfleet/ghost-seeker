package shorebridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestNodeStatusEvictions(t *testing.T) {
	if n := nodeStatus(&pb.GetStatusResponse{ItemsEvicted: 7}); n.Evicted != 7 {
		t.Fatalf("evictions not forwarded: %+v", n)
	}
}

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
	views[2].Score.Priority = 0.2
	objs, cnt := Group(views, []string{"submarine"}, nil)
	if cnt.Objects != 2 || cnt.ObjectsWithImage != 1 || cnt.Items != 3 || cnt.Grouped != 2 || cnt.Sightings != 1 || cnt.SightingsWithImage != 1 {
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
	if objs[0].Priority != "P4" || objs[1].Priority != "P1" || objs[1].Best.Priority != "P1" {
		t.Fatalf("tiers: suppressed=%s unknown=%s", objs[0].Priority, objs[1].Priority)
	}
	if objs, _ := Group(views, []string{"ssbn"}, nil); !objs[0].Suppressed {
		t.Fatal("suppress list should also match the sidecar subtype tag")
	}
	objs, _ = Group(views, nil, nil)
	if objs[0].Priority != "P1" || objs[1].Priority != "P4" {
		t.Fatalf("rank tiers: %s %s", objs[0].Priority, objs[1].Priority)
	}
}

func TestGroupUsesContactReportFrames(t *testing.T) {
	img := view("a", 1000, map[string]string{"sighting_id": "s1"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_CROP: true})
	rec := view("r", 2000, map[string]string{"sighting_id": "s1", "event": "close"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true})
	rec.Item.Type = pb.ItemType_ITEM_TYPE_RECORD
	objs, cnt := Group([]*pb.ItemView{img, rec}, nil, map[string]int{"r": 37})
	if cnt.Objects != 1 || objs[0].Frames != 37 || !objs[0].FramesKnown || objs[0].Reports != 2 {
		t.Fatalf("objs = %+v cnt = %+v", objs[0], cnt)
	}
	if objs[0].Best.ID != "a" || !objs[0].HasImage {
		t.Fatalf("record must not win best: %+v", objs[0].Best)
	}
}

func TestGroupSceneTiles(t *testing.T) {
	scene := view("sc", 3000, map[string]string{"event": "scene"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_THUMB: true})
	obj := view("a", 1000, map[string]string{"sighting_id": "s1"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_CROP: true})
	objs, cnt := Group([]*pb.ItemView{scene, obj}, nil, nil)
	if cnt.Scenes != 1 || cnt.Objects != 1 || cnt.Sightings != 1 || cnt.ObjectsWithImage != 1 {
		t.Fatalf("cnt = %+v", cnt)
	}
	if !objs[0].Scene || objs[0].Priority != "P4" || objs[1].Priority != "P1" {
		t.Fatalf("scene tile must not be ranked: %+v %+v", objs[0], objs[1])
	}
}

func TestGroupTwoFrameSightingFramesKnown(t *testing.T) {
	img := view("a", 1000, map[string]string{"sighting_id": "s1"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_CROP: true})
	rec := view("r", 2000, map[string]string{"sighting_id": "s1", "event": "close"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true})
	rec.Item.Type = pb.ItemType_ITEM_TYPE_RECORD
	objs, _ := Group([]*pb.ItemView{img, rec}, nil, map[string]int{"r": 2})
	if objs[0].Frames != 2 || !objs[0].FramesKnown {
		t.Fatalf("n_frames equal to the report count must still count as known: %+v", objs[0])
	}
}

func TestPolicyInfoAndLayeredDirectiveEMCON(t *testing.T) {
	command := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "sub", Locked: true, BoostLabels: []string{"submarine"}}},
		Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, Locked: true, SetBy: "CTF-72", Echelon: pb.Echelon_ECHELON_COMMAND}}
	operator := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO",
		Rules: []*pb.InterestRule{{Id: "old", BoostLabels: []string{"tanker"}}},
		Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_BURST, SetBy: "TAO"}}
	current := &pb.Policy{Id: "current", Version: 5, Emcon: pb.EmconMode_EMCON_MODE_SILENT,
		EmconSetting: proto.Clone(command.Emcon).(*pb.EmconSetting), Layers: []*pb.PolicyLayer{command, operator},
		Conflicts: []*pb.MergeConflict{{RuleId: "blocked", Reason: "Locked by CTF-72", BlockedBy: "sub"}}}
	info := policyInfo(current)
	if info.Version != 5 || info.Emcon != "SILENT" || !info.EmconLocked || info.EmconLockedBy != "CTF-72" ||
		len(info.Locks) != 1 || info.Locks[0].RuleID != "sub" || len(info.Conflicts) != 1 {
		t.Fatalf("policy info=%+v", info)
	}

	var setPolicy *pb.Policy
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, consolePath) {
		case "GetPolicy":
			raw, _ := protojson.Marshal(&pb.GetPolicyResponse{Policy: current})
			w.Header().Set("Content-Type", "application/json")
			w.Write(raw)
		case "SetPolicy":
			var req pb.SetPolicyRequest
			raw := readBody(t, r)
			if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &req); err != nil {
				t.Errorf("decode SetPolicy: %v", err)
			}
			setPolicy = req.Policy
			raw, _ = protojson.Marshal(&pb.SetPolicyResponse{Policy: setPolicy, Conflicts: setPolicy.Conflicts})
			w.Header().Set("Content-Type", "application/json")
			w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	bridge := New(server.URL, "", nil)
	rec := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy",
		strings.NewReader(`{"issuedBy":"TAO","text":"ignore submarines","suppress":["submarine"]}`)))
	if rec.Code != http.StatusOK || setPolicy == nil {
		t.Fatalf("directive status=%d body=%s policy=%v", rec.Code, rec.Body.String(), setPolicy)
	}
	if len(setPolicy.Layers) != 2 || setPolicy.Layers[1].Rules[0].Id[:3] != "ui-" ||
		setPolicy.Layers[1].Emcon.Mode != pb.EmconMode_EMCON_MODE_BURST ||
		setPolicy.EmconSetting.Mode != pb.EmconMode_EMCON_MODE_SILENT ||
		setPolicy.Id != "" || setPolicy.Version != 0 || setPolicy.Signature != nil {
		t.Fatalf("directive did not preserve layers/EMCON or clear metadata: %+v", setPolicy)
	}

	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/emcon", strings.NewReader(`{"mode":"NORMAL"}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "EMCON locked by CTF-72") {
		t.Fatalf("locked EMCON status=%d body=%s", rec.Code, rec.Body.String())
	}
	current.EmconSetting.Locked = false
	current.Layers[0].Emcon.Locked = false
	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/emcon", strings.NewReader(`{"mode":"BURST"}`)))
	if rec.Code != http.StatusOK || setPolicy.Layers[1].Emcon.Mode != pb.EmconMode_EMCON_MODE_BURST ||
		setPolicy.Layers[1].Emcon.SetBy != "operator-ui" || setPolicy.Id != "" || setPolicy.Version != 0 {
		t.Fatalf("EMCON update status=%d body=%s policy=%+v", rec.Code, rec.Body.String(), setPolicy)
	}

	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/emcon", strings.NewReader(`{"mode":"quiet"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mode status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
