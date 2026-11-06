package scheduler

import (
	"context"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestBurstOnlySendsRequestedImageForm(t *testing.T) {
	for _, requested := range []pb.FormKind{pb.FormKind_FORM_KIND_FACT, pb.FormKind_FORM_KIND_THUMB, pb.FormKind_FORM_KIND_CROP, pb.FormKind_FORM_KIND_FULL, pb.FormKind_FORM_KIND_UNSPECIFIED} {
		t.Run(requested.String(), func(t *testing.T) {
			s, st, _, clk := setup(t, Prioritized)
			ctx := context.Background()
			row := addImage(t, s, st, clk, "boat", .9, 0, 1000)
			if err := s.Pull(ctx, &pb.Pull{ItemId: row.Item.Id, Form: requested, RequestedBy: "operator"}); err != nil {
				t.Fatal(err)
			}
			row, err := st.Get(ctx, row.Item.Id)
			if err != nil {
				t.Fatal(err)
			}
			row.ManifestAcked = true
			row.Forms = append(row.Forms[:2], append([]store.FormState{{Kind: pb.FormKind_FORM_KIND_CROP, Bytes: 500, Ready: true}}, row.Forms[2:]...)...)
			if _, _, err := s.ApplyPolicy(ctx, &pb.Policy{Id: "burst", Emcon: pb.EmconMode_EMCON_MODE_BURST}); err != nil {
				t.Fatal(err)
			}
			want := requested
			if want == pb.FormKind_FORM_KIND_UNSPECIFIED {
				want = pb.FormKind_FORM_KIND_FULL
			}
			plan := s.BuildPlan([]*store.Row{row}, 1_000_000, clk.Now())
			found := false
			for _, chunk := range plan.Chunks {
				if chunk.Form == want {
					found = true
				}
				if chunk.Form != pb.FormKind_FORM_KIND_FACT && chunk.Form != want {
					t.Fatalf("pull %v sent unrequested %v", requested, chunk.Form)
				}
			}
			if !found {
				t.Fatalf("requested %v not sent", want)
			}
			for i := range row.Forms {
				if row.Forms[i].Kind == want {
					row.Forms[i].Complete = true
				}
			}
			for _, chunk := range s.BuildPlan([]*store.Row{row}, 1_000_000, clk.Now()).Chunks {
				if chunk.Form != pb.FormKind_FORM_KIND_FACT {
					t.Fatalf("completed pull released %v", chunk.Form)
				}
			}
		})
	}
}
