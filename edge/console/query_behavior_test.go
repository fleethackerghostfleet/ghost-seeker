package console

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

func queryService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	sch, err := scheduler.New(scheduler.DefaultConfig("edge"), st, link.NewConnect("http://unused", time.Second, link.NewEstimator(64000, 300, 1e9)), scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{NodeID: "edge", St: st, Sch: sch, Clk: clk}
}

func seedQueryItems(t *testing.T, s *Service) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	payloads := map[string][]byte{}
	scores := map[string]*pb.Score{}
	for _, tc := range []struct {
		id, label, override string
		kind                pb.ItemType
		unknown             float32
		priority            float64
	}{
		{"ship", "cargo ship", "", pb.ItemType_ITEM_TYPE_IMAGE, 0.1, 1},
		{"unknown", "", "", pb.ItemType_ITEM_TYPE_IMAGE, 0.9, 9},
		{"reviewed", "bird", "Patrol Boat", pb.ItemType_ITEM_TYPE_IMAGE, 0, 5},
		{"record", "cargo ship", "", pb.ItemType_ITEM_TYPE_RECORD, 0.1, 3},
	} {
		data := []byte(`{"id":"` + tc.id + `"}`)
		payloads[tc.id] = data
		item := &pb.Item{Id: tc.id, Type: tc.kind, Source: "cam-port", CapturedAtUnixMs: s.Clk.Now().UnixMilli(),
			Features: &pb.Features{Unknownness: tc.unknown, LabelOverride: tc.override, Detections: []*pb.Detection{{Label: tc.label, Confidence: 0.8}}}}
		if _, err := s.St.Put(ctx, item, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: data}); err != nil {
			t.Fatal(err)
		}
		scores[tc.id] = &pb.Score{Priority: tc.priority, Reasons: []*pb.Reason{{Scorer: "novelty", Note: "unseen"}, {Scorer: "empty"}}}
	}
	if err := s.St.UpdateScores(ctx, scores); err != nil {
		t.Fatal(err)
	}
	return payloads
}

func TestListItemsFiltersAndPages(t *testing.T) {
	s := queryService(t)
	ctx := context.Background()
	empty, err := s.ListItems(ctx, connect.NewRequest(&pb.ListItemsRequest{}))
	if err != nil || empty.Msg.Total != 0 || len(empty.Msg.Items) != 0 || empty.Msg.NextPageToken != "" {
		t.Fatalf("empty list: %v %v", empty, err)
	}
	seedQueryItems(t, s)
	for _, tc := range []struct {
		name  string
		query *pb.ListItemsRequest
		ids   []string
		total int64
		next  string
	}{
		{"first page", &pb.ListItemsRequest{Limit: 2}, []string{"unknown", "reviewed"}, 4, "2"},
		{"last page", &pb.ListItemsRequest{Limit: 2, PageToken: "2"}, []string{"record", "ship"}, 4, ""},
		{"beyond end", &pb.ListItemsRequest{PageToken: "99"}, nil, 4, ""},
		{"unknown", &pb.ListItemsRequest{UnknownOnly: true}, []string{"unknown"}, 1, ""},
		{"human override", &pb.ListItemsRequest{Label: "BOAT"}, []string{"reviewed"}, 1, ""},
		{"combined type and label", &pb.ListItemsRequest{Type: pb.ItemType_ITEM_TYPE_RECORD, Label: "cargo"}, []string{"record"}, 1, ""},
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
	for _, query := range []*pb.ListItemsRequest{{PageToken: "-1"}, {PageToken: "invalid"}, {Label: strings.Repeat("x", MaxLabelChars+1)}} {
		if _, err := s.ListItems(ctx, connect.NewRequest(query)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid query: %v", err)
		}
	}
	if _, err := s.GetItem(ctx, connect.NewRequest(&pb.GetItemRequest{ItemId: "missing"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing item: %v", err)
	}

	rec := httptest.NewRecorder()
	s.ShipItemsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/ship/items", nil))
	var body struct {
		Items []shipItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(body.Items) != 4 || body.Items[0].ID != "unknown" || body.Items[1].Label != "Patrol Boat" || !reflect.DeepEqual(body.Items[0].Reasons, []string{"novelty: unseen"}) {
		t.Fatalf("ship stream projection: %d %+v", rec.Code, body)
	}
}

func TestGetItemStatusAndBlobProgress(t *testing.T) {
	s := queryService(t)
	ctx := context.Background()
	payloads := seedQueryItems(t, s)
	if err := s.St.MarkManifestAcked(ctx, []string{"ship"}); err != nil {
		t.Fatal(err)
	}
	if err := s.St.SetAcked(ctx, "ship", pb.FormKind_FORM_KIND_FACT, 1, false); err != nil {
		t.Fatal(err)
	}
	item, err := s.GetItem(ctx, connect.NewRequest(&pb.GetItemRequest{ItemId: "ship"}))
	if err != nil {
		t.Fatal(err)
	}
	view := item.Msg.Item
	if !view.ManifestSent || view.Item.Source != "cam-port" || len(view.FormStates) != 1 || view.FormStates[0].AckedThrough != 1 || view.FormStates[0].Complete || view.FormStates[0].Bytes != int64(len(payloads["ship"])) {
		t.Fatalf("item progress: %v", view)
	}
	s.Deduped = func() int64 { return 7 }
	status, err := s.GetStatus(ctx, connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var remaining int64 = -1
	for _, data := range payloads {
		remaining += int64(len(data))
	}
	got := status.Msg
	if got.Role != "edge" || got.NodeId != "edge" || got.ItemsTotal != 4 || got.ItemsManifested != 1 || got.ItemsComplete != 0 || got.ItemsUnknown != 1 || got.ItemsDeduped != 7 || got.Prioritizer != scoring.PrioritizerEdgeml || got.Heartbeat.QueuedItems != 4 || got.Heartbeat.QueuedBytes != remaining || got.Heartbeat.UnmanifestedItems != 3 {
		t.Fatalf("status counters: %v", got)
	}
	for _, tc := range []struct {
		path string
		code int
	}{{"/blob/ship/fact", 200}, {"/blob/ship/full", 404}, {"/blob/missing/fact", 404}, {"/blob/bad!/fact", 404}, {"/blob/ship", 404}} {
		rec := httptest.NewRecorder()
		s.BlobHandler().ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.code {
			t.Fatalf("%s: got %d, want %d", tc.path, rec.Code, tc.code)
		}
		if tc.code == 200 && (rec.Body.String() != string(payloads["ship"]) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json")) {
			t.Fatalf("blob changed: %s %s", rec.Header(), rec.Body)
		}
	}
	if err := s.St.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListItems(ctx, connect.NewRequest(&pb.ListItemsRequest{})); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("storage error: %v", err)
	}
}
