package shorebridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestShoreOutageKeepsLastPicture(t *testing.T) {
	for _, failure := range []string{"GetStatus", "GetPolicy", "ListItems", "second-page", "all"} {
		t.Run(failure, func(t *testing.T) {
			failed, empty := false, false
			item := view("retained", 1000, nil, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_THUMB: true})
			shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method := r.URL.Path[len(consolePath):]
				var req pb.ListItemsRequest
				if method == "ListItems" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					if err := protojson.Unmarshal(body, &req); err != nil {
						t.Error(err)
					}
				}
				if failed && (failure == "all" || method == failure || failure == "second-page" && req.PageToken != "") {
					http.Error(w, "shore unavailable", http.StatusServiceUnavailable)
					return
				}
				var response proto.Message
				switch method {
				case "GetStatus":
					response = &pb.GetStatusResponse{NodeId: "shore"}
				case "GetPolicy":
					response = &pb.GetPolicyResponse{Policy: &pb.Policy{Id: "standing-policy", Text: "prioritize submarines"}}
				case "ListItems":
					list := &pb.ListItemsResponse{}
					if !empty {
						list.Items = []*pb.ItemView{item}
					}
					if failed && failure == "second-page" {
						list.Items = []*pb.ItemView{view("partial", 2000, nil, nil)}
						list.NextPageToken = "next"
					}
					response = list
				default:
					http.NotFound(w, r)
					return
				}
				buf, err := protojson.Marshal(response)
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(buf)
			}))
			defer shore.Close()
			bridge := New(shore.URL, "", nil)
			console := NewConsole(bridge)
			check := func(wantError bool, wantItems int) {
				t.Helper()
				bridge.refresh(context.Background())
				var snap Snapshot
				if err := json.Unmarshal(bridge.snap, &snap); err != nil {
					t.Fatal(err)
				}
				if (snap.Shore.Error != "") != wantError || snap.Shore.OK == wantError || len(snap.Objects) != wantItems || len(console.base) != wantItems {
					t.Fatalf("error=%q ok=%v objects=%d console=%d", snap.Shore.Error, snap.Shore.OK, len(snap.Objects), len(console.base))
				}
				if (console.live.Shore.Error != "") != wantError {
					t.Fatal("console lost outage state")
				}
				if wantItems > 0 && (snap.Objects[0].Best.ID != "retained" || snap.PolicyID != "standing-policy") {
					t.Fatal("last-known picture or policy replaced")
				}
			}
			failed = true
			check(true, 0)
			failed = false
			check(false, 1)
			failed = true
			check(true, 1)
			check(true, 1)
			failed = false
			check(false, 1)
			empty = true
			check(false, 0)
		})
	}
}
