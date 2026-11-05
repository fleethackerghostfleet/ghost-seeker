package store

import (
	"context"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/proto"
)

func TestPolicyBackendPersistsStateAndNewestFirstAudit(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &pb.Policy{Id: "p1", Version: 7, Text: "last good"}
	if err := st.SavePolicyState(ctx, p, 9); err != nil {
		t.Fatal(err)
	}
	got, highWater, err := st.LoadPolicyState(ctx)
	if err != nil || highWater != 9 || !proto.Equal(got, p) {
		t.Fatalf("loaded policy=%v highwater=%d err=%v", got, highWater, err)
	}
	for i, result := range []string{"applied", "rejected"} {
		if err := st.AppendPolicyAudit(ctx, policy.AuditEntry{AtUnixMs: int64(i + 1), PolicyID: p.Id, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := st.ListPolicyAudit(ctx, 1)
	if err != nil || len(entries) != 1 || entries[0].Result != "rejected" {
		t.Fatalf("audit=%+v err=%v", entries, err)
	}
	entries, err = st.ListPolicyAudit(ctx, 0)
	if err != nil || len(entries) != 2 || entries[0].Result != "rejected" || entries[1].Result != "applied" {
		t.Fatalf("audit=%+v err=%v", entries, err)
	}
}
