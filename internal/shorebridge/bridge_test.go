package shorebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
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

func TestGroupIgnoresInvalidSightingID(t *testing.T) {
	invalid := `x"><style>.row{display:none}</style>`
	views := []*pb.ItemView{
		view("a", 1000, map[string]string{"sighting_id": invalid}, nil),
		view("b", 2000, map[string]string{"sighting_id": invalid}, nil),
	}
	objs, _ := Group(views, nil, nil)
	if len(objs) != 2 {
		t.Fatalf("invalid sighting ID grouped into %d objects", len(objs))
	}
	for _, obj := range objs {
		if obj.SightingID != "" || strings.Contains(obj.Key, "sighting:") || strings.Contains(obj.Key, "<") {
			t.Fatalf("invalid sighting ID escaped validation: %+v", obj)
		}
	}

	objs, _ = Group([]*pb.ItemView{
		view("a", 1000, map[string]string{"sighting_id": "clip:0001"}, nil),
		view("b", 2000, map[string]string{"sighting_id": "clip:0001"}, nil),
	}, nil, nil)
	if len(objs) != 1 || objs[0].SightingID != "clip:0001" {
		t.Fatalf("valid sighting IDs did not group: %+v", objs)
	}
}

func TestGroupAcceptsLongSightingID(t *testing.T) {
	sid := strings.Repeat("a", 124) + ":0001"
	objs, _ := Group([]*pb.ItemView{
		view("a", 1000, map[string]string{"sighting_id": sid}, nil),
	}, nil, nil)
	if len(objs) != 1 || objs[0].Key != "sighting:"+sid {
		t.Fatalf("long valid sighting ID was not grouped: %+v", objs)
	}
}

func TestToCardClearsInvalidSource(t *testing.T) {
	v := view("a", 1000, nil, nil)
	v.Item.Source = `<b>y</b>`
	if got := ToCard(v).Source; got != "" {
		t.Fatalf("ToCard source = %q, want empty", got)
	}
}

func TestEnsureSourceFallsBackToCamera(t *testing.T) {
	c := NewConsole(New("", "", nil))
	if got := c.ensureSource(`<b>y</b>`); got != "camera" {
		t.Fatalf("ensureSource returned %q, want camera", got)
	}
	for key := range c.sources {
		if strings.Contains(key, "<") {
			t.Fatalf("unsafe source key %q", key)
		}
	}
}

func TestGroupDoesNotFoldInvalidContactReportSightingID(t *testing.T) {
	rec := view("record", 1000, nil, nil)
	rec.Item.Type = pb.ItemType_ITEM_TYPE_RECORD
	objs, _ := Group([]*pb.ItemView{rec}, nil, map[string]contactReport{
		"record": {SightingID: `x"><style>.row{display:none}</style>`},
	})
	if len(objs) != 1 || objs[0].Key != "item:record" || objs[0].SightingID != "" {
		t.Fatalf("invalid contact report was folded: %+v", objs)
	}
}

