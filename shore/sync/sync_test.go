package sync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

func TestExchangeRejectsWritesFromAnotherUnit(t *testing.T) {
	const key = "test-signing-key-at-least-32-bytes-long"
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	auth, err := unitauth.New(true, key)
	if err != nil {
		t.Fatal(err)
	}
	svc := ssync.New(st, clock.Real{}, nil)
	arrivals := 0
	svc.OnArrival = func(string, pb.FormKind) { arrivals++ }
	path, handler := ghostseekerv1connect.NewSyncServiceHandler(ssync.Handler{Service: svc}, connect.WithInterceptors(auth))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := ghostseekerv1connect.NewSyncServiceClient(server.Client(), server.URL)
	tokenFor := func(edge string) string {
		t.Helper()
		token, err := unitauth.Mint(edge, key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	exchange := func(edge, token string, req *pb.ExchangeRequest) *pb.ExchangeResponse {
		t.Helper()
		req.EdgeId = edge
		connectReq := connect.NewRequest(req)
		connectReq.Header().Set("Authorization", "Bearer "+token)
		resp, err := client.Exchange(ctx, connectReq)
		if err != nil {
			t.Fatalf("%s Exchange: %v", edge, err)
		}
		return resp.Msg
	}

	const ownedID = "01M6MSQ0RSB2E2N3ARPS6QMJHT"
	const ownID = "01M6MSQ0RSB2E2N3ARPS6QMJHV"
	full := pb.FormKind_FORM_KIND_FULL
	original := []byte("AAAAAA")
	originalHash := sha256.Sum256(original)
	manifest := func(id, label string, priority float64) *pb.ManifestEntry {
		return &pb.ManifestEntry{ItemId: id, Type: pb.ItemType_ITEM_TYPE_IMAGE, TopLabel: label, Priority: priority,
			Forms: []*pb.FormSize{{Kind: full, Bytes: 6}}}
	}
	first := exchange("edge-1", tokenFor("edge-1"), &pb.ExchangeRequest{
		Manifest: []*pb.ManifestEntry{manifest(ownedID, "warship", 0.9)},
		Chunks: []*pb.Chunk{{ItemId: ownedID, Form: full, TotalBytes: int64(len(original)),
			Data: original, ContentHash: hex.EncodeToString(originalHash[:])}},
	})
	if !hasAck(first, ownedID, true) || !hasAck(first, ownedID, false) {
		t.Fatalf("edge-1 response lacks manifest/chunk acknowledgments: %+v", first.Acks)
	}
	if arrivals != 1 {
		t.Fatalf("OnArrival calls after legitimate chunk = %d, want 1", arrivals)
	}

	attacker := []byte("BBBBBB")
	attackerHash := sha256.Sum256(attacker)
	second := exchange("edge-2", tokenFor("edge-2"), &pb.ExchangeRequest{
		Manifest: []*pb.ManifestEntry{manifest(ownedID, "buoy", 0.01), manifest(ownID, "fishing vessel", 0.4)},
		Chunks: []*pb.Chunk{{ItemId: ownedID, Form: full, TotalBytes: int64(len(attacker)),
			Data: attacker, ContentHash: hex.EncodeToString(attackerHash[:])}},
		Overrides: []*pb.LabelOverride{{ItemId: ownedID, Label: "buoy", By: "edge-2"}},
	})
	for _, ack := range second.Acks {
		if ack.ItemId == ownedID {
			t.Fatalf("edge-2 received acknowledgment for rejected item: %+v", ack)
		}
	}
	if !hasAck(second, ownID, true) {
		t.Fatalf("edge-2's own item was not acked: %+v", second.Acks)
	}
	item, err := st.Get(ctx, ownedID)
	if err != nil {
		t.Fatal(err)
	}
	if item.EdgeID != "edge-1" || item.Manifest.Priority != 0.9 || item.Manifest.TopLabel != "warship" {
		t.Fatalf("rejected exchange changed item: owner=%q priority=%v label=%q",
			item.EdgeID, item.Manifest.Priority, item.Manifest.TopLabel)
	}
	stored, err := st.ReadForm(ownedID, full)
	if err != nil || string(stored) != string(original) {
		t.Fatalf("FULL after rejected exchange = %q, err=%v", stored, err)
	}
	if item.LabelOverride != "" {
		t.Fatalf("rejected override changed label to %q", item.LabelOverride)
	}
	if arrivals != 1 {
		t.Fatalf("OnArrival called for rejected chunk: calls=%d", arrivals)
	}
}

func hasAck(resp *pb.ExchangeResponse, itemID string, manifest bool) bool {
	for _, ack := range resp.Acks {
		if ack.ItemId == itemID && ack.Manifest == manifest {
			return true
		}
	}
	return false
}
