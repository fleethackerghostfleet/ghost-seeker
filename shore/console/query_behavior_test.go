package console

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	"google.golang.org/protobuf/proto"
)

func queryService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Service{NodeID: "shore", St: st, Clk: clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
}

func seedQueryItems(t *testing.T, s *Service) []byte {
	t.Helper()
	ctx := context.Background()
	for _, entry := range []*pb.ManifestEntry{
		{ItemId: "known", Type: pb.ItemType_ITEM_TYPE_IMAGE, Source: "cam-port", CapturedAtUnixMs: 1234, Priority: 1, TopLabel: "cargo ship", TopConf: 0.8,
			Geo: &pb.Geo{Lat: 36, Lon: -75}, Forms: []*pb.FormSize{{Kind: pb.FormKind_FORM_KIND_FACT, Bytes: 14}}},
		{ItemId: "mystery", Type: pb.ItemType_ITEM_TYPE_IMAGE, Priority: 9, Unknownness: 0.9, Forms: []*pb.FormSize{{Kind: pb.FormKind_FORM_KIND_FACT, Bytes: 5}}},
		{ItemId: "reviewed", Type: pb.ItemType_ITEM_TYPE_RECORD, Priority: 5, Unknownness: 0.8, TopLabel: "bird"},
	} {
		if err := s.St.UpsertManifest(ctx, "edge-1", entry, s.Clk.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.St.SetFeatures(ctx, "reviewed", &pb.Features{ModelId: "detector-v2", Unknownness: 0.8, Detections: []*pb.Detection{{Label: "bird", Confidence: 0.7}}, Tags: map[string]string{"track": "track-1"}}, "bird"); err != nil {
		t.Fatal(err)
	}
	if err := s.St.SetOverride(ctx, "reviewed", "Patrol Boat", "TAO"); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"id":"known"}`)
	if _, complete, err := s.St.WriteChunk(ctx, "edge-1", &pb.Chunk{ItemId: "known", Form: pb.FormKind_FORM_KIND_FACT, Data: data, TotalBytes: int64(len(data)), Mime: "application/json"}, s.Clk.Now()); err != nil || !complete {
		t.Fatalf("fixture completion: %v %v", complete, err)
	}
	return data
}

func TestListItemsFilteringPaginationAndView(t *testing.T) {
	s, ctx := queryService(t), context.Background()
	empty, err := s.ListItems(ctx, connect.NewRequest(&pb.ListItemsRequest{}))
	if err != nil || empty.Msg.Total != 0 || len(empty.Msg.Items) != 0 {
		t.Fatalf("empty list: %v %v", empty, err)
	}
	data := seedQueryItems(t, s)
	for _, tc := range []struct {
		name  string
		query *pb.ListItemsRequest
		ids   []string
		total int64
		next  string
	}{
		{"first page", &pb.ListItemsRequest{Limit: 2}, []string{"mystery", "reviewed"}, 3, "2"},
		{"last page", &pb.ListItemsRequest{Limit: 2, PageToken: "2"}, []string{"known"}, 3, ""},
		{"beyond end", &pb.ListItemsRequest{PageToken: "99"}, nil, 3, ""},
		{"unknown excludes human review", &pb.ListItemsRequest{UnknownOnly: true}, []string{"mystery"}, 1, ""},
		{"override label", &pb.ListItemsRequest{Label: "Boat"}, []string{"reviewed"}, 1, ""},
		{"combined type and label", &pb.ListItemsRequest{Type: pb.ItemType_ITEM_TYPE_IMAGE, Label: "cargo"}, []string{"known"}, 1, ""},
		{"no match", &pb.ListItemsRequest{Label: "submarine"}, nil, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := s.ListItems(ctx, connect.NewRequest(tc.query))
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, item := range response.Msg.Items {
				ids = append(ids, item.Item.Id)
			}
			if !reflect.DeepEqual(ids, tc.ids) || response.Msg.Total != tc.total || response.Msg.NextPageToken != tc.next {
				t.Fatalf("got %v total=%d next=%q, want %v total=%d next=%q", ids, response.Msg.Total, response.Msg.NextPageToken, tc.ids, tc.total, tc.next)
			}
		})
	}
	known, err := s.GetItem(ctx, connect.NewRequest(&pb.GetItemRequest{ItemId: "known"}))
	if err != nil {
		t.Fatal(err)
	}
	v := known.Msg.Item
	if v.Item.Source != "cam-port" || v.Item.CapturedAtUnixMs != 1234 || v.Item.Geo.Lat != 36 || v.Score.Priority != 1 || !v.ManifestSent || v.Item.Features.Detections[0].Label != "cargo ship" || v.Item.Features.Detections[0].Confidence != 0.8 {
		t.Fatalf("manifest-only projection: %v", v)
	}
	if len(v.Item.Forms) != 1 || !v.Item.Forms[0].Ready || v.Item.Forms[0].Mime != "application/json" || len(v.FormStates) != 1 || !v.FormStates[0].Complete || v.FormStates[0].AckedThrough != int64(len(data)) {
		t.Fatalf("form completion projection: %v", v)
	}
	reviewed, err := s.GetItem(ctx, connect.NewRequest(&pb.GetItemRequest{ItemId: "reviewed"}))
	if err != nil {
		t.Fatal(err)
	}
	features := reviewed.Msg.Item.Item.Features
	if features.ModelId != "detector-v2" || features.Tags["track"] != "track-1" || features.LabelOverride != "Patrol Boat" || features.OverrideBy != "TAO" {
		t.Fatalf("rich FACT/human override lost: %v", features)
	}
	if _, err := s.GetItem(ctx, connect.NewRequest(&pb.GetItemRequest{ItemId: "missing"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing item: %v", err)
	}
	if _, err := s.ListItems(ctx, connect.NewRequest(&pb.ListItemsRequest{PageToken: "-1"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid page: %v", err)
	}
}

func TestStatusSelectsEdgeAndAgesHeartbeat(t *testing.T) {
	s, ctx := queryService(t), context.Background()
	if empty, err := s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{})); err != nil || empty.Msg.Link != nil || empty.Msg.ItemsTotal != 0 {
		t.Fatalf("empty status: %v %v", empty, err)
	}
	if empty, err := s.GetPolicy(ctx, connect.NewRequest(&pb.GetPolicyRequest{})); err != nil || empty.Msg.Policy != nil {
		t.Fatalf("empty policy: %v %v", empty, err)
	}
	seedQueryItems(t, s)
	wantPolicy := &pb.Policy{Id: "held-policy", Version: 7}
	if err := s.St.SetPolicy(ctx, wantPolicy); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPolicy(ctx, connect.NewRequest(&pb.GetPolicyRequest{})); err != nil || !proto.Equal(got.Msg.Policy, wantPolicy) {
		t.Fatalf("stored policy: %v %v", got, err)
	}
	lastSeen := s.Clk.Now().Add(-59 * time.Second)
	heartbeat := &pb.Heartbeat{LinkEstimateBps: 9600, QueuedItems: 8, PolicyId: "held-policy"}
	if err := s.St.Heartbeat(ctx, "edge-1", heartbeat, 123, lastSeen); err != nil {
		t.Fatal(err)
	}
	if err := s.St.Heartbeat(ctx, "edge-2", &pb.Heartbeat{LinkEstimateBps: 64000}, 999, s.Clk.Now()); err != nil {
		t.Fatal(err)
	}
	s.EdgeID = "edge-1"
	status, err := s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	got := status.Msg
	if got.Role != "shore" || got.NodeId != "shore" || got.PolicyId != "held-policy" || got.ItemsTotal != 3 || got.ItemsManifested != 3 || got.ItemsUnknown != 1 || got.ItemsComplete != 1 || got.BytesSent != 123 || !proto.Equal(got.Heartbeat, heartbeat) || !got.Link.Up || got.Link.EstimateBps != 9600 || got.Link.LastSuccessUnixMs != lastSeen.UnixMilli() {
		t.Fatalf("selected edge/status: %v", got)
	}
	s.Clk.(*clock.Virtual).Advance(time.Second)
	status, err = s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil || status.Msg.Link.Up {
		t.Fatalf("60-second heartbeat should be stale: %v %v", status, err)
	}
	s.EdgeID = "not-seen"
	status, err = s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil || status.Msg.Link != nil || status.Msg.Heartbeat != nil || status.Msg.BytesSent != 0 {
		t.Fatalf("unmatched edge leaked another heartbeat: %v %v", status, err)
	}
}

func TestBlobReadinessAndStorageErrors(t *testing.T) {
	s, ctx := queryService(t), context.Background()
	data := seedQueryItems(t, s)
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/blob/known/fact", 200}, {"/blob/mystery/fact", 409}, {"/blob/known/full", 409},
		{"/blob/missing/fact", 404}, {"/blob/bad!/fact", 404}, {"/blob/known", 404},
	} {
		rec := httptest.NewRecorder()
		s.BlobHandler().ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.code {
			t.Fatalf("%s: got %d, want %d", tc.path, rec.Code, tc.code)
		}
		if tc.code == 200 && (rec.Body.String() != string(data) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json")) {
			t.Fatalf("completed blob changed: %s %s", rec.Header(), rec.Body)
		}
	}
	if err := s.St.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListItems(ctx, connect.NewRequest(&pb.ListItemsRequest{})); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("ListItems storage error: %v", err)
	}
	if _, err := s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{})); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("GetStatus storage error: %v", err)
	}
	if _, err := s.GetPolicy(ctx, connect.NewRequest(&pb.GetPolicyRequest{})); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("GetPolicy storage error: %v", err)
	}
}