func TestGroupUsesContactReportFrames(t *testing.T) {
	img := view("a", 1000, map[string]string{"sighting_id": "s1"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true, pb.FormKind_FORM_KIND_CROP: true})
	rec := view("r", 2000, map[string]string{"sighting_id": "s1", "event": "close"}, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_FACT: true})
	rec.Item.Type = pb.ItemType_ITEM_TYPE_RECORD
	objs, cnt := Group([]*pb.ItemView{img, rec}, nil, map[string]contactReport{"r": {NFrames: 37}})
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
	objs, _ := Group([]*pb.ItemView{img, rec}, nil, map[string]contactReport{"r": {NFrames: 2}})
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
	infoPolicy := &pb.Policy{
		Id: "current", Version: current.Version,
		Layers: []*pb.PolicyLayer{
			proto.Clone(command).(*pb.PolicyLayer),
			{
				Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO",
				Rules: []*pb.InterestRule{{
					Id: "blocked", SuppressLabels: []string{"submarine"},
				}},
				Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: "TAO"},
			},
		},
	}
	info := policyInfo(policy.Flatten(infoPolicy, "", time.Now()))
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
	bridge := New(server.URL, "", nil).WithOperator("TAO")
	rec := httptest.NewRecorder()
	// A request-body issuedBy is ignored: the identity is the configured operator.
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy",
		strings.NewReader(`{"issuedBy":"CTF-72","text":"ignore submarines","suppress":["submarine"]}`)))
	if rec.Code != http.StatusOK || setPolicy == nil {
		t.Fatalf("directive status=%d body=%s policy=%v", rec.Code, rec.Body.String(), setPolicy)
	}
	if len(setPolicy.Layers) != 2 || setPolicy.Layers[1].Rules[0].Id[:3] != "ui-" || setPolicy.Layers[1].IssuedBy != "TAO" || setPolicy.IssuedBy != "TAO" ||
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
		setPolicy.Layers[1].Emcon.SetBy != "TAO" || setPolicy.Id != "" || setPolicy.Version != 0 {
		t.Fatalf("EMCON update status=%d body=%s policy=%+v", rec.Code, rec.Body.String(), setPolicy)
	}

	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/emcon", strings.NewReader(`{"mode":"quiet"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mode status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/emcon",
		strings.NewReader(`{"mode":"NORMAL","padding":"`+strings.Repeat("x", 5<<10)+`"}`)))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "invalid request body\n" {
		t.Fatalf("oversized EMCON body status=%d body=%q", rec.Code, rec.Body.String())
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

func TestDirectiveEmconPhraseUpdatesShipLayer(t *testing.T) {
	cmdLayer := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "sub", Locked: true, BoostLabels: []string{"submarine"}}},
		Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_NORMAL, SetBy: "CTF-72", Echelon: pb.Echelon_ECHELON_COMMAND}}
	operator := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "operator-ui"}
	current := &pb.Policy{Id: "current", Version: 5, Emcon: pb.EmconMode_EMCON_MODE_NORMAL,
		EmconSetting: proto.Clone(cmdLayer.Emcon).(*pb.EmconSetting), Layers: []*pb.PolicyLayer{cmdLayer, operator}}

	var setPolicy *pb.Policy
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, consolePath) {
		case "GetPolicy":
			raw, _ := protojson.Marshal(&pb.GetPolicyResponse{Policy: current})
			w.Header().Set("Content-Type", "application/json")
			w.Write(raw)
		case "SetPolicy":
			var req pb.SetPolicyRequest
			if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(readBody(t, r), &req); err != nil {
				t.Errorf("decode SetPolicy: %v", err)
			}
			setPolicy = req.Policy
			raw, _ := protojson.Marshal(&pb.SetPolicyResponse{Policy: setPolicy})
			w.Header().Set("Content-Type", "application/json")
			w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	bridge := New(server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}

	rec := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy", strings.NewReader(`{"text":"go quiet"}`)))
	if rec.Code != http.StatusOK || setPolicy == nil {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if setPolicy.Emcon != pb.EmconMode_EMCON_MODE_SILENT || setPolicy.EmconSetting.Mode != pb.EmconMode_EMCON_MODE_SILENT ||
		setPolicy.Layers[1].Emcon == nil || setPolicy.Layers[1].Emcon.Mode != pb.EmconMode_EMCON_MODE_SILENT || setPolicy.Layers[1].Emcon.SetBy != "operator-ui" {
		t.Fatalf("directive EMCON phrase must set the ship layer EMCON: %+v", setPolicy)
	}
	if len(setPolicy.Interest.BoostLabels) != 0 || len(setPolicy.Interest.SuppressLabels) != 0 {
		t.Fatalf("EMCON-only directive unexpectedly gained labels: %+v", setPolicy.Interest)
	}
	if !strings.Contains(rec.Body.String(), "EMCON SILENT") {
		t.Fatalf("readback missing EMCON: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy",
		strings.NewReader(`{"text":"go quiet","boost":["tanker"]}`)))
	if rec.Code != http.StatusOK || setPolicy.Emcon != pb.EmconMode_EMCON_MODE_SILENT ||
		setPolicy.EmconSetting.Mode != pb.EmconMode_EMCON_MODE_SILENT ||
		setPolicy.Layers[1].Emcon == nil || setPolicy.Layers[1].Emcon.Mode != pb.EmconMode_EMCON_MODE_SILENT ||
		len(setPolicy.Interest.BoostLabels) != 1 || setPolicy.Interest.BoostLabels[0] != "tanker" {
		t.Fatalf("explicit-label EMCON directive status=%d body=%s policy=%+v", rec.Code, rec.Body.String(), setPolicy)
	}

	current.EmconSetting.Locked = true
	current.Layers[0].Emcon.Locked = true
	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy", strings.NewReader(`{"text":"go quiet"}`)))
	if rec.Code != http.StatusOK || setPolicy.Emcon != pb.EmconMode_EMCON_MODE_NORMAL || setPolicy.Layers[1].Emcon != nil ||
		!strings.Contains(rec.Body.String(), "EMCON locked by CTF-72") {
		t.Fatalf("locked EMCON must be kept with a warning: status=%d body=%s policy=%+v", rec.Code, rec.Body.String(), setPolicy)
	}

	rec = httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy",
		strings.NewReader(`{"text":"go quiet","boost":["tanker"]}`)))
	if rec.Code != http.StatusOK || setPolicy.Emcon != pb.EmconMode_EMCON_MODE_NORMAL ||
		setPolicy.EmconSetting.Mode != pb.EmconMode_EMCON_MODE_NORMAL || setPolicy.Layers[1].Emcon != nil {
		t.Fatalf("explicit-label locked EMCON must keep current mode: status=%d body=%s policy=%+v", rec.Code, rec.Body.String(), setPolicy)
	}
}

