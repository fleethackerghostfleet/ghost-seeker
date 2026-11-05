// Package console serves the operator API on the edge (ship-side reviewer).
package console

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

type Service struct {
	ghostseekerv1connect.UnimplementedConsoleServiceHandler
	NodeID string
	St     *store.Store
	Sch    *scheduler.Scheduler
	Clk    clock.Clock
	// Deduped reports captures folded into existing items (ingest.Ingester.Deduped); optional.
	Deduped func() int64
}

func view(r *store.Row) *pb.ItemView {
	v := &pb.ItemView{Item: r.Item, Score: r.Score, ManifestSent: r.ManifestAcked}
	for _, f := range r.Forms {
		v.FormStates = append(v.FormStates, &pb.FormState{Kind: f.Kind, Bytes: f.Bytes, AckedThrough: f.AckedThrough, Complete: f.Complete})
	}
	return v
}

func (s *Service) ListItems(ctx context.Context, req *connect.Request[pb.ListItemsRequest]) (*connect.Response[pb.ListItemsResponse], error) {
	rows, err := s.St.All(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	q := req.Msg
	var filtered []*store.Row
	for _, r := range rows {
		if q.Type != pb.ItemType_ITEM_TYPE_UNSPECIFIED && r.Item.Type != q.Type {
			continue
		}
		if q.UnknownOnly && r.Item.GetFeatures().GetUnknownness() < scoring.DefaultUnknownMin {
			continue
		}
		if q.Label != "" {
			l, _ := scoring.TopLabel(r.Item, 0)
			if !strings.Contains(strings.ToLower(l), strings.ToLower(q.Label)) {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	// Highest priority first.
	for i := 1; i < len(filtered); i++ {
		for j := i; j > 0 && filtered[j].Score.Priority > filtered[j-1].Score.Priority; j-- {
			filtered[j], filtered[j-1] = filtered[j-1], filtered[j]
		}
	}
	off, _ := strconv.Atoi(q.PageToken)
	lim := int(q.Limit)
	if lim <= 0 {
		lim = 100
	}
	resp := &pb.ListItemsResponse{Total: int64(len(filtered))}
	for i := off; i < len(filtered) && i < off+lim; i++ {
		resp.Items = append(resp.Items, view(filtered[i]))
	}
	if off+lim < len(filtered) {
		resp.NextPageToken = strconv.Itoa(off + lim)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GetItem(ctx context.Context, req *connect.Request[pb.GetItemRequest]) (*connect.Response[pb.GetItemResponse], error) {
	r, err := s.St.Get(ctx, req.Msg.ItemId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&pb.GetItemResponse{Item: view(r)}), nil
}

func (s *Service) GetQueue(ctx context.Context, req *connect.Request[pb.GetQueueRequest]) (*connect.Response[pb.GetQueueResponse], error) {
	rows, err := s.St.All(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	plan := s.Sch.BuildPlan(rows, s.Sch.Link().State().EstimateBps, s.Clk.Now())
	resp := &pb.GetQueueResponse{}
	var tb int64
	for i, e := range plan.Queue {
		tb += e.BytesRemaining
		if req.Msg.Limit <= 0 || i < int(req.Msg.Limit) {
			resp.Entries = append(resp.Entries, e)
		}
	}
	resp.TotalItems, resp.TotalBytesRemaining = int64(len(plan.Queue)), tb
	return connect.NewResponse(resp), nil
}

func (s *Service) GetPolicy(context.Context, *connect.Request[pb.GetPolicyRequest]) (*connect.Response[pb.GetPolicyResponse], error) {
	return connect.NewResponse(&pb.GetPolicyResponse{Policy: s.Sch.Policy()}), nil
}

func (s *Service) SetPolicy(ctx context.Context, req *connect.Request[pb.SetPolicyRequest]) (*connect.Response[pb.SetPolicyResponse], error) {
	if req.Msg.Policy == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("policy required"))
	}
	sup, boost, err := s.Sch.ApplyPolicy(ctx, req.Msg.Policy)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.SetPolicyResponse{Policy: s.Sch.Policy(), SuppressedCount: sup, BoostedCount: boost}), nil
}

func (s *Service) OverrideLabel(ctx context.Context, req *connect.Request[pb.OverrideLabelRequest]) (*connect.Response[pb.OverrideLabelResponse], error) {
	if req.Msg.Override == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("override required"))
	}
	if err := s.Sch.Override(ctx, req.Msg.Override); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.OverrideLabelResponse{}), nil
}

func (s *Service) RequestPull(ctx context.Context, req *connect.Request[pb.RequestPullRequest]) (*connect.Response[pb.RequestPullResponse], error) {
	if req.Msg.Pull == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pull required"))
	}
	if err := s.Sch.Pull(ctx, req.Msg.Pull); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.RequestPullResponse{}), nil
}

func (s *Service) GetStatus(ctx context.Context, _ *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	rows, err := s.St.All(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	st := s.Sch.Stats()
	resp := &pb.GetStatusResponse{NodeId: s.NodeID, Role: "edge", Link: s.Sch.Link().State(), PolicyId: s.Sch.Policy().Id,
		BytesSent: st.BytesPayload + st.BytesManifest, ItemsTotal: int64(len(rows))}
	if s.Deduped != nil {
		resp.ItemsDeduped = s.Deduped()
	}
	var queued, qb int64
	for _, r := range rows {
		if r.ManifestAcked {
			resp.ItemsManifested++
		}
		if r.Item.GetFeatures().GetUnknownness() >= scoring.DefaultUnknownMin && r.Item.GetFeatures().GetLabelOverride() == "" {
			resp.ItemsUnknown++
		}
		done := true
		for i := range r.Forms {
			f := &r.Forms[i]
			if !f.Complete && !scheduler.PullOnly(r, f) {
				done = false
				queued++
				qb += f.Bytes - f.AckedThrough
				break
			}
		}
		if done {
			resp.ItemsComplete++
		}
	}
	resp.Heartbeat = &pb.Heartbeat{SentAtUnixMs: s.Clk.Now().UnixMilli(), QueuedItems: queued, QueuedBytes: qb,
		UnmanifestedItems: resp.ItemsTotal - resp.ItemsManifested, PolicyId: resp.PolicyId, LinkEstimateBps: resp.Link.EstimateBps}
	return connect.NewResponse(resp), nil
}

// BlobHandler serves GET /blob/{item_id}/{form} from local storage.
func (s *Service) BlobHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/blob/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		kind := parseForm(parts[1])
		row, err := s.St.Get(r.Context(), parts[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f := row.Form(kind)
		if f == nil {
			http.NotFound(w, r)
			return
		}
		data, err := s.St.ReadFormAll(parts[0], kind)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", f.Mime)
		_, _ = w.Write(data)
	})
}

// ParseForm accepts "fact", "thumb", "crop", "full" or the enum number.
func parseForm(s string) pb.FormKind {
	switch strings.ToLower(s) {
	case "fact", "1":
		return pb.FormKind_FORM_KIND_FACT
	case "thumb", "2":
		return pb.FormKind_FORM_KIND_THUMB
	case "crop", "3":
		return pb.FormKind_FORM_KIND_CROP
	default:
		return pb.FormKind_FORM_KIND_FULL
	}
}

func ParseForm(s string) pb.FormKind { return parseForm(s) }