func TestPolicyInfoPreservesEdgeScopedLayerConflict(t *testing.T) {
	current := &pb.Policy{
		Id: "layered", Version: 8,
		Layers: []*pb.PolicyLayer{
			{
				Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
				Rules: []*pb.InterestRule{{
					Id: "command-sub", Locked: true, BoostLabels: []string{"submarine"},
					Targets: []string{"edge-1"},
				}},
			},
			{
				Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO",
				Rules: []*pb.InterestRule{{
					Id: "ship-suppress-sub", SuppressLabels: []string{"submarine"},
				}},
			},
		},
	}
	writeResponse := func(w http.ResponseWriter, response proto.Message) {
		t.Helper()
		raw, err := protojson.Marshal(response)
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetStatus":
			writeResponse(w, &pb.GetStatusResponse{NodeId: "shore-1"})
		case consolePath + "GetPolicy":
			writeResponse(w, &pb.GetPolicyResponse{Policy: current})
		case consolePath + "ListItems":
			writeResponse(w, &pb.ListItemsResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(shore.Close)
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != consolePath+"GetStatus" {
			http.NotFound(w, r)
			return
		}
		writeResponse(w, &pb.GetStatusResponse{NodeId: "edge-1"})
	}))
	t.Cleanup(edge.Close)

	bridge := New(shore.URL, edge.URL, nil)
	console := NewConsole(bridge)
	bridge.refresh(context.Background())

	rec := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/shore/snapshot", nil))
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.PolicyInfo == nil || len(snap.PolicyInfo.Conflicts) != 1 ||
		snap.PolicyInfo.Conflicts[0].RuleID != "ship-suppress-sub" {
		t.Fatalf("snapshot policy info lost the edge-scoped conflict: %+v", snap.PolicyInfo)
	}

	console.mu.Lock()
	conflicts := append([]PolicyConflict(nil), console.policy.Conflicts...)
	console.mu.Unlock()
	if len(conflicts) != 1 || conflicts[0].RuleID != "ship-suppress-sub" {
		t.Fatalf("console policy info lost the edge-scoped conflict: %+v", conflicts)
	}
}

func TestPolicyInfoDoesNotShowExpiredLayeredEMCON(t *testing.T) {
	expired := time.Now().Add(-time.Minute).UnixMilli()
	current := policy.Flatten(&pb.Policy{
		Emcon: pb.EmconMode_EMCON_MODE_SILENT,
		EmconSetting: &pb.EmconSetting{
			Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: "TAO", Echelon: pb.Echelon_ECHELON_SHIP,
		},
		Layers: []*pb.PolicyLayer{{
			Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO",
			Emcon: &pb.EmconSetting{
				Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: "TAO",
				Echelon: pb.Echelon_ECHELON_SHIP, ExpiresAtUnixMs: expired,
			},
		}},
	}, "", time.Now())
	info := policyInfo(current, pb.Echelon_ECHELON_SHIP)
	if info.Emcon != "NORMAL" || info.EmconSetBy != "" || info.EmconLocked {
		t.Fatalf("policy info should show default NORMAL after EMCON expiry: %+v", info)
	}
}

func writeProtoResponse(t *testing.T, w http.ResponseWriter, response proto.Message) {
	t.Helper()
	raw, err := protojson.Marshal(response)
	if err != nil {
		t.Errorf("marshal response: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func shorePolicyTestServer(t *testing.T, current *pb.Policy, setStatus int, onSet func(*pb.Policy)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetPolicy":
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: current})
		case consolePath + "SetPolicy":
			if setStatus != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(setStatus)
				_, _ = w.Write([]byte(`{"code":"permission_denied","message":"opauth rejected"}`))
				return
			}
			var request pb.SetPolicyRequest
			if err := protojson.Unmarshal(readBody(t, r), &request); err != nil {
				t.Errorf("decode SetPolicy request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if onSet != nil {
				onSet(proto.Clone(request.Policy).(*pb.Policy))
			}
			writeProtoResponse(t, w, &pb.SetPolicyResponse{Policy: request.Policy})
		default:
			http.NotFound(w, r)
		}
	}))
}

func submitTimedDirectiveWithoutJWT(t *testing.T, current *pb.Policy) (*pb.Policy, string, []string) {
	t.Helper()
	var submitted *pb.Policy
	shore := shorePolicyTestServer(t, current, 0, func(p *pb.Policy) { submitted = p })
	defer shore.Close()
	bridge := New(shore.URL, "", nil).WithOperator("operator-ui")
	rec := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/policy",
		strings.NewReader(`{"text":"prioritize tankers and go quiet for 30 minutes","boost":["tanker"]}`)))
	if rec.Code != http.StatusOK || submitted == nil {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Readback string   `json:"readback"`
		Warnings []string `json:"warnings"`
		Compiled struct {
			Warnings []string `json:"warnings"`
		} `json:"compiled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return submitted, response.Readback, append(response.Warnings, response.Compiled.Warnings...)
}

func TestConfiguredOperatorUsedWithoutJWT(t *testing.T) {
	const operator = "configured-operator"
	stored := &pb.Policy{Interest: &pb.Interest{}, Layers: []*pb.PolicyLayer{{
		Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: operator,
		Rules: []*pb.InterestRule{{Id: "existing"}},
	}}}
	var requests []*pb.Policy
	shore := shorePolicyTestServer(t, stored, 0, func(p *pb.Policy) { requests = append(requests, p) })
	defer shore.Close()
	handler := New(shore.URL, "", nil).WithOperator(operator).Handler()
	for _, tc := range []struct{ path, body string }{
		{"/api/shore/policy", `{"text":"prioritize tankers","boost":["tanker"]}`},
		{"/api/shore/emcon", `{"mode":"SILENT"}`},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
	}
	if len(requests) != 2 {
		t.Fatalf("SetPolicy calls=%d, want 2", len(requests))
	}
	for _, request := range requests {
		if request.IssuedBy != operator || len(request.Layers) != 1 || request.Layers[0].IssuedBy != operator {
			t.Fatalf("configured issuer policy=%+v", request)
		}
	}
	if requests[0].Layers[0].Rules[0].Author != operator ||
		requests[1].Layers[0].GetEmcon().GetSetBy() != operator {
		t.Fatalf("configured issuer attribution: policy=%+v emcon=%+v", requests[0], requests[1])
	}
}

func TestTimedLayeredNonJWTEMCONUsesPerLayerExpiryWithoutCompiler(t *testing.T) {
	current := &pb.Policy{Interest: &pb.Interest{}, Layers: []*pb.PolicyLayer{
		{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72"},
		{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "operator-ui"},
	}}
	policy, readback, warnings := submitTimedDirectiveWithoutJWT(t, current)
	if policy.GetExpiresAtUnixMs() != 0 || len(policy.Layers) != 2 {
		t.Fatalf("layered policy expiry/layers=%d/%+v", policy.GetExpiresAtUnixMs(), policy.Layers)
	}
	wantAfter, wantBefore := time.Now().Add(29*time.Minute).UnixMilli(), time.Now().Add(31*time.Minute).UnixMilli()
	ship := policy.Layers[1]
	if ship.GetEmcon().GetExpiresAtUnixMs() < wantAfter || ship.GetEmcon().GetExpiresAtUnixMs() > wantBefore ||
		len(ship.Rules) != 1 || ship.Rules[0].GetExpiresAtUnixMs() < wantAfter ||
		ship.Rules[0].GetExpiresAtUnixMs() > wantBefore {
		t.Fatalf("layered timed policy=%+v", policy)
	}
	if len(warnings) != 0 || !strings.Contains(readback, "EMCON SILENT for 30 min.") {
		t.Fatalf("readback=%q warnings=%q", readback, warnings)
	}
}

func TestTimedFlatNonJWTUsesPerDirectiveExpiry(t *testing.T) {
	policy, _, _ := submitTimedDirectiveWithoutJWT(t, &pb.Policy{Interest: &pb.Interest{}})
	if policy.GetExpiresAtUnixMs() != 0 {
		t.Fatalf("flat policy expiry=%d, want 0", policy.GetExpiresAtUnixMs())
	}
	wantAfter, wantBefore := time.Now().Add(29*time.Minute).UnixMilli(), time.Now().Add(31*time.Minute).UnixMilli()
	if len(policy.Layers) != 1 || policy.Layers[0].IssuedBy != DefaultOperator ||
		policy.Layers[0].Echelon != pb.Echelon_ECHELON_SHIP || len(policy.Layers[0].Rules) != 1 ||
		policy.Layers[0].GetEmcon().GetMode() != pb.EmconMode_EMCON_MODE_SILENT ||
		policy.Layers[0].GetEmcon().GetExpiresAtUnixMs() < wantAfter ||
		policy.Layers[0].GetEmcon().GetExpiresAtUnixMs() > wantBefore ||
		policy.Layers[0].Rules[0].GetExpiresAtUnixMs() < wantAfter ||
		policy.Layers[0].Rules[0].GetExpiresAtUnixMs() > wantBefore {
		t.Fatalf("flat non-JWT timed policy=%+v", policy)
	}
}

func TestEMCONWithConfiguredIssuerPreservesOtherConsoleLayer(t *testing.T) {
	otherLayer := &pb.PolicyLayer{
		Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "ops-2",
		Rules: []*pb.InterestRule{{Id: "ops-2-rule", Author: "ops-2", BoostLabels: []string{"tanker"}}},
		Emcon: &pb.EmconSetting{
			Mode: pb.EmconMode_EMCON_MODE_NORMAL, Locked: true,
			SetBy: "ops-2", Echelon: pb.Echelon_ECHELON_SHIP,
		},
	}
	var submitted *pb.Policy
	shore := shorePolicyTestServer(t, &pb.Policy{Interest: &pb.Interest{}, Layers: []*pb.PolicyLayer{otherLayer}},
		0, func(p *pb.Policy) { submitted = p })
	defer shore.Close()
	rec := httptest.NewRecorder()
	New(shore.URL, "", nil).WithOperator("ops-1").Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/shore/emcon", strings.NewReader(`{"mode":"SILENT"}`)))
	if rec.Code != http.StatusOK || submitted == nil {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(submitted.Layers) != 2 || !proto.Equal(submitted.Layers[0], otherLayer) {
		t.Fatalf("layers=%+v, want the other console's layer preserved", submitted.Layers)
	}
	reused := submitted.Layers[1]
	if reused.Echelon != pb.Echelon_ECHELON_SHIP || reused.IssuedBy != "ops-1" ||
		len(reused.Rules) != 0 ||
		reused.GetEmcon().GetSetBy() != "ops-1" ||
		reused.GetEmcon().GetMode() != pb.EmconMode_EMCON_MODE_SILENT || reused.GetEmcon().GetLocked() {
		t.Fatalf("reused ship layer=%+v", reused)
	}
}

func TestFlatEMCONWrapsBoostFactorAsDirectiveWithoutJWT(t *testing.T) {
	current := &pb.Policy{Interest: &pb.Interest{BoostFactor: 3, BoostLabels: []string{"tanker"}}}
	var submitted *pb.Policy
	shore := shorePolicyTestServer(t, current, 0, func(p *pb.Policy) { submitted = p })
	defer shore.Close()
	rec := httptest.NewRecorder()
	New(shore.URL, "", nil).Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/shore/emcon", strings.NewReader(`{"mode":"SILENT"}`)))
	if rec.Code != http.StatusOK || submitted == nil {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(submitted.Layers) != 1 {
		t.Fatalf("flat policy was not wrapped in a layer: %+v", submitted)
	}
	layer := submitted.Layers[0]
	if layer.Echelon != pb.Echelon_ECHELON_SHIP || layer.IssuedBy != DefaultOperator ||
		layer.GetEmcon().GetMode() != pb.EmconMode_EMCON_MODE_SILENT ||
		len(layer.Rules) != 1 || layer.Rules[0].GetWeight() != 3 ||
		len(layer.Rules[0].GetBoostLabels()) != 1 || layer.Rules[0].GetBoostLabels()[0] != "tanker" {
		t.Fatalf("flat policy boost/EMCON was not preserved in the new layer: %+v", layer)
	}
}

func TestShoreAuthorizationFailureIsReturnedAsJSON(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, tc := range []struct{ path, body string }{
			{"/api/shore/policy", `{"text":"ignore submarines"}`},
			{"/api/shore/emcon", `{"mode":"SILENT"}`},
		} {
			t.Run(fmt.Sprintf("%d%s", status, tc.path), func(t *testing.T) {
				shore := shorePolicyTestServer(t, &pb.Policy{Interest: &pb.Interest{}}, status, nil)
				defer shore.Close()
				rec := httptest.NewRecorder()
				New(shore.URL, "", nil).Handler().ServeHTTP(rec,
					httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
				var response struct {
					Error string `json:"error"`
				}
				if rec.Code != status || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
					json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.Error != "opauth rejected" {
					t.Fatalf("status=%d content-type=%q body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
				}
			})
		}
	}
}

func TestConsoleDirectiveSurfacesShoreAuthorizationFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			shore := shorePolicyTestServer(t, &pb.Policy{Interest: &pb.Interest{}}, status, nil)
			defer shore.Close()
			rec := httptest.NewRecorder()
			NewConsole(New(shore.URL, "", nil)).Handler().ServeHTTP(rec,
				httptest.NewRequest(http.MethodPost, "/api/directives",
					strings.NewReader(`{"text":"ignore submarines","rules":[{"type":"suppress","classes":["submarine"]}]}`)))
			var response struct {
				Error string `json:"error"`
			}
			if rec.Code != status || json.Unmarshal(rec.Body.Bytes(), &response) != nil ||
				response.Error != "opauth rejected" {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
